package handler

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"lykeion/server/internal/storage"
	db "lykeion/server/pkg/db/generated"
)

type cellReadStorage struct {
	storage.Storage
	gets   atomic.Int32
	before func(context.Context)
}

func (s *cellReadStorage) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	s.gets.Add(1)
	if s.before != nil {
		s.before(ctx)
	}
	return s.Storage.Get(ctx, key)
}
func TestPersonCellOutputsMembershipAndStorage(t *testing.T) {
	s, a, b, c, ws, p := csFixture(t)
	path, chat := csChat(t, s, a, ws, p.ID)
	var cell db.CoresearchCell
	if code := s.call(t, "POST", path+"/cells", a, ws, map[string]string{"kernel": "python3", "source": "1"}, &cell); code != 201 {
		t.Fatal(code)
	}
	ref := csRegister(t, s, a, ws, "work", "claude")
	csWork(t, s, a, "work", ref)
	outputs := json.RawMessage(`[{"type":"stream","name":"stdout","text":"` + strings.Repeat("x", 1<<20) + `"}]`)
	if code := s.call(t, "POST", "/api/daemon/coresearch/cells/"+cell.ID.String()+"/finish?daemon_id=work", s.personalToken(t, a), ws, map[string]any{"status": "ok", "outputs": outputs}, nil); code != 204 {
		t.Fatal(code)
	}
	blobs := &cellReadStorage{Storage: s.H.Blobs}
	s.H.Blobs = blobs
	url := path + "/cells/" + cell.ID.String() + "/outputs"
	for _, token := range []string{b, c} {
		if code := s.call(t, "GET", url, token, ws, nil, nil); code != 404 {
			t.Fatalf("hidden %d", code)
		}
	}
	otherWS := s.workspace(t, a, "other").ID
	if code := s.call(t, "GET", url, a, otherWS, nil, nil); code != 404 {
		t.Fatal(code)
	}
	foreign, _ := csChat(t, s, a, ws, p.ID)
	if code := s.call(t, "GET", foreign+"/cells/"+cell.ID.String()+"/outputs", a, ws, nil, nil); code != 404 {
		t.Fatal(code)
	}
	var otherProject csProject
	s.call(t, "POST", csProjects, a, ws, map[string]string{"name": "Other"}, &otherProject)
	otherPath := csProjects + "/" + otherProject.ID + "/chats/" + chat.ID.String() + "/cells/" + cell.ID.String() + "/outputs"
	if code := s.call(t, "GET", otherPath, a, ws, nil, nil); code != 404 {
		t.Fatal(code)
	}
	if blobs.gets.Load() != 0 {
		t.Fatal("unauthorized storage read")
	}
	csAdd(t, s, a, b, ws, p.ID, "member")
	// A separate connection must acquire the exact member row lock while Get
	// runs. This would time out if memberTx still held its FOR SHARE lock.
	blobs.before = func(ctx context.Context) {
		ctx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		tx, err := s.Env.Owner.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(context.Background())
		if _, err = tx.Exec(ctx, `SELECT user_id FROM member WHERE workspace_id=$1 AND user_id=$2 FOR UPDATE`, ws, s.userID(t, b)); err != nil {
			t.Errorf("storage inside member transaction: %v", err)
		}
	}
	var got json.RawMessage
	if code := s.call(t, "GET", url, b, ws, nil, &got); code != 200 || string(got) != string(outputs) {
		t.Fatalf("member outputs %d size=%d", code, len(got))
	}
	if blobs.gets.Load() != 1 {
		t.Fatal("blob not read")
	}
}
func TestPersonCellOutputsShapesAndCorruption(t *testing.T) {
	s, a, _, _, ws, p := csFixture(t)
	path, _ := csChat(t, s, a, ws, p.ID)
	var cell db.CoresearchCell
	s.call(t, "POST", path+"/cells", a, ws, map[string]string{"kernel": "x", "source": "x"}, &cell)
	url := path + "/cells/" + cell.ID.String() + "/outputs"
	for _, tc := range []struct{ raw, want string }{{"null", "[]"}, {`[{"type":"text","text":"hi"}]`, `[{"text":"hi","type":"text"}]`}, {`"old text"`, `[{"type":"text","text":"old text"}]`}, {`{"legacy":true}`, `[{"type":"text","text":"{\"legacy\": true}"}]`}} {
		if _, e := s.Env.Owner.Exec(t.Context(), `UPDATE coresearch_cell SET outputs=$1::jsonb,outputs_blob=NULL WHERE id=$2`, tc.raw, cell.ID); e != nil {
			t.Fatal(e)
		}
		var got json.RawMessage
		if code := s.call(t, "GET", url, a, ws, nil, &got); code != 200 {
			t.Fatal(code)
		}
		var v, w any
		json.Unmarshal(got, &v)
		json.Unmarshal([]byte(tc.want), &w)
		vb, _ := json.Marshal(v)
		wb, _ := json.Marshal(w)
		if string(vb) != string(wb) {
			t.Fatalf("%s got %s want %s", tc.raw, got, tc.want)
		}
	}
	key := "test-cell-output"
	s.Env.Owner.Exec(t.Context(), `UPDATE coresearch_cell SET outputs=NULL,outputs_blob=$1 WHERE id=$2`, key, cell.ID)
	if code := s.call(t, "GET", url, a, ws, nil, nil); code != 404 {
		t.Fatal(code)
	}
	for _, raw := range []string{``, `broken`, strings.Repeat("x", (20<<20)+1), `"` + strings.Repeat("x", (20<<20)-2) + `"`} {
		if e := s.H.Blobs.Put(t.Context(), key, strings.NewReader(raw), int64(len(raw))); e != nil {
			t.Fatal(e)
		}
		if code := s.call(t, "GET", url, a, ws, nil, nil); code != 500 {
			t.Fatalf("corrupt or oversized %d", code)
		}
	}
}
