package labmemory

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

// look is a real lab read: the log keeps what the fact showed.
func (x *turn) look(ref Ref, via string) {
	x.t.Helper()
	f, err := x.read(ref)
	if err != nil {
		x.t.Fatal(err)
	}
	shown := titleOnly(ref)
	if via == "read" {
		shown = exposure(f)
	}
	x.exec("INSERT INTO lab_read(workspace_id,agent_run_id,ref,via,read_version,shown,attempt) VALUES($1,$2,$3,$4,$5,$6,$7)", x.ws, x.run.ID, ref.String(), via, pin(f).Version, shown, x.ref.Attempt)
}

func (x *turn) display() (shown, changed bool) {
	x.t.Helper()
	err := x.runs.Store.InWorkspaceSnapshot(x.t.Context(), x.ws, x.user, func(tx *store.Tx) error {
		rows, err := tx.Q.ListPrivateLabMessages(x.t.Context(), db.ListPrivateLabMessagesParams{ChatID: *x.run.LabChatID, PageSize: 1})
		if err != nil {
			return err
		}
		reader := NewDisplayReader(tx, x.user, *x.run.LabChatID)
		if _, _, shown, err = reader.Answer(x.t.Context(), rows[0]); err != nil {
			return err
		}
		changed = reader.Changed(rows[0].ID)
		return nil
	})
	if err != nil {
		x.t.Fatal(err)
	}
	return
}

func TestAnswersOutliveChangesButNotWithdrawals(t *testing.T) {
	const (
		current = "shown"
		changed = "changed"
		hidden  = "hidden"
	)
	for name, c := range map[string]struct {
		after func(x *turn)
		want  string
	}{
		"nothing happened": {func(x *turn) {}, current},
		"study moved":      {func(x *turn) { x.exec("UPDATE card SET board_column='hypotheses' WHERE id=$1", x.study) }, changed},
		"comment added": {func(x *turn) {
			x.exec("INSERT INTO comment(workspace_id,card_id,author_id,body) VALUES($1,$2,$3,'more')", x.ws, x.study, x.user)
		}, changed},
		"question rewritten":    {func(x *turn) { x.exec("UPDATE card SET question='Redacted' WHERE id=$1", x.study) }, hidden},
		"handoff note added":    {func(x *turn) { x.exec("UPDATE card SET handoff_note='Next: replicate' WHERE id=$1", x.study) }, changed},
		"run finished":          {func(x *turn) { x.exec("UPDATE card SET state='blocked',round=round+1 WHERE id=$1", x.study) }, changed},
		"study deleted":         {func(x *turn) { x.exec("DELETE FROM card WHERE id=$1", x.study) }, hidden},
		"team made private":     {func(x *turn) { x.exec("UPDATE field_pack SET visibility='private' WHERE workspace_id=$1", x.ws) }, hidden},
		"team removed from use": {func(x *turn) { x.exec("DELETE FROM card_team WHERE card_id=$1", x.study) }, changed},
		"attachment removed":    {func(x *turn) { x.exec("DELETE FROM attachment WHERE card_id=$1", x.study) }, hidden},
	} {
		t.Run(name, func(t *testing.T) {
			x := runningTurn(t)
			x.exec("UPDATE field_pack SET visibility='workspace' WHERE id=$1", x.team())
			x.exec("INSERT INTO attachment(id,workspace_id,card_id,kind,name,content_type,size_bytes,sha256,blob_key,uploader_id) VALUES($1,$2,$3,'file','data.csv','text/csv',1,'x',$4,$5)", uuid.New(), x.ws, x.study, x.ws.String()+"/attachments/"+uuid.NewString(), x.user)
			ref := Ref{Kind: "study", ID: x.study}
			x.look(ref, "read")
			if probs, err := x.submit(t.Context(), "fact", ref.String()); err != nil || len(probs) > 0 {
				t.Fatal(err, probs)
			}
			c.after(x)
			shown, isChanged := x.display()
			got := map[bool]map[bool]string{true: {false: current, true: changed}, false: {false: hidden, true: hidden}}[shown][isChanged]
			if got != c.want {
				t.Fatalf("%s, want %s", got, c.want)
			}
		})
	}
}

