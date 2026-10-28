package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"lykeion/server/internal/coresearch"
	"lykeion/server/internal/coresearch/doc"
	"lykeion/server/internal/storage"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

func paperFixture(t *testing.T) (*server, string, string, string, csProject, string) {
	t.Helper()
	s, a, b, _, ws, p := csFixture(t)
	if _, e := s.Env.Owner.Exec(t.Context(), `UPDATE coresearch_project SET mode='cowrite' WHERE id=$1`, p.ID); e != nil {
		t.Fatal(e)
	}
	if c := csAdd(t, s, a, b, ws, p.ID, "member"); c != 201 {
		t.Fatal(c)
	}
	c, v := csUpload(t, s, a, ws, p.ID, "main.tex", 0, false, []byte("a😀\r\nb"))
	if c != 201 {
		t.Fatal(c)
	}
	return s, a, b, ws, p, v.FileID.String()
}
func TestBehindStepRefused(t *testing.T) {
	s, a, _, ws, p, f := paperFixture(t)
	url := csProjects + "/" + p.ID + "/docs/" + f
	var d doc.Doc
	if c := s.call(t, "GET", url, a, ws, nil, &d); c != 200 || d.Text != "a😀\r\nb" {
		t.Fatalf("open %d %+v", c, d)
	}
	steps := []any{}
	for i := 0; i < 5; i++ {
		steps = append(steps, []any{6, []any{0, "x"}, i})
	}
	if c := s.call(t, "POST", url+"/steps", a, ws, map[string]any{"base": 0, "client_id": "a", "steps": steps}, nil); c != 200 {
		t.Fatal(c)
	}
	var behind struct{ Current int }
	if c := s.call(t, "POST", url+"/steps", a, ws, map[string]any{"base": 3, "client_id": "a", "steps": []any{[]any{11}}}, &behind); c != 409 || behind.Current != 5 {
		t.Fatalf("behind %d %+v", c, behind)
	}
	var rows []struct {
		Version  int
		Changes  json.RawMessage
		ClientID string `json:"client_id"`
	}
	if c := s.call(t, "GET", url+"/steps?after=0", a, ws, nil, &rows); c != 200 || len(rows) != 5 || rows[4].ClientID != "a" {
		t.Fatalf("steps %d %+v", c, rows)
	}
	if c := s.call(t, "GET", url, a, ws, nil, &d); c != 200 || d.Version != 5 || d.Text != "a😀\r\nbxxxxx" {
		t.Fatalf("state %d %+v", c, d)
	}
	if c := s.call(t, "POST", url+"/steps", a, ws, map[string]any{"base": 5, "client_id": "a", "steps": []any{[]any{11, []any{0, "y"}}, []any{1}}}, nil); c != 400 {
		t.Fatalf("invalid batch %d", c)
	}
	s.call(t, "GET", url, a, ws, nil, &d)
	if d.Version != 5 {
		t.Fatal("partial batch committed")
	}
}
func TestRemovedMemberLosesPaper(t *testing.T) {
	s, a, b, ws, p, f := paperFixture(t)
	url := csProjects + "/" + p.ID + "/docs/" + f
	if c := s.call(t, "GET", url, b, ws, nil, nil); c != 200 {
		t.Fatal(c)
	}
	if c := s.call(t, "POST", url+"/cursor", b, ws, map[string]int{"anchor": 1, "head": 3}, nil); c != 204 {
		t.Fatal(c)
	}
	var comment db.CoresearchComment
	commentInput := map[string]any{"base": 0, "from": 1, "to": 3, "quote": "😀", "line": 1, "body": "member comment"}
	if c := s.call(t, "POST", url+"/comments", b, ws, commentInput, &comment); c != 201 {
		t.Fatalf("member comment %d", c)
	}
	commentURL := csProjects + "/" + p.ID + "/comments/" + comment.ID.String()
	if c := s.call(t, "DELETE", csProjects+"/"+p.ID+"/members/"+s.userID(t, b), a, ws, nil, nil); c != 204 {
		t.Fatal(c)
	}
	for _, r := range []struct {
		method, suffix string
		body           any
	}{{"GET", "", nil}, {"GET", "/steps?after=0", nil}, {"GET", "/presence", nil}, {"POST", "/steps", map[string]any{"base": 0, "client_id": "b", "steps": []any{[]any{6}}}}, {"POST", "/cursor", map[string]int{"anchor": 1, "head": 3}}} {
		if c := s.call(t, r.method, url+r.suffix, b, ws, r.body, nil); c != 404 {
			t.Fatalf("%s %s = %d", r.method, r.suffix, c)
		}
	}
	for _, endpoint := range []struct {
		method, url string
		body        any
	}{
		{"GET", url + "/comments", nil},
		{"POST", url + "/comments", commentInput},
		{"POST", commentURL + "/resolve", nil},
		{"DELETE", commentURL, nil},
	} {
		if c := s.call(t, endpoint.method, endpoint.url, b, ws, endpoint.body, nil); c != 404 {
			t.Fatalf("removed comment access %s %s = %d", endpoint.method, endpoint.url, c)
		}
	}
	var people []map[string]any
	s.call(t, "GET", url+"/presence", a, ws, nil, &people)
	if len(people) != 0 {
		t.Fatal("removed member presence leaked")
	}
}
func TestDocLiveSaveAndPresence(t *testing.T) {
	s, a, _, ws, p, f := paperFixture(t)
	url := csProjects + "/" + p.ID + "/docs/" + f
	if c := s.call(t, "GET", url, a, ws, nil, nil); c != 200 {
		t.Fatal(c)
	}
	if c, _, b := csUploadResponse(t, s, a, ws, p.ID, "main.tex", 1, true, []byte("lost")); c != 409 || !strings.Contains(string(b), "live document") {
		t.Fatalf("save %d %s", c, b)
	}
	if c := s.call(t, "POST", url+"/cursor", a, ws, map[string]int{"anchor": 1, "head": 3}, nil); c != 204 {
		t.Fatal(c)
	}
	var people []struct {
		UserID       string `json:"user_id"`
		Anchor, Head int
	}
	if c := s.call(t, "GET", url+"/presence", a, ws, nil, &people); c != 200 || len(people) != 1 || people[0].Head != 3 {
		t.Fatalf("presence %d %+v", c, people)
	}
	ttl := s.Env.Redis.TTL(t.Context(), "lyk:presence:"+f+":"+s.userID(t, a)).Val()
	if ttl <= 0 || ttl > 10*time.Second {
		t.Fatal(ttl)
	}
	s.H.Hub.Redis = nil
	if c := s.call(t, "POST", url+"/cursor", a, ws, map[string]int{"anchor": 1, "head": 3}, nil); c != 204 {
		t.Fatal(c)
	}
	if c := s.call(t, "GET", url+"/presence", a, ws, nil, &people); c != 200 || len(people) != 0 {
		t.Fatalf("nil redis %d %+v", c, people)
	}
}

