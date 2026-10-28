package doc

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"testing"

	"github.com/google/uuid"

	"lykeion/server/internal/store"
	"lykeion/server/internal/testutil"
	db "lykeion/server/pkg/db/generated"
)

func TestStepsInterleaveRandomised(t *testing.T) {
	env := testutil.Setup(t)
	ctx := t.Context()
	ws, a, b := uuid.New(), uuid.New(), uuid.New()
	project, file, proposal, agent := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, e := env.Owner.Exec(ctx, q, args...); e != nil {
			t.Fatal(e)
		}
	}
	exec(`INSERT INTO "user"(id,email) VALUES($1,'a@test'),($2,'b@test')`, a, b)
	exec(`INSERT INTO workspace(id,slug,name) VALUES($1,'doc','doc')`, ws)
	exec(`INSERT INTO member(workspace_id,user_id,role) VALUES($1,$2,'owner'),($1,$3,'member')`, ws, a, b)
	exec(`INSERT INTO coresearch_project(id,workspace_id,name,created_by,mode) VALUES($1,$2,'paper',$3,'cowrite')`, project, ws, a)
	exec(`INSERT INTO coresearch_member(workspace_id,project_id,user_id,role) VALUES($1,$2,$3,'owner'),($1,$2,$4,'member')`, ws, project, a, b)
	exec(`INSERT INTO coresearch_file(id,workspace_id,project_id,path) VALUES($1,$2,$3,'main.tex')`, file, ws, project)
	exec(`INSERT INTO coresearch_file_version(workspace_id,file_id,n,sha256,size,blob_key,author_user_id) VALUES($1,$2,7,repeat('a',64),3,'seed',$3)`, ws, file, a)
	exec(`INSERT INTO coresearch_proposal(id,workspace_id,project_id,created_by) VALUES($1,$2,$3,$4)`, proposal, ws, project, a)
	var chat uuid.UUID
	if e := env.Owner.QueryRow(ctx, `INSERT INTO coresearch_chat(workspace_id,project_id,owner_id,mode,cli) VALUES($1,$2,$3,'cowrite','claude') RETURNING id`, ws, project, a).Scan(&chat); e != nil {
		t.Fatal(e)
	}
	exec(`INSERT INTO agent_run(id,workspace_id,kind,requester_id,responsible_user_id,board_column,round,harness,role,prompt,skill,status,coresearch_chat_id) VALUES($1,$2,'coresearch',$3,$3,'',0,'claude','coresearch','x','','queued',$4)`, agent, ws, a, chat)

	var fixture interleavingFixture
	rawFixture, err := os.ReadFile("testdata/interleavings.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(rawFixture, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.Seed != 782 || len(fixture.Trace) != 300 || len(fixture.InitialClients) != 3 {
		t.Fatal("invalid interleaving fixture")
	}
	st := &store.Store{Pool: env.App}
	f := File{Record: db.CoresearchFile{ID: file, WorkspaceID: ws, ProjectID: project, Path: "main.tex"}, InitialText: fixture.Initial, InitialHead: 7}
	type clientState struct {
		version int64
		text    string
		draft   referenceDraft
	}
	clients := make([]clientState, 3)
	for i, draft := range fixture.InitialClients {
		clients[i] = clientState{text: fixture.Initial, draft: draft}
	}
	pending := map[uuid.UUID]referenceRange{}
	ids := map[int]uuid.UUID{}
	history := []string{fixture.Initial}
	if err := st.InWorkspace(ctx, ws, a, func(tx *store.Tx) error { _, err := Open(ctx, tx, f); return err }); err != nil {
		t.Fatal(err)
	}
	staleAttempts, changedRebases := 0, 0
	writers := [3]int{}
	kinds := map[string]int{}
	for i, want := range fixture.Trace {
		writer := want.Writer
		writers[writer]++
		client := &clients[writer]
		if client.version != want.Base || client.text != want.Observed || !reflect.DeepEqual(client.draft, want.Draft) {
			t.Fatalf("step%d: client%d did not retain its independently observed draft", i, writer)
		}
		kinds[client.draft.Kind]++
		draft := Replace(client.draft.From, client.draft.To, client.draft.Insert, Length(client.text))
		raw, err := json.Marshal(draft)
		if err != nil {
			t.Fatal(err)
		}
		assertChangesEqual(t, raw, client.draft.Changes)
		who := a
		if writer == 1 {
			who = b
		}
		author := Author{UserID: &who}
		if writer == 2 {
			author = Author{AgentRunID: &agent}
		}
		push := func(base int64, changes json.RawMessage) error {
			return st.InWorkspace(ctx, ws, who, func(tx *store.Tx) error {
				version, err := Push(ctx, tx, f, base, []Step{{Changes: changes, ClientID: fmt.Sprint(writer)}}, author)
				if err != nil {
					return err
				}
				if version != int64(i+1) {
					t.Fatalf("step%d: version%d", i, version)
				}
				actual, err := Open(ctx, tx, f)
				if err != nil {
					return err
				}
				if actual.Text != want.Result {
					t.Fatalf("step%d: server text %q != CodeMirror %q", i, actual.Text, want.Result)
				}
				return nil
			})
		}
		err = push(client.version, raw)
		if client.version != int64(i) {
			staleAttempts++
			var behind ErrBehind
			if !errors.As(err, &behind) || behind.Current != int64(i) {
				t.Fatalf("step%d: stale edit accepted or wrong rejection: %v", i, err)
			}
			var remote []Step
			if err = st.InWorkspace(ctx, ws, who, func(tx *store.Tx) error {
				var err error
				remote, err = Steps(ctx, tx, f, client.version, behind.Current)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			if len(remote) != len(want.Rebases) {
				t.Fatalf("step%d: missing remote updates", i)
			}
			for j, step := range remote {
				expected := want.Rebases[j]
				if step.Version != expected.Version {
					t.Fatalf("step%d: remote version%d", i, step.Version)
				}
				assertChangesEqual(t, step.Changes, expected.Remote)
				update, err := Parse(step.Changes)
				if err != nil {
					t.Fatal(err)
				}
				client.text, err = update.Apply(client.text)
				if err != nil || client.text != expected.Observed {
					t.Fatalf("step%d catchup%d: %q %v", i, j, client.text, err)
				}
				if !sameChanges(raw, expected.Changes) {
					changedRebases++
				}
				// This is the browser's CodeMirror ChangeSet.map result for the exact
				// fetched update, generated independently of the Go implementation.
				raw = expected.Changes
				mapped, err := Parse(raw)
				if err != nil {
					t.Fatal(err)
				}
				local, err := mapped.Apply(client.text)
				if err != nil || local != expected.LocalResult {
					t.Fatalf("step%d rebase%d: %q != %q: %v", i, j, local, expected.LocalResult, err)
				}
				client.version = step.Version
			}
			assertChangesEqual(t, raw, want.Accepted)
			if err = push(client.version, raw); err != nil {
				t.Fatalf("step%d rebased push: %v", i, err)
			}
		} else if err != nil {
			t.Fatalf("step%d current push: %v", i, err)
		} else {
			assertChangesEqual(t, raw, want.Accepted)
		}
		history = append(history, want.Result)
		for _, added := range want.Added {
			id := uuid.New()
			ids[added.ID] = id
			pending[id] = added
			exec(`INSERT INTO coresearch_change(id,workspace_id,proposal_id,file_id,from_pos,to_pos,removed_text,status) VALUES($1,$2,$3,$4,$5,$6,$7,'pending')`, id, ws, proposal, file, added.From, added.To, added.Removed)
		}
		expectedRanges := map[uuid.UUID]referenceRange{}
		for _, rangeWant := range want.Ranges {
			expectedRanges[ids[rangeWant.ID]] = rangeWant
		}
		rows, err := env.Owner.Query(ctx, `SELECT id,from_pos,to_pos,status FROM coresearch_change WHERE file_id=$1`, file)
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		for rows.Next() {
			var id uuid.UUID
			var from, to int
			var status string
			if err = rows.Scan(&id, &from, &to, &status); err != nil {
				t.Fatal(err)
			}
			count++
			expected, ok := expectedRanges[id]
			if !ok || from != expected.From || to != expected.To || status != expected.Status {
				t.Fatalf("step%d range%s: [%d,%d) %s != %+v", i, id, from, to, status, expected)
			}
			slice, err := Slice(want.Result, from, to)
			if err != nil {
				t.Fatal(err)
			}
			if status == "pending" && slice != pending[id].Inserted {
				t.Fatalf("step%d pending slice %q != inserted %q", i, slice, pending[id].Inserted)
			}
		}
		if err = rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		if count != len(expectedRanges) {
			t.Fatalf("step%d: missing change rows", i)
		}
		client.version = int64(i + 1)
		client.text = want.Result
		client.draft = want.NextDraft
	}
	if staleAttempts != fixture.Stats.StaleAttempts || changedRebases != fixture.Stats.ChangedRebases || staleAttempts < 200 || changedRebases == 0 || fixture.Stats.OverlappingRebases == 0 {
		t.Fatalf("insufficient real rebasing: stale%d changed%d stats%+v", staleAttempts, changedRebases, fixture.Stats)
	}
	for _, count := range writers {
		if count != 100 {
			t.Fatalf("unbalanced writers: %v", writers)
		}
	}
	for _, kind := range []string{"insert", "delete", "replace"} {
		if kinds[kind] == 0 {
			t.Fatalf("missing %s interleavings", kind)
		}
	}
	if err := st.InWorkspace(ctx, ws, a, func(tx *store.Tx) error {
		for _, v := range []int64{0, 17, 199, 200, 250, 300} {
			d, err := AtVersion(ctx, tx, f, v)
			if err != nil {
				return err
			}
			if d.Text != history[v] {
				t.Fatalf("historical%d: %q != %q", v, d.Text, history[v])
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var checkpoint, steps int
	if err := env.Owner.QueryRow(ctx, `SELECT checkpoint_version,(SELECT count(*) FROM coresearch_doc_step WHERE file_id=$1) FROM coresearch_doc WHERE file_id=$1`, file).Scan(&checkpoint, &steps); err != nil || checkpoint != 200 || steps != 300 {
		t.Fatalf("fold %d/%d %v", checkpoint, steps, err)
	}
	t.Logf("CodeMirror seed%d: 300 accepted, %d stale drafts, %d changed rebase operations, %d overlapping rebases, kinds%v", fixture.Seed, staleAttempts, changedRebases, fixture.Stats.OverlappingRebases, kinds)
}

type referenceDraft struct {
	Kind     string
	From, To int
	Insert   string
	Changes  json.RawMessage
}
type referenceRange struct {
	ID, From, To              int
	Inserted, Removed, Status string
}
type referenceRebase struct {
	Version               int64
	Remote, Changes       json.RawMessage
	Observed, LocalResult string
}
type referenceTrace struct {
	Writer        int
	Base          int64
	Observed      string
	Draft         referenceDraft
	Rebases       []referenceRebase
	Accepted      json.RawMessage
	Result        string
	Added, Ranges []referenceRange
	NextDraft     referenceDraft
}
type interleavingFixture struct {
	Seed           int
	Initial        string
	InitialClients []referenceDraft
	Stats          struct{ StaleAttempts, ChangedRebases, OverlappingRebases int }
	Trace          []referenceTrace
}

func sameChanges(a, b json.RawMessage) bool {
	var x, y any
	return json.Unmarshal(a, &x) == nil && json.Unmarshal(b, &y) == nil && reflect.DeepEqual(x, y)
}
func assertChangesEqual(t *testing.T, a, b json.RawMessage) {
	t.Helper()
	if !sameChanges(a, b) {
		t.Fatalf("changes %s != CodeMirror %s", a, b)
	}
}