func TestListingAStudyDoesNotMarkTheAnswerChanged(t *testing.T) {
	x := runningTurn(t)
	other := uuid.New()
	x.exec(`INSERT INTO card(id,workspace_id,number,title,question,owner_id,board_column) VALUES($1,$2,2,'Other','Q',$3,'grounding')`, other, x.ws, x.user)
	record := Ref{Kind: "record", ID: x.record}
	x.look(Ref{Kind: "study", ID: other}, "workspace:context")
	x.look(Ref{Kind: "workspace"}, "links")
	x.look(record, "read")
	if probs, err := x.submit(t.Context(), "fact", record.String()); err != nil || len(probs) > 0 {
		t.Fatal(err, probs)
	}
	x.exec("UPDATE card SET board_column='hypotheses' WHERE id=$1", other)
	if shown, changed := x.display(); !shown || changed {
		t.Fatal(shown, changed)
	}
	x.exec("DELETE FROM card WHERE id=$1", other)
	if shown, _ := x.display(); shown {
		t.Fatal("an answer kept the title of a deleted study")
	}
}

func TestRewrittenEvidenceHidesTheAnswer(t *testing.T) {
	for name, q := range map[string]string{
		"record":  "UPDATE record SET body='{\"status\":\"supported\",\"text\":\"H9\"}' WHERE id=$1",
		"comment": "UPDATE comment SET body='Something else' WHERE id=$1",
	} {
		t.Run(name, func(t *testing.T) {
			x := runningTurn(t)
			ref := Ref{Kind: name, ID: map[string]uuid.UUID{"record": x.record, "comment": x.comment}[name]}
			x.look(ref, "read")
			if probs, err := x.submit(t.Context(), "fact", ref.String()); err != nil || len(probs) > 0 {
				t.Fatal(err, probs)
			}
			x.exec(q, ref.ID)
			if shown, _ := x.display(); shown {
				t.Fatal("kept after its source was rewritten")
			}
		})
	}
	x := runningTurn(t)
	ref := Ref{Kind: "record", ID: x.record}
	x.look(ref, "read")
	if probs, err := x.submit(t.Context(), "fact", ref.String()); err != nil || len(probs) > 0 {
		t.Fatal(err, probs)
	}
	x.exec("UPDATE record SET superseded_at=now() WHERE id=$1", x.record)
	if shown, changed := x.display(); !shown || !changed {
		t.Fatal("a superseded record", shown, changed)
	}
}

func TestAStudyMayMoveWhileItIsAnswered(t *testing.T) {
	x := runningTurn(t)
	ref := Ref{Kind: "study", ID: x.study}
	x.look(ref, "read")
	x.exec("UPDATE card SET board_column='hypotheses' WHERE id=$1", x.study)
	if probs, err := x.submit(t.Context(), "fact", ref.String()); err != nil || len(probs) > 0 {
		t.Fatal(err, probs)
	}
	if shown, changed := x.display(); !shown || !changed {
		t.Fatal(shown, changed)
	}
	y := runningTurn(t)
	y.look(ref2(y), "read")
	y.exec("DELETE FROM card WHERE id=$1", y.study)
	if probs, err := y.submit(t.Context(), "gap"); err != nil || len(probs) != 1 || probs[0] != FreshTurnProblem {
		t.Fatal("published after its study was deleted", err, probs)
	}
}

func ref2(x *turn) Ref { return Ref{Kind: "study", ID: x.study} }

func TestAnswersWithoutARecordOfWhatTheyShowedStayStrict(t *testing.T) {
	x := runningTurn(t)
	ref := Ref{Kind: "study", ID: x.study}.String()
	x.log(ref, "read")
	if probs, err := x.submit(t.Context(), "fact", ref); err != nil || len(probs) > 0 {
		t.Fatal(err, probs)
	}
	x.exec("UPDATE card SET board_column='hypotheses' WHERE id=$1", x.study)
	if shown, _ := x.display(); shown {
		t.Fatal("relaxed an answer that cannot say what it showed")
	}
}