func TestDocSnapshotStagesOutsideTransaction(t *testing.T) {
	s, a, _, ws, p, f := paperFixture(t)
	url := csProjects + "/" + p.ID + "/docs/" + f
	var d doc.Doc
	if c := s.call(t, "GET", url, a, ws, nil, &d); c != 200 {
		t.Fatal(c)
	}
	file := doc.File{Record: db.CoresearchFile{ID: uuid.MustParse(f), WorkspaceID: uuid.MustParse(ws), ProjectID: uuid.MustParse(p.ID), Path: "main.tex"}}
	prepared := doc.PrepareSnapshot(d)
	who := uuid.MustParse(s.userID(t, a))
	workspace := uuid.MustParse(ws)
	n := 0
	err := coresearch.WithBlobs(t.Context(), s.H.Store, s.H.Blobs, workspace, who, []coresearch.UploadBlob{{SHA256: prepared.SHA256, Body: strings.NewReader(prepared.Text), Size: int64(len(prepared.Text))}}, func() error {
		return s.H.Store.InWorkspace(t.Context(), workspace, who, func(tx *store.Tx) error {
			_, e := tx.Q.LockWorkspaceForMutation(t.Context(), workspace)
			if e != nil {
				return e
			}
			n, e = doc.Snapshot(t.Context(), tx, file, prepared, doc.Author{UserID: &who})
			return e
		})
	})
	if err != nil || n != 2 {
		t.Fatalf("snapshot n=%d %v", n, err)
	}
	var hash string
	if e := s.Env.Owner.QueryRow(t.Context(), `SELECT sha256 FROM coresearch_file_version WHERE file_id=$1 AND n=2`, f).Scan(&hash); e != nil || hash != prepared.SHA256 {
		t.Fatalf("snapshot hash %s %v", hash, e)
	}
	if c := s.call(t, "POST", url+"/steps", a, ws, map[string]any{"base": 0, "client_id": "a", "steps": []any{[]any{6, []any{0, "!"}}}}, nil); c != 200 {
		t.Fatal(c)
	}
	err = s.H.Store.InWorkspace(t.Context(), workspace, who, func(tx *store.Tx) error {
		_, e := doc.Snapshot(t.Context(), tx, file, prepared, doc.Author{UserID: &who})
		return e
	})
	var behind doc.ErrBehind
	if !errors.As(err, &behind) {
		t.Fatalf("stale snapshot accepted: %v", err)
	}
}

