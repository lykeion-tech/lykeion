package labmemory

import (
	"encoding/json"
	"errors"
	db "lykeion/server/pkg/db/generated"
	"strings"
	"testing"

	"github.com/google/uuid"
	"lykeion/server/internal/store"
)

func manualNote(t *testing.T, f *fixture) Ref {
	t.Helper()
	id := uuid.New()
	f.exec(`INSERT INTO inbox_item(id,workspace_id,card_id,user_id,kind,text,title,context) VALUES($1,$2,$3,$4,'manual_decision','','Private zirconium plan','Secret laboratory context')`, id, f.ws, f.study, f.user)
	return Ref{Kind: "decision", ID: id}
}

func TestManualDecisionLabAccess(t *testing.T) {
	f := setup(t)
	note := manualNote(t, f)
	fact, err := f.read(note)
	if err != nil || fact.Title != "Private zirconium plan" || !strings.Contains(fact.Text, "Secret laboratory context") {
		t.Fatalf("creator read: %+v %v", fact, err)
	}
	outsider := uuid.New()
	f.exec(`INSERT INTO "user"(id,email,name) VALUES($1,'foreign@example.org','Foreign')`, outsider)
	f.exec(`INSERT INTO member(workspace_id,user_id,role) VALUES($1,$2,'admin')`, f.ws, outsider)
	// An erroneous shared cache candidate belonging to another creator is reauthorized.
	foreign := uuid.New()
	f.exec(`INSERT INTO inbox_item(id,workspace_id,user_id,kind,text,title) VALUES($1,$2,$3,'manual_decision','','Foreign hidden')`, foreign, f.ws, outsider)
	cached := newTools(t, f, searchStub{f.ws, []Ref{{Kind: "decision", ID: foreign}}, nil})
	if out := cached.call("search", map[string]any{"query": "unmatched"}, false); strings.Contains(out, foreign.String()) || strings.Contains(out, "Foreign hidden") {
		t.Fatalf("cached private hit escaped: %s", out)
	}
	s := &store.Store{Pool: f.env.App, LessonsEnabled: true}
	err = s.InWorkspace(t.Context(), f.ws, outsider, func(tx *store.Tx) error { _, e := Read(t.Context(), tx, outsider, note); return e })
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign admin read: %v", err)
	}
	tf := newTools(t, f, searchStub{f.ws, nil, nil})
	if out := tf.call("search", map[string]any{"query": "zirconium"}, false); !strings.Contains(out, note.String()) {
		t.Fatalf("creator search: %s", out)
	}
	if out := tf.call("links", map[string]any{"ref": Ref{Kind: "study", ID: f.study}.String()}, false); !strings.Contains(out, note.String()) {
		t.Fatalf("creator links: %s", out)
	}
}

func TestManualDecisionNeverSharedMemory(t *testing.T) {
	f := setup(t)
	note := manualNote(t, f)
	err := (&store.Store{Pool: f.env.App, LessonsEnabled: true}).Maintenance(t.Context(), func(tx *store.Tx) error {
		if e := tx.Workspace(t.Context(), f.ws); e != nil {
			return e
		}
		if _, e := ReadMaintenance(t.Context(), tx, note); !errors.Is(e, ErrNotFound) {
			t.Fatalf("maintenance read: %v", e)
		}
		study, e := ReadMaintenance(t.Context(), tx, Ref{Kind: "study", ID: f.study})
		if e != nil {
			return e
		}
		for _, link := range study.Links {
			if link == note {
				t.Fatal("private study link in maintenance")
			}
		}
		refs, e := tx.LabFactRefs(t.Context(), "", 33)
		if e != nil {
			return e
		}
		for _, r := range refs {
			if r == note.String() {
				t.Fatal("private reference indexed")
			}
		}
		if _, e := ReadMaintenance(t.Context(), tx, Ref{Kind: "decision", ID: f.decision}); e != nil {
			t.Fatalf("shared decision: %v", e)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestManualDecisionHistoryRechecksAccess(t *testing.T) {
	x := runningTurn(t)
	note := manualNote(t, x.fixture)
	x.log(note.String(), "read")
	if p, e := x.submit(t.Context(), "fact", note.String()); e != nil || len(p) > 0 {
		t.Fatal(e, p)
	}
	verify := func(want bool) {
		t.Helper()
		e := x.runs.Store.InWorkspaceSnapshot(t.Context(), x.ws, x.user, func(tx *store.Tx) error {
			rows, e := tx.Q.ListPrivateLabMessages(t.Context(), db.ListPrivateLabMessagesParams{ChatID: *x.run.LabChatID, PageSize: 1})
			if e != nil {
				return e
			}
			ss, ee, safe, e := NewDisplayReader(tx, x.user, *x.run.LabChatID).Answer(t.Context(), rows[0])
			if e != nil {
				return e
			}
			if safe != want || !want && (len(ss) != 0 || len(ee) != 0) {
				t.Fatalf("history safe=%v want=%v statements=%v evidence=%v", safe, want, ss, ee)
			}
			return nil
		})
		if e != nil {
			t.Fatal(e)
		}
	}
	verify(true)
	// Model a later study visibility restriction while the creator remains a member.
	x.exec(`CREATE POLICY manual_note_test_hide ON card AS RESTRICTIVE USING (id<>'` + x.study.String() + `'::uuid)`)
	t.Cleanup(func() { x.exec(`DROP POLICY IF EXISTS manual_note_test_hide ON card`) })
	verify(false)
	if _, e := x.read(note); !errors.Is(e, ErrNotFound) {
		t.Fatalf("hidden linked note read: %v", e)
	}
}

func TestManualDecisionNeverSharedMemoryMaliciousLesson(t *testing.T) {
	f := setup(t)
	note := manualNote(t, f)
	fact, e := f.read(note)
	if e != nil {
		t.Fatal(e)
	}
	cites, _ := json.Marshal([]Citation{{Ref: note.String(), Version: fact.Version}})
	id := uuid.New()
	f.exec(`INSERT INTO lesson(id,workspace_id,kind,text,cites,checked_at,model) VALUES($1,$2,'lab','Leaked lesson',$3,now(),'test')`, id, f.ws, cites)
	if _, e = f.read(Ref{Kind: "lesson", ID: id}); !errors.Is(e, ErrNotFound) {
		t.Fatalf("checked private lesson cite opened: %v", e)
	}
}
