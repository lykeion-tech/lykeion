package doc

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"lykeion/server/internal/store"
	"lykeion/server/internal/testutil"
	db "lykeion/server/pkg/db/generated"
	"os"
	"reflect"
	"strings"
	"testing"
)

func hunkFixture(t *testing.T, text string) (*store.Store, File, uuid.UUID, uuid.UUID, uuid.UUID) {
	t.Helper()
	env := testutil.Setup(t)
	ctx := t.Context()
	ws, who, project, file, proposal, run, chat := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, e := env.Owner.Exec(ctx, q, args...); e != nil {
			t.Fatal(e)
		}
	}
	other := uuid.New()
	exec(`INSERT INTO "user"(id,email) VALUES($1,'hunk@test'),($2,'hunk-b@test')`, who, other)
	exec(`INSERT INTO workspace(id,slug,name) VALUES($1,'hunks','hunks')`, ws)
	exec(`INSERT INTO member(workspace_id,user_id,role) VALUES($1,$2,'owner'),($1,$3,'member')`, ws, who, other)
	exec(`INSERT INTO coresearch_project(id,workspace_id,name,created_by,mode) VALUES($1,$2,'paper',$3,'cowrite')`, project, ws, who)
	exec(`INSERT INTO coresearch_member(workspace_id,project_id,user_id,role) VALUES($1,$2,$3,'owner'),($1,$2,$4,'member')`, ws, project, who, other)
	exec(`INSERT INTO coresearch_file(id,workspace_id,project_id,path) VALUES($1,$2,$3,'main.tex')`, file, ws, project)
	exec(`INSERT INTO coresearch_file_version(workspace_id,file_id,n,sha256,size,blob_key,author_user_id) VALUES($1,$2,1,repeat('a',64),$3,'seed',$4)`, ws, file, len(text), who)
	exec(`INSERT INTO coresearch_proposal(id,workspace_id,project_id,created_by) VALUES($1,$2,$3,$4)`, proposal, ws, project, who)
	exec(`INSERT INTO coresearch_chat(id,workspace_id,project_id,owner_id,mode,cli) VALUES($1,$2,$3,$4,'cowrite','claude')`, chat, ws, project, who)
	exec(`INSERT INTO agent_run(id,workspace_id,kind,requester_id,responsible_user_id,board_column,round,harness,role,prompt,skill,status,coresearch_chat_id) VALUES($1,$2,'coresearch',$3,$3,'',0,'claude','coresearch','x','','queued',$4)`, run, ws, who, chat)
	return &store.Store{Pool: env.App}, File{Record: db.CoresearchFile{ID: file, WorkspaceID: ws, ProjectID: project, Path: "main.tex"}, InitialText: text, InitialHead: 1}, who, run, proposal
}
func stepText(ctx context.Context, tx *store.Tx, f File, from, to int, text string, who uuid.UUID) error {
	d, e := Open(ctx, tx, f)
	if e != nil {
		return e
	}
	raw, _ := json.Marshal(Replace(from, to, text, Length(d.Text)))
	_, e = Push(ctx, tx, f, d.Version, []Step{{Changes: raw, ClientID: "person"}}, Author{UserID: &who})
	return e
}
func TestStaleHunkRefusedOthersApply(t *testing.T) {
	st, f, who, run, p := hunkFixture(t, "one two three")
	ctx := t.Context()
	e := st.InWorkspace(ctx, f.Record.WorkspaceID, who, func(tx *store.Tx) error {
		for i := 0; i < 4; i++ {
			if e := stepText(ctx, tx, f, 0, 0, "", who); e != nil {
				return e
			}
		}
		if e := stepText(ctx, tx, f, 4, 7, "changed", who); e != nil {
			return e
		}
		results, e := ApplyHunks(ctx, tx, f, 4, []Hunk{{"one", "ONE"}, {"two", "TWO"}, {"three", "THREE"}}, run, p)
		if e != nil {
			return e
		}
		if len(results) != 3 || !results[0].Applied || results[1].Applied || results[1].Current != "changed" || !results[2].Applied {
			t.Fatalf("results %+v", results)
		}
		d, e := Open(ctx, tx, f)
		if d.Text != "ONE changed THREE" || d.Version != 7 {
			t.Fatalf("doc %+v", d)
		}
		return e
	})
	if e != nil {
		t.Fatal(e)
	}
}
func TestHunkAnchorRules(t *testing.T) {
	st, f, who, run, p := hunkFixture(t, "same same unique")
	ctx := t.Context()
	e := st.InWorkspace(ctx, f.Record.WorkspaceID, who, func(tx *store.Tx) error {
		if _, e := Open(ctx, tx, f); e != nil {
			return e
		}
		if _, e := ApplyHunks(ctx, tx, f, 0, []Hunk{{"unique", "changed"}, {"", "x"}}, run, p); !errors.Is(e, ErrEmptyFind) {
			t.Fatalf("empty find %v", e)
		}
		r, e := ApplyHunks(ctx, tx, f, 0, []Hunk{{"absent", "x"}, {"same", "x"}}, run, p)
		if e != nil {
			return e
		}
		if r[0].Applied || r[1].Applied || r[1].Current != "ambiguous" {
			t.Fatalf("rules %+v", r)
		}
		d, e := Open(ctx, tx, f)
		if d.Version != 0 {
			t.Fatal("partial validation committed")
		}
		return e
	})
	if e != nil {
		t.Fatal(e)
	}
}
func TestKeepUndo(t *testing.T) {
	st, f, who, run, p := hunkFixture(t, "a😀b")
	ctx := t.Context()
	e := st.InWorkspace(ctx, f.Record.WorkspaceID, who, func(tx *store.Tx) error {
		r, e := ApplyHunks(ctx, tx, f, 0, []Hunk{{"😀", "insert"}}, run, p)
		if e != nil {
			return e
		}
		if e = Decide(ctx, tx, f, *r[0].ChangeID, who, false); e != nil {
			return e
		}
		d, e := Open(ctx, tx, f)
		if e != nil {
			return e
		}
		if d.Text != "a😀b" || d.Version != 2 {
			t.Fatalf("undo %+v", d)
		}
		r, e = ApplyHunks(ctx, tx, f, 2, []Hunk{{"😀", "kept"}}, run, p)
		if e != nil {
			return e
		}
		if e = Decide(ctx, tx, f, *r[0].ChangeID, who, true); e != nil {
			return e
		}
		d, e = Open(ctx, tx, f)
		if d.Text != "akeptb" || d.Version != 3 {
			t.Fatalf("keep %+v", d)
		}
		return e
	})
	if e != nil {
		t.Fatal(e)
	}
}

