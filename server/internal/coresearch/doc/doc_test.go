package doc

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
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
	st := &store.Store{Pool: env.App}
	f := File{Record: db.CoresearchFile{ID: file, WorkspaceID: ws, ProjectID: project, Path: "main.tex"}, InitialText: "abc", InitialHead: 7}
	text := "abc"
	version := int64(0)
	rng := rand.New(rand.NewSource(782))
	pending := map[uuid.UUID]string{}
	history := []string{text}
	if e := st.InWorkspace(ctx, ws, a, func(tx *store.Tx) error { _, e := Open(ctx, tx, f); return e }); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 300; i++ {
		writer := i % 3
		who := a
		if writer == 1 {
			who = b
		}
		at := rng.Intn(Length(text) + 1)
		insert := fmt.Sprintf("[%d]", i)
		cs := Replace(at, at, insert, Length(text))
		raw, _ := json.Marshal(cs)
		auth := Author{UserID: &who}
		if writer == 2 {
			auth = Author{AgentRunID: &agent}
		}
		if i > 0 {
			e := st.InWorkspace(ctx, ws, who, func(tx *store.Tx) error {
				_, e := Push(ctx, tx, f, version-1, []Step{{Changes: raw, ClientID: "lagging"}}, auth)
				return e
			})
			var behind ErrBehind
			if !errors.As(e, &behind) || behind.Current != version {
				t.Fatalf("behind %v", e)
			}
		}
		e := st.InWorkspace(ctx, ws, who, func(tx *store.Tx) error {
			v, e := Push(ctx, tx, f, version, []Step{{Changes: raw, ClientID: fmt.Sprint(writer)}}, auth)
			if e == nil && v != version+1 {
				t.Fatal(v)
			}
			return e
		})
		if e != nil {
			t.Fatalf("%d %v", i, e)
		}
		text = text[:at] + insert + text[at:]
		version++
		history = append(history, text)
		if writer == 2 {
			id := uuid.New()
			exec(`INSERT INTO coresearch_change(id,workspace_id,proposal_id,file_id,from_pos,to_pos,removed_text,status) VALUES($1,$2,$3,$4,$5,$6,'','pending')`, id, ws, proposal, file, at, at+len(insert))
			pending[id] = insert
		}
		rows, e := env.Owner.Query(ctx, `SELECT id,from_pos,to_pos,status FROM coresearch_change WHERE file_id=$1`, file)
		if e != nil {
			t.Fatal(e)
		}
		for rows.Next() {
			var id uuid.UUID
			var from, to int
			var status string
			if e = rows.Scan(&id, &from, &to, &status); e != nil {
				t.Fatal(e)
			}
			if status == "pending" && text[from:to] != pending[id] {
				t.Fatalf("%d changed pending %q != %q", i, text[from:to], pending[id])
			}
		}
		rows.Close()
	}
	if e := st.InWorkspace(ctx, ws, a, func(tx *store.Tx) error {
		d, e := Open(ctx, tx, f)
		if e != nil {
			return e
		}
		if d.Version != 300 || d.Text != text {
			t.Fatalf("final %+v", d)
		}
		for _, v := range []int64{0, 17, 199, 200, 250} {
			d, e := AtVersion(ctx, tx, f, v)
			if e != nil {
				return e
			}
			if d.Text != history[v] {
				t.Fatalf("historical %d", v)
			}
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	var checkpoint, steps int
	if e := env.Owner.QueryRow(ctx, `SELECT checkpoint_version,(SELECT count(*) FROM coresearch_doc_step WHERE file_id=$1) FROM coresearch_doc WHERE file_id=$1`, file).Scan(&checkpoint, &steps); e != nil || checkpoint != 200 || steps != 300 {
		t.Fatalf("fold %d/%d %v", checkpoint, steps, e)
	}
}