func TestSavedProposalsAreLabEvidenceUntilDiscarded(t *testing.T) {
	x := runningTurn(t)
	id := uuid.New()
	x.exec(`INSERT INTO study_proposal(workspace_id,id,owner_id,description,stage,assignee,revision) VALUES($1,$2,$3,'Does a nap help recall?','experimentation','{"type":"agent","agent_id":"00000000-0000-0000-0000-000000000001"}',1)`, x.ws, id, x.user)
	ref := Ref{Kind: "proposal", ID: id}
	colleague := x.member()
	for _, who := range []uuid.UUID{x.user, colleague} {
		f, err := x.readAs(who, ref)
		if err != nil || !strings.Contains(f.Text, "Does a nap help recall?") || !strings.Contains(f.Text, "not started") || strings.Contains(f.Text, "agent_id") {
			t.Fatal(err, f)
		}
	}
	x.look(ref, "read")
	if probs, err := x.submit(t.Context(), "fact", ref.String()); err != nil || len(probs) > 0 {
		t.Fatal(err, probs)
	}
	x.exec("UPDATE study_proposal SET stage='hypotheses',revision=2 WHERE id=$1", id)
	if shown, changed := x.display(); !shown || !changed {
		t.Fatal("moved stage", shown, changed)
	}
	x.exec("UPDATE study_proposal SET description='Redacted',revision=3 WHERE id=$1", id)
	if shown, _ := x.display(); shown {
		t.Fatal("kept a rewritten proposal")
	}
	x.exec("UPDATE study_proposal SET description='Does a nap help recall?',revision=4 WHERE id=$1", id)
	x.exec("UPDATE study_proposal SET operation_id=$2,launch_revision=4,card_id=$3,card_number=1 WHERE id=$1", id, uuid.New(), x.study)
	if shown, changed := x.display(); !shown || !changed {
		t.Fatal("started", shown, changed)
	}
	if f, err := x.read(ref); err != nil || !strings.Contains(f.Text, `"started"`) || len(f.Links) != 1 || f.Links[0].ID != x.study {
		t.Fatal(err, f)
	}
	x.exec("UPDATE study_proposal SET operation_id=NULL,launch_revision=NULL,card_id=NULL,card_number=NULL,description=NULL,stage=NULL,assignee=NULL,discarded_at=now() WHERE id=$1", id)
	if shown, _ := x.display(); shown {
		t.Fatal("kept a discarded proposal")
	}
	if _, err := x.read(ref); err != ErrNotFound {
		t.Fatal(err)
	}
}

func TestLabToolsRecordWhatTheyShowed(t *testing.T) {
	f := setup(t)
	tf := newTools(t, f, nil)
	team := f.team()
	f.exec("UPDATE field_pack SET visibility='workspace' WHERE id=$1", team)
	saved, started := uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{saved, started} {
		f.exec(`INSERT INTO study_proposal(workspace_id,id,owner_id,description,stage,assignee,revision) VALUES($1,$2,$3,'Does a nap help recall?','experimentation','{"type":"agent"}',1)`, f.ws, id, f.user)
	}
	f.exec("UPDATE study_proposal SET operation_id=$2,launch_revision=1,card_id=$3,card_number=1 WHERE id=$1", started, uuid.New(), f.study)
	ref := Ref{Kind: "study", ID: f.study}.String()
	tf.call("read", map[string]any{"ref": ref}, false)
	page := tf.call("links", map[string]any{"ref": "workspace:context", "directory": "proposals"}, false)
	if !strings.Contains(page, saved.String()) || strings.Contains(page, started.String()) {
		t.Fatal(page)
	}
	shown := func(where, r string) (tokens []string) {
		t.Helper()
		if err := f.env.Owner.QueryRow(context.Background(), "SELECT shown FROM lab_read WHERE agent_run_id=$1 AND ref=$2 AND via"+where, tf.run, r).Scan(&tokens); err != nil {
			t.Fatal(err)
		}
		return
	}
	if got := shown("='read'", ref); !slices.Contains(got, "team:"+team.String()) || !slices.Contains(got, shownAlways) {
		t.Fatal(got)
	}
	if got := shown("<>'read'", Ref{Kind: "proposal", ID: saved}.String()); !slices.Equal(got, []string{shownAlways}) {
		t.Fatal(got)
	}
	lesson := f.lessonFor(Ref{Kind: "record", ID: f.record})
	tf.call("read", map[string]any{"ref": lesson.String()}, false)
	if got := shown("='read'", lesson.String()); got != nil {
		t.Fatal("a lesson no longer needs its exact version", got)
	}
}