func TestDocStepsMapPendingAndComments(t *testing.T) {
	s, a, _, ws, p, f := paperFixture(t)
	url := csProjects + "/" + p.ID + "/docs/" + f
	s.call(t, "GET", url, a, ws, nil, nil)
	proposal, change, comment := uuid.New(), uuid.New(), uuid.New()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO coresearch_proposal(id,workspace_id,project_id) VALUES($1,$2,$3)`, []any{proposal, ws, p.ID}},
		{`INSERT INTO coresearch_change(id,workspace_id,proposal_id,file_id,from_pos,to_pos,removed_text,status) VALUES($1,$2,$3,$4,1,3,'old','pending')`, []any{change, ws, proposal, f}},
		{`INSERT INTO coresearch_comment(id,workspace_id,file_id,from_pos,to_pos,quote,line,author_user_id,body) VALUES($1,$2,$3,1,3,'😀',1,$4,'note')`, []any{comment, ws, f, s.userID(t, a)}},
	} {
		if _, e := s.Env.Owner.Exec(t.Context(), q.sql, q.args...); e != nil {
			t.Fatal(e)
		}
	}
	push := func(base int, cs doc.ChangeSet) {
		t.Helper()
		if c := s.call(t, "POST", url+"/steps", a, ws, map[string]any{"base": base, "client_id": "a", "steps": []doc.ChangeSet{cs}}, nil); c != 200 {
			t.Fatal(c)
		}
	}
	push(0, doc.Replace(1, 1, "X", 6))
	var from, to int
	var status string
	if e := s.Env.Owner.QueryRow(t.Context(), `SELECT from_pos,to_pos,status FROM coresearch_change WHERE id=$1`, change).Scan(&from, &to, &status); e != nil || from != 2 || to != 4 || status != "pending" {
		t.Fatalf("boundary range %d %d %s %v", from, to, status, e)
	}
	push(1, doc.Replace(2, 4, "z", 7))
	if e := s.Env.Owner.QueryRow(t.Context(), `SELECT from_pos,to_pos,status FROM coresearch_change WHERE id=$1`, change).Scan(&from, &to, &status); e != nil || status != "stale" {
		t.Fatalf("overlap %s %v", status, e)
	}
	if e := s.Env.Owner.QueryRow(t.Context(), `SELECT from_pos,to_pos FROM coresearch_comment WHERE id=$1`, comment).Scan(&from, &to); e != nil || from != 2 || to != 3 {
		t.Fatalf("comment %d %d %v", from, to, e)
	}
}
func TestDocStepsPagination(t *testing.T) {
	s, a, _, ws, p, f := paperFixture(t)
	url := csProjects + "/" + p.ID + "/docs/" + f
	s.call(t, "GET", url, a, ws, nil, nil)
	steps := make([][]int, 500)
	for i := range steps {
		steps[i] = []int{6}
	}
	for _, batch := range []struct {
		base  int
		steps [][]int
	}{{0, steps}, {500, steps[:1]}} {
		if c := s.call(t, "POST", url+"/steps", a, ws, map[string]any{"base": batch.base, "client_id": "a", "steps": batch.steps}, nil); c != 200 {
			t.Fatal(c)
		}
	}
	for _, page := range []struct{ after, count int }{{0, 500}, {500, 1}, {501, 0}} {
		var steps []doc.Step
		if c := s.call(t, "GET", url+"/steps?after="+strconv.Itoa(page.after), a, ws, nil, &steps); c != 200 || len(steps) != page.count {
			t.Fatalf("page %d: %d len%d", page.after, c, len(steps))
		}
	}
}

type docHydrationStorage struct {
	storage.Storage
	once   atomic.Bool
	before func()
}

func (b *docHydrationStorage) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	if b.once.CompareAndSwap(false, true) {
		b.before()
	}
	return b.Storage.Get(ctx, key)
}
func TestDocOpenRetriesChangedSourceOutsideLocks(t *testing.T) {
	s, a, _, ws, p, f := paperFixture(t)
	s.H.Blobs = &docHydrationStorage{Storage: s.H.Blobs, before: func() {
		if c, _ := csUpload(t, s, a, ws, p.ID, "main.tex", 1, false, []byte("new head😀")); c != 201 {
			t.Fatalf("save during hydration: %d", c)
		}
	}}
	var d doc.Doc
	if c := s.call(t, "GET", csProjects+"/"+p.ID+"/docs/"+f, a, ws, nil, &d); c != 200 || d.Text != "new head😀" || d.Version != 0 {
		t.Fatalf("retry %d %+v", c, d)
	}
}

func TestDocStepsMapStaleRanges(t *testing.T) {
	for _, sameBatch := range []bool{false, true} {
		t.Run(fmt.Sprintf("same_batch_%t", sameBatch), func(t *testing.T) {
			s, a, _, ws, p, f := paperFixture(t)
			url := csProjects + "/" + p.ID + "/docs/" + f
			if code := s.call(t, "GET", url, a, ws, nil, nil); code != 200 {
				t.Fatal(code)
			}
			proposal, change := uuid.New(), uuid.New()
			if _, err := s.Env.Owner.Exec(t.Context(), `INSERT INTO coresearch_proposal(id,workspace_id,project_id) VALUES($1,$2,$3)`, proposal, ws, p.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Env.Owner.Exec(t.Context(), `INSERT INTO coresearch_change(id,workspace_id,proposal_id,file_id,from_pos,to_pos,removed_text,status) VALUES($1,$2,$3,$4,1,3,'old','pending')`, change, ws, proposal, f); err != nil {
				t.Fatal(err)
			}
			steps := []doc.ChangeSet{doc.Replace(1, 3, "z", 6), doc.Replace(0, 0, "😀\r\n", 5), doc.Replace(0, 2, "", 9)}
			batches := [][]doc.ChangeSet{steps}
			if !sameBatch {
				batches = [][]doc.ChangeSet{{steps[0]}, {steps[1]}, {steps[2]}}
			}
			base := 0
			for _, batch := range batches {
				if code := s.call(t, "POST", url+"/steps", a, ws, map[string]any{"base": base, "client_id": "a", "steps": batch}, nil); code != 200 {
					t.Fatal(code)
				}
				base += len(batch)
			}
			var from, to int
			var status string
			if err := s.Env.Owner.QueryRow(t.Context(), `SELECT from_pos,to_pos,status FROM coresearch_change WHERE id=$1`, change).Scan(&from, &to, &status); err != nil {
				t.Fatal(err)
			}
			if from != 3 || to != 4 || status != "stale" {
				t.Fatalf("stale range lost after prefix edits: [%d,%d) %s; want [3,4) stale", from, to, status)
			}
			var d doc.Doc
			if code := s.call(t, "GET", url, a, ws, nil, &d); code != 200 {
				t.Fatal(code)
			}
			got, err := doc.Slice(d.Text, from, to)
			if err != nil || got != "z" {
				t.Fatalf("stale Undo target %q, %v", got, err)
			}
		})
	}
}
