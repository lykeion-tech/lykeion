package labmemory

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"lykeion/server/internal/store"
	"lykeion/server/internal/testutil"
)

type fixture struct {
	t                                          *testing.T
	env                                        *testutil.Env
	ws, user, study, record, comment, decision uuid.UUID
}

func setup(t *testing.T) *fixture {
	f := &fixture{t: t, env: testutil.Setup(t), ws: uuid.New(), user: uuid.New(), study: uuid.New(), record: uuid.New(), comment: uuid.New(), decision: uuid.New()}
	f.exec(`INSERT INTO "user" (id,email,name) VALUES ($1,'facts@example.org','Researcher')`, f.user)
	f.exec(`INSERT INTO workspace(id,slug,name) VALUES($1,'facts','Facts')`, f.ws)
	f.exec(`INSERT INTO member(workspace_id,user_id,role) VALUES($1,$2,'owner')`, f.ws, f.user)
	f.exec(`INSERT INTO card(id,workspace_id,number,title,question,owner_id,board_column) VALUES($1,$2,1,'Study','Question',$3,'grounding')`, f.study, f.ws, f.user)
	f.exec(`INSERT INTO record(id,workspace_id,card_id,board_column,round,kind,role,body) VALUES($1,$2,$3,'hypotheses',1,'hypothesis','hypotheses','{"status":"refuted","text":"H1"}')`, f.record, f.ws, f.study)
	f.exec(`INSERT INTO comment(id,workspace_id,card_id,author_id,body) VALUES($1,$2,$3,$4,'Discussion')`, f.comment, f.ws, f.study, f.user)
	f.exec(`INSERT INTO inbox_item(id,workspace_id,card_id,user_id,kind,text,options) VALUES($1,$2,$3,$4,'agent_decision','Which method?','[{"id":"a"}]')`, f.decision, f.ws, f.study, f.user)
	return f
}
func (f *fixture) exec(q string, args ...any) {
	f.t.Helper()
	if _, err := f.env.Owner.Exec(context.Background(), q, args...); err != nil {
		f.t.Fatal(err)
	}
}
func (f *fixture) read(r Ref) (Fact, error) {
	var fact Fact
	err := (&store.Store{Pool: f.env.App}).InWorkspace(context.Background(), f.ws, f.user, func(tx *store.Tx) error {
		var err error
		fact, err = Read(context.Background(), tx, f.user, r)
		return err
	})
	return fact, err
}
func TestReadStudyLinksItsRecordsCommentsAndDecisions(t *testing.T) {
	f := setup(t)
	fact, err := f.read(Ref{"study", f.study})
	if err != nil {
		t.Fatal(err)
	}
	want := map[Ref]bool{{"record", f.record}: true, {"comment", f.comment}: true, {"decision", f.decision}: true}
	for _, link := range fact.Links {
		delete(want, link)
	}
	if len(want) > 0 {
		t.Fatalf("missing %v", want)
	}
}
func TestRecordVersionChangesWhenSuperseded(t *testing.T) {
	f := setup(t)
	r := Ref{"record", f.record}
	before, err := f.read(r)
	if err != nil {
		t.Fatal(err)
	}
	f.exec(`UPDATE record SET superseded_at=now() WHERE id=$1`, f.record)
	after, err := f.read(r)
	if err != nil || before.Version != "live" || after.Version != "superseded" || !after.Superseded {
		t.Fatalf("%+v %+v %v", before, after, err)
	}
}
func TestCommentVersionChangesWhenCancelled(t *testing.T) {
	f := setup(t)
	r := Ref{"comment", f.comment}
	before, err := f.read(r)
	if err != nil {
		t.Fatal(err)
	}
	f.exec(`INSERT INTO comment_cancellation(workspace_id,comment_id,author_id) VALUES($1,$2,$3)`, f.ws, f.comment, f.user)
	after, err := f.read(r)
	if err != nil || before.Version != "live" || after.Version != "cancelled" {
		t.Fatalf("%+v %+v %v", before, after, err)
	}
}
func TestNonMemberCannotRead(t *testing.T) {
	f := setup(t)
	f.exec(`DELETE FROM member WHERE workspace_id=$1 AND user_id=$2`, f.ws, f.user)
	for _, r := range []Ref{{"study", f.study}, {"record", f.record}, {"comment", f.comment}, {"decision", f.decision}, {Kind: "workspace"}} {
		if _, err := f.read(r); !errors.Is(err, ErrNotFound) {
			t.Fatalf("%v: %v", r, err)
		}
	}
}
func TestLessonOpensOnlyWhenEveryCiteOpens(t *testing.T) {
	f := setup(t)
	id := uuid.New()
	study, err := f.read(Ref{"study", f.study})
	if err != nil {
		t.Fatal(err)
	}
	cites, _ := json.Marshal([]Citation{{Ref: Ref{"record", f.record}.String(), Version: "live"}, {Ref: Ref{"study", f.study}.String(), Version: study.Version}})
	f.exec(`INSERT INTO lesson(id,workspace_id,kind,text,cites,model) VALUES($1,$2,'lab','Lesson',$3,'test')`, id, f.ws, cites)
	r := Ref{"lesson", id}
	fact, err := f.read(r)
	if err != nil || len(fact.Links) != 2 {
		t.Fatalf("%+v %v", fact, err)
	}
	f.exec(`UPDATE card SET version=version+1 WHERE id=$1`, f.study)
	if _, err := f.read(r); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stale lesson: %v", err)
	}
	f.exec(`UPDATE card SET version=1 WHERE id=$1`, f.study)
	f.exec(`UPDATE lesson SET status='set_aside' WHERE id=$1`, id)
	if _, err := f.read(r); !errors.Is(err, ErrNotFound) {
		t.Fatalf("set aside: %v", err)
	}
	f.exec(`UPDATE lesson SET status='active',cites=$2 WHERE id=$1`, id, `[{"ref":"record:`+uuid.NewString()+`","version":"live"}]`)
	if _, err := f.read(r); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing cite: %v", err)
	}
}
func TestWorkspaceContextAndScope(t *testing.T) {
	f := setup(t)
	a, err := f.read(Ref{Kind: "workspace"})
	if err != nil {
		t.Fatal(err)
	}
	f.exec(`UPDATE workspace SET research_context='Updated context' WHERE id=$1`, f.ws)
	b, err := f.read(Ref{Kind: "workspace"})
	if err != nil || a.Version == b.Version {
		t.Fatalf("%+v %+v %v", a, b, err)
	}
	if _, err := f.read(Ref{"workspace", uuid.New()}); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestFactsCannotCrossWorkspace(t *testing.T) {
	f := setup(t)
	otherWS, otherStudy := uuid.New(), uuid.New()
	f.exec(`INSERT INTO workspace(id,slug,name) VALUES($1,'other','Other')`, otherWS)
	f.exec(`INSERT INTO card(id,workspace_id,number,title,question,owner_id,board_column) VALUES($1,$2,1,'Private','Secret',$3,'grounding')`, otherStudy, otherWS, f.user)
	for _, r := range []Ref{{"study", otherStudy}, {"workspace", otherWS}, {"decision", f.record}} {
		if _, err := f.read(r); !errors.Is(err, ErrNotFound) {
			t.Fatalf("%v: %v", r, err)
		}
	}
	// A workspace-local lesson cannot smuggle another workspace's fact.
	id := uuid.New()
	cites, _ := json.Marshal([]Citation{{Ref: Ref{"study", otherStudy}.String(), Version: "1"}})
	f.exec(`INSERT INTO lesson(id,workspace_id,kind,text,cites,model) VALUES($1,$2,'lab','Leak',$3,'test')`, id, f.ws, cites)
	if _, err := f.read(Ref{"lesson", id}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign citation: %v", err)
	}
}
func TestLessonRejectsMalformedEmptyAndRecursiveCites(t *testing.T) {
	f := setup(t)
	id := uuid.New()
	f.exec(`INSERT INTO lesson(id,workspace_id,kind,text,cites,model) VALUES($1,$2,'lab','Bad lesson','[]','test')`, id, f.ws)
	for _, cites := range []string{`[]`, `{}`, `["record:` + f.record.String() + `"]`, `[{"ref":"record:` + f.record.String() + `"}]`, `[{"ref":"lesson:` + id.String() + `","version":"1"}]`} {
		f.exec(`UPDATE lesson SET cites=$2 WHERE id=$1`, id, cites)
		if _, err := f.read(Ref{"lesson", id}); !errors.Is(err, ErrNotFound) {
			t.Fatalf("%s: %v", cites, err)
		}
	}
}
func TestStudyAndRecordFactProjections(t *testing.T) {
	f := setup(t)
	f.exec(`UPDATE card SET workflow='reproducible',workflow_release='v1',end_reason='Failed validation',ending='stopped' WHERE id=$1`, f.study)
	f.exec(`INSERT INTO attachment(workspace_id,uploader_id,card_id,name,content_type,size_bytes,sha256,blob_key) VALUES($1,$2,$3,'protocol.txt','text/plain',7,'hash','private/blob')`, f.ws, f.user, f.study)
	study, err := f.read(Ref{"study", f.study})
	if err != nil {
		t.Fatal(err)
	}
	var projection struct {
		Study struct {
			EndReason string `json:"end_reason"`
			Workflow  string `json:"workflow"`
		}
		Attachments []struct{ Name string }
	}
	if err := json.Unmarshal([]byte(study.Text), &projection); err != nil {
		t.Fatal(err)
	}
	if projection.Study.EndReason != "Failed validation" || projection.Study.Workflow != "reproducible" || len(projection.Attachments) != 1 || projection.Attachments[0].Name != "protocol.txt" {
		t.Fatalf("%s", study.Text)
	}
	record, err := f.read(Ref{"record", f.record})
	if err != nil {
		t.Fatal(err)
	}
	var projected struct{ Body struct{ Status, Text string } }
	if err := json.Unmarshal([]byte(record.Text), &projected); err != nil {
		t.Fatal(err)
	}
	if projected.Body.Status != "refuted" || projected.Body.Text != "H1" {
		t.Fatal(record.Text)
	}
}
func TestCanOpenAndInternalVersion(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	s := &store.Store{Pool: f.env.App}
	err := s.InWorkspace(ctx, f.ws, f.user, func(tx *store.Tx) error {
		yes, err := CanOpen(ctx, tx, f.user, Ref{"record", f.record})
		if err != nil || !yes {
			t.Fatalf("member: %v %v", yes, err)
		}
		no, err := CanOpen(ctx, tx, uuid.New(), Ref{"record", f.record})
		if err != nil || no {
			t.Fatalf("nonmember: %v %v", no, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	err = s.Maintenance(ctx, func(tx *store.Tx) error {
		if _, err := Version(ctx, tx, Ref{"record", f.record}); !errors.Is(err, ErrNotFound) {
			t.Fatalf("unscoped version: %v", err)
		}
		if err := tx.Workspace(ctx, f.ws); err != nil {
			return err
		}
		v, err := Version(ctx, tx, Ref{"record", f.record})
		if err != nil || v != "live" {
			t.Fatalf("scoped version: %s %v", v, err)
		}
		if _, err := Read(ctx, tx, uuid.Nil, Ref{"record", f.record}); !errors.Is(err, ErrNotFound) {
			t.Fatalf("maintenance cannot public-read: %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) member() uuid.UUID {
	id := uuid.New()
	f.exec(`INSERT INTO "user"(id,email,name) VALUES($1,$2,'Other')`, id, id.String()+"@example.org")
	f.exec(`INSERT INTO member(workspace_id,user_id,role) VALUES($1,$2,'member')`, f.ws, id)
	return id
}
func (f *fixture) readAs(user uuid.UUID, r Ref) (Fact, error) {
	var fact Fact
	err := (&store.Store{Pool: f.env.App}).InWorkspace(context.Background(), f.ws, user, func(tx *store.Tx) error {
		var err error
		fact, err = Read(context.Background(), tx, user, r)
		return err
	})
	return fact, err
}
func (f *fixture) team() uuid.UUID {
	id := uuid.New()
	f.exec(`INSERT INTO field_pack(id,workspace_id,name,version,sha256,manifest,blob_key,source,uploaded_by) VALUES($1,$2,'Private Team','1','hash','{"secret":"private-method"}','blob','uploaded',$3)`, id, f.ws, f.user)
	f.exec(`INSERT INTO card_team(workspace_id,card_id,board_column,pack_id) VALUES($1,$2,'grounding',$3)`, f.ws, f.study, id)
	return id
}
func (f *fixture) lessonFor(ref Ref) Ref {
	fact, err := f.read(ref)
	if err != nil {
		f.t.Fatal(err)
	}
	cites, _ := json.Marshal([]Citation{{Ref: ref.String(), Version: fact.Version}})
	id := uuid.New()
	f.exec(`INSERT INTO lesson(id,workspace_id,kind,text,cites,model) VALUES($1,$2,'lab','Lesson from private-method',$3,'test')`, id, f.ws, cites)
	return Ref{"lesson", id}
}
func TestPublicReadsBindTransactionIdentity(t *testing.T) {
	f := setup(t)
	other := f.member()
	ctx := context.Background()
	s := &store.Store{Pool: f.env.App}
	r := Ref{"study", f.study}
	check := func(tx *store.Tx) error {
		if _, err := Read(ctx, tx, f.user, r); !errors.Is(err, ErrNotFound) {
			t.Fatalf("borrowed identity read: %v", err)
		}
		yes, err := CanOpen(ctx, tx, f.user, r)
		if err != nil || yes {
			t.Fatalf("borrowed identity open: %v %v", yes, err)
		}
		return nil
	}
	for _, identity := range []uuid.UUID{other, uuid.New(), uuid.Nil} {
		if err := s.InWorkspace(ctx, f.ws, identity, check); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Maintenance(ctx, func(tx *store.Tx) error {
		if err := tx.Workspace(ctx, f.ws); err != nil {
			return err
		}
		return check(tx)
	}); err != nil {
		t.Fatal(err)
	}
}
func TestTeamVisibilityAndLessonsFollowAsker(t *testing.T) {
	f := setup(t)
	other := f.member()
	team := f.team()
	for _, r := range []Ref{{"study", f.study}, {Kind: "workspace"}} {
		own, err := f.read(r)
		if err != nil {
			t.Fatal(err)
		}
		peer, err := f.readAs(other, r)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(own.Text, "private-method") || strings.Contains(peer.Text, "private-method") {
			t.Fatalf("visibility: owner=%s peer=%s", own.Text, peer.Text)
		}
		lesson := f.lessonFor(r)
		if _, err := f.readAs(other, lesson); !errors.Is(err, ErrNotFound) {
			t.Fatalf("private evidence lesson exposed: %v", err)
		}
		f.exec(`UPDATE field_pack SET visibility='workspace' WHERE id=$1`, team)
		shared, err := f.readAs(other, r)
		if err != nil || !strings.Contains(shared.Text, "private-method") {
			t.Fatalf("shared: %s %v", shared.Text, err)
		}
		sharedLesson := f.lessonFor(r)
		if _, err := f.readAs(other, sharedLesson); err != nil {
			t.Fatalf("shared lesson: %v", err)
		}
		f.exec(`UPDATE field_pack SET visibility='private' WHERE id=$1`, team)
		if _, err := f.readAs(other, sharedLesson); !errors.Is(err, ErrNotFound) {
			t.Fatalf("unshared lesson: %v", err)
		}
		f.exec(`UPDATE field_pack SET deleted_at=now() WHERE id=$1`, team)
		for _, user := range []uuid.UUID{f.user, other} {
			fact, err := f.readAs(user, r)
			if err != nil || strings.Contains(fact.Text, "private-method") {
				t.Fatalf("deleted team: %s %v", fact.Text, err)
			}
		}
		if _, err := f.read(lesson); !errors.Is(err, ErrNotFound) {
			t.Fatalf("deleted lesson evidence: %v", err)
		}
		f.exec(`UPDATE field_pack SET deleted_at=NULL WHERE id=$1`, team)
	}
}
func TestProjectionVersionsTrackSubstantiveChanges(t *testing.T) {
	f := setup(t)
	team := f.team()
	check := func(r Ref, mutation string, args ...any) {
		t.Helper()
		before, err := f.read(r)
		if err != nil {
			t.Fatal(err)
		}
		lesson := f.lessonFor(r)
		f.exec(mutation, args...)
		after, err := f.read(r)
		if err != nil {
			t.Fatal(err)
		}
		if before.Version == after.Version {
			t.Fatalf("version unchanged for %v", r)
		}
		if _, err := f.read(lesson); !errors.Is(err, ErrNotFound) {
			t.Fatalf("lesson survived changed %v: %v", r, err)
		}
	}
	check(Ref{"decision", f.decision}, `INSERT INTO inbox_item(id,workspace_id,card_id,user_id,kind,text,options) VALUES($1,$2,$3,$4,'agent_decision','New open question','[{"id":"b"}]') ON CONFLICT(id) DO UPDATE SET text=excluded.text,options=excluded.options`, f.decision, f.ws, f.study, f.user)
	check(Ref{Kind: "workspace"}, `UPDATE field_pack SET manifest='{"method":"updated"}' WHERE id=$1`, team)
	check(Ref{Kind: "workspace"}, `UPDATE card SET workflow='new-workflow' WHERE id=$1`, f.study)
	check(Ref{"study", f.study}, `INSERT INTO attachment(workspace_id,uploader_id,card_id,name,content_type,size_bytes,sha256,blob_key) VALUES($1,$2,$3,'new.txt','text/plain',1,'hash','blob')`, f.ws, f.user, f.study)
	check(Ref{"study", f.study}, `UPDATE field_pack SET manifest='{"method":"revised"}' WHERE id=$1`, team)
	check(Ref{"study", f.study}, `UPDATE card SET handoff_note='New hand-in' WHERE id=$1`, f.study)
	// Repeated reads must stay stable, rather than hashing non-deterministic arrays.
	a, _ := f.read(Ref{Kind: "workspace"})
	b, _ := f.read(Ref{Kind: "workspace"})
	if a.Version != b.Version {
		t.Fatal("unstable hash")
	}
}
func TestStudyProjectionExcludesInternalColumns(t *testing.T) {
	f := setup(t)
	f.exec(`UPDATE card SET grounding_criteria='[{"private":"legacy-secret"}]',legacy_column='grounding' WHERE id=$1`, f.study)
	fact, err := f.read(Ref{"study", f.study})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"grounding_criteria", "legacy_column", "legacy-secret"} {
		if strings.Contains(fact.Text, key) {
			t.Fatalf("internal field %s in %s", key, fact.Text)
		}
	}
}