func TestStepsInterleaveRandomisedKeepUndo(t *testing.T) {
	var fixture struct {
		Initial        string
		InitialClients []referenceDraft
		Trace          []struct {
			Kind             string
			Writer, Change   int
			Version, Base    int64
			Observed, Result string
			Draft, NextDraft referenceDraft
			Accepted         json.RawMessage
			Rebases          []referenceRebase
			Added, Ranges    []referenceRange
		}
	}
	data, e := os.ReadFile("testdata/decisions.json")
	if e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(data, &fixture); e != nil {
		t.Fatal(e)
	}
	st, f, who, run, p := hunkFixture(t, fixture.Initial)
	ctx := t.Context()
	ids := map[int]uuid.UUID{}
	type clientState struct {
		version int64
		text    string
		draft   referenceDraft
	}
	clients := make([]clientState, 3)
	for i, draft := range fixture.InitialClients {
		clients[i] = clientState{text: fixture.Initial, draft: draft}
	}
	var other uuid.UUID
	if e := st.InWorkspace(ctx, f.Record.WorkspaceID, who, func(tx *store.Tx) error {
		members, e := tx.Q.ListCoresearchMembers(ctx, db.ListCoresearchMembersParams{WorkspaceID: tx.WS, ProjectID: f.Record.ProjectID})
		if e != nil {
			return e
		}
		for _, m := range members {
			if m.UserID != who {
				other = m.UserID
			}
		}
		return nil
	}); e != nil || other == uuid.Nil {
		t.Fatalf("second writer %v", e)
	}
	current := int64(0)
	keeps, undos, stales := 0, 0, 0
	for i, want := range fixture.Trace {
		clicker := who
		if want.Writer == 1 {
			clicker = other
		}
		e = st.InWorkspace(ctx, f.Record.WorkspaceID, clicker, func(tx *store.Tx) error {
			if _, e := Open(ctx, tx, f); e != nil {
				return e
			}
			if want.Kind == "edit" {
				client := &clients[want.Writer]
				if client.version != want.Base || client.text != want.Observed || !reflect.DeepEqual(client.draft, want.Draft) {
					t.Fatalf("event%d lost stale draft", i)
				}
				raw, _ := json.Marshal(Replace(client.draft.From, client.draft.To, client.draft.Insert, Length(client.text)))
				author := Author{UserID: &clicker}
				if want.Writer == 2 {
					author = Author{AgentRunID: &run}
				}
				if client.version != current {
					_, e := Push(ctx, tx, f, client.version, []Step{{Changes: raw, ClientID: "draft"}}, author)
					var behind ErrBehind
					if !errors.As(e, &behind) || behind.Current != current {
						t.Fatalf("event%d stale %v", i, e)
					}
					stales++
					remote, e := Steps(ctx, tx, f, client.version, current)
					if e != nil {
						return e
					}
					if len(remote) != len(want.Rebases) {
						t.Fatal("remote history differs")
					}
					for j, s := range remote {
						r := want.Rebases[j]
						assertChangesEqual(t, s.Changes, r.Remote)
						c, e := Parse(s.Changes)
						if e != nil {
							return e
						}
						client.text, e = c.Apply(client.text)
						if e != nil {
							return e
						}
						if client.text != r.Observed {
							t.Fatal("catchup drift")
						}
						raw = r.Changes
						client.version = s.Version
					}
				}
				assertChangesEqual(t, raw, want.Accepted)
				version, e := Push(ctx, tx, f, client.version, []Step{{Changes: raw, ClientID: "accepted"}}, author)
				if e != nil {
					return e
				}
				current = version
				for _, added := range want.Added {
					row, e := tx.Q.InsertCoresearchChange(ctx, db.InsertCoresearchChangeParams{WorkspaceID: tx.WS, ProposalID: p, FileID: f.Record.ID, FromPos: int32(added.From), ToPos: int32(added.To), RemovedText: added.Removed})
					if e != nil {
						return e
					}
					ids[added.ID] = row.ID
				}
				client.version = current
				client.text = want.Result
				client.draft = want.NextDraft
			} else {
				keep := want.Kind == "keep"
				if keep {
					keeps++
				} else {
					undos++
				}
				if e := Decide(ctx, tx, f, ids[want.Change], clicker, keep); e != nil {
					return e
				}
				if !keep {
					steps, e := Steps(ctx, tx, f, current, current+1)
					if e != nil {
						return e
					}
					if len(steps) != 1 {
						t.Fatal("undo must be one step")
					}
					assertChangesEqual(t, steps[0].Changes, want.Accepted)
					current++
				}
			}
			d, e := Open(ctx, tx, f)
			if e != nil {
				return e
			}
			if d.Text != want.Result || d.Version != want.Version {
				t.Fatalf("event%d got %+v want version%d text%q", i, d, want.Version, want.Result)
			}
			rows, e := tx.Q.ListCoresearchChanges(ctx, db.ListCoresearchChangesParams{WorkspaceID: tx.WS, FileID: f.Record.ID})
			if e != nil {
				return e
			}
			if len(rows) != len(want.Ranges) {
				t.Fatal("missing ranges")
			}
			for _, expected := range want.Ranges {
				for _, row := range rows {
					if row.ID == ids[expected.ID] && (int(row.FromPos) != expected.From || int(row.ToPos) != expected.To || row.Status != expected.Status) {
						t.Fatalf("event%d range %+v expected %+v", i, row, expected)
					}
				}
			}
			return nil
		})
		if e != nil {
			t.Fatalf("event%d %s: %v", i, want.Kind, e)
		}
	}
	if keeps == 0 || undos == 0 || stales < 50 {
		t.Fatalf("insufficient decisions keep%d undo%d stale%d", keeps, undos, stales)
	}
	t.Logf("real CodeMirror interleavings with %d Keeps, %d Undos, %d stale drafts", keeps, undos, stales)
}

func TestHunkOldAnchorAfterFoldAndPagination(t *testing.T) {
	st, f, who, run, p := hunkFixture(t, "anchor")
	ctx := t.Context()
	e := st.InWorkspace(ctx, f.Record.WorkspaceID, who, func(tx *store.Tx) error {
		if _, e := Open(ctx, tx, f); e != nil {
			return e
		}
		steps := make([]Step, 500)
		for i := range steps {
			raw, _ := json.Marshal(Replace(0, 0, "x", 6+i))
			steps[i] = Step{Changes: raw, ClientID: "person"}
		}
		if _, e := Push(ctx, tx, f, 0, steps, Author{UserID: &who}); e != nil {
			return e
		}
		if e := stepText(ctx, tx, f, 0, 0, "x", who); e != nil {
			return e
		}
		r, e := ApplyHunks(ctx, tx, f, 0, []Hunk{{"anchor", "kept"}}, run, p)
		if e != nil {
			return e
		}
		if len(r) != 1 || !r[0].Applied {
			t.Fatalf("old anchor %+v", r)
		}
		d, e := Open(ctx, tx, f)
		if d.Text != strings.Repeat("x", 501)+"kept" {
			t.Fatal("old mapped range wrong")
		}
		return e
	})
	if e != nil {
		t.Fatal(e)
	}
}
