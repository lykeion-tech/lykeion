package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"lykeion/server/internal/skill"
	"lykeion/server/internal/storage"
)

type skillView struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Domain      string  `json:"domain"`
	Revision    int64   `json:"revision"`
	Editable    bool    `json:"editable"`
	Content     string  `json:"content"`
	CreatedBy   *string `json:"created_by"`
	Origin      string  `json:"origin"`
}

func createSkillFixture(t *testing.T, s *server, token, ws, name string) skillView {
	t.Helper()
	var got skillView
	if c := s.call(t, "POST", "/api/skills", token, ws, map[string]string{"name": name, "description": "A workspace method"}, &got); c != 201 {
		t.Fatalf("create: %d %+v", c, got)
	}
	return got
}

// Removing the importer/admin check would allow a member to mutate this skill.
func TestWorkspaceSkillCRUDAndPermissions(t *testing.T) {
	s := newServer(t)
	owner := s.signIn(t, "owner@lab.org")
	ws := s.workspace(t, owner, "lab")
	importer := s.member(t, owner, ws.ID, "importer@lab.org", "member")
	member := s.member(t, owner, ws.ID, "reader@lab.org", "member")
	admin := s.member(t, owner, ws.ID, "admin@lab.org", "admin")
	foreign := s.signIn(t, "foreign@lab.org")
	other := s.workspace(t, foreign, "other")
	got := createSkillFixture(t, s, importer, ws.ID, "lab-method")
	if got.Domain != "Workspace" || got.Origin != "imported" || got.Revision != 1 || got.CreatedBy == nil || !got.Editable || !strings.Contains(got.Content, "name: lab-method") {
		t.Fatalf("created: %+v", got)
	}
	var read skillView
	if c := s.call(t, "GET", "/api/skills/"+got.ID, member, ws.ID, nil, &read); c != 200 || read.Editable || read.Content != got.Content {
		t.Fatalf("member read: %d %+v", c, read)
	}
	for _, method := range []string{"PATCH", "DELETE"} {
		body := map[string]any{"revision": 1}
		if method == "PATCH" {
			body["content"] = got.Content
		}
		if c := s.call(t, method, "/api/skills/"+got.ID, member, ws.ID, body, nil); c != 403 {
			t.Fatalf("member %s: %d", method, c)
		}
	}
	if c := s.call(t, "GET", "/api/skills/"+got.ID, foreign, other.ID, nil, nil); c != 404 {
		t.Fatalf("foreign tenant: %d", c)
	}
	if c := s.call(t, "POST", "/api/skills", member, ws.ID, map[string]string{"name": "lab-method", "description": "duplicate"}, nil); c != 409 {
		t.Fatalf("name conflict: %d", c)
	}
	content := "---\nname: renamed-method\ndescription: Revised method\n---\nDo the research.\n"
	if c := s.call(t, "PATCH", "/api/skills/"+got.ID, importer, ws.ID, map[string]any{"revision": 1, "content": content}, &got); c != 200 || got.Name != "renamed-method" || got.Description != "Revised method" || got.Revision != 2 {
		t.Fatalf("metadata edit: %d %+v", c, got)
	}
	var list json.RawMessage
	if c := s.call(t, "GET", "/api/skills", member, ws.ID, nil, &list); c != 200 || strings.Contains(string(list), `"content"`) || strings.Contains(string(list), `"files"`) || !strings.Contains(string(list), "renamed-method") {
		t.Fatalf("metadata list: %d %s", c, list)
	}
	if c := s.call(t, "PATCH", "/api/skills/"+got.ID, admin, ws.ID, map[string]any{"revision": 2, "content": content + "Admin change\n"}, &got); c != 200 || got.Revision != 3 {
		t.Fatalf("admin edit: %d", c)
	}
	if c := s.call(t, "DELETE", "/api/skills/"+got.ID, admin, ws.ID, map[string]any{"revision": 3}, nil); c != 204 {
		t.Fatalf("admin delete: %d", c)
	}
	if c := s.call(t, "GET", "/api/skills/"+got.ID, importer, ws.ID, nil, nil); c != 404 {
		t.Fatalf("deleted read: %d", c)
	}
	if n := s.count(t, `SELECT count(*) FROM workspace_skill_version WHERE skill_id=$1`, got.ID); n != 3 {
		t.Fatalf("retained versions: %d", n)
	}
	if n := s.count(t, `SELECT count(*) FROM event WHERE type LIKE 'skill.%' AND (payload ? 'content' OR payload ? 'files')`); n != 0 {
		t.Fatal("file bodies leaked into events")
	}
}
func TestBundledSkillCannotBeMutated(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	for _, method := range []string{"PATCH", "DELETE"} {
		body := map[string]any{"revision": 1}
		if method == "PATCH" {
			body["content"] = "bad"
		}
		if c := s.call(t, method, "/api/skills/bundle:peer-review", a, w.ID, body, nil); c != 403 {
			t.Fatalf("bundled %s: %d", method, c)
		}
	}
	if c := s.call(t, "POST", "/api/skills", a, w.ID, map[string]string{"name": "peer-review", "description": "clash"}, nil); c != 409 {
		t.Fatalf("bundled name clash: %d", c)
	}
	var got skillView
	if c := s.call(t, "GET", "/api/skills/bundle:peer-review", a, w.ID, nil, &got); c != 200 || got.Editable || got.Origin != "bundled" || got.Content == "" {
		t.Fatalf("bundled read: %d %+v", c, got)
	}
}
func TestSkillStaleWriteIsRejected(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	first := createSkillFixture(t, s, a, w.ID, "lab-method")
	var before skillView
	if c := s.call(t, "PATCH", "/api/skills/"+first.ID, a, w.ID, map[string]any{"revision": 1, "content": first.Content + "saved\n"}, &before); c != 200 {
		t.Fatal(c)
	}
	for _, method := range []string{"PATCH", "DELETE"} {
		var conflict struct {
			Code     string
			Revision int64
		}
		body := map[string]any{"revision": 1}
		if method == "PATCH" {
			body["content"] = first.Content + "lost\n"
		}
		c := s.call(t, method, "/api/skills/"+first.ID, a, w.ID, body, &conflict)
		var after skillView
		s.call(t, "GET", "/api/skills/"+first.ID, a, w.ID, nil, &after)
		if c != 409 || after.Revision != before.Revision || after.Content != before.Content || conflict.Code != "stale_revision" || conflict.Revision != 2 {
			t.Fatalf("stale %s changed skill: %d %+v %+v", method, c, conflict, after)
		}
	}
	other := createSkillFixture(t, s, a, w.ID, "other-method")
	if c := s.call(t, "PATCH", "/api/skills/"+other.ID, a, w.ID, map[string]any{"revision": 1, "content": first.Content}, nil); c != 409 {
		t.Fatalf("rename clash: %d", c)
	}
}

func TestSkillBinaryFileRoundTrips(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	ctx := context.Background()
	var user uuid.UUID
	if err := s.Env.Owner.QueryRow(ctx, `SELECT id FROM "user" WHERE email='a@lab.org'`).Scan(&user); err != nil {
		t.Fatal(err)
	}
	service := skill.Service{Store: s.H.Store, Blobs: s.H.Blobs}
	binary := []byte{0, 255, 254, 128, 42}
	got, err := service.Create(ctx, uuid.MustParse(w.ID), user, skill.Bundle{Files: map[string][]byte{"SKILL.md": []byte("---\nname: binary-method\ndescription: Preserve bytes\n---\n"), "assets/raw.bin": binary, "scripts/run.py": []byte("print('research')")}}, skill.Source{Kind: "local", Location: "binary.zip"})
	if err != nil {
		t.Fatal(err)
	}
	if !got.ContainsScripts {
		t.Fatal("script metadata absent")
	}
	found := false
	for _, f := range got.Files {
		if f.Path == "assets/raw.bin" {
			found = true
			if !f.Binary || f.Size != 5 || f.SHA256 != "fc1ec90702bde758f105a1a18d03c3de406d9fed7fefa8c8796756d04f1c2b69" {
				t.Fatalf("binary metadata: %+v", f)
			}
		}
	}
	if !found {
		t.Fatal("missing binary metadata")
	}
	read := func() {
		t.Helper()
		req, _ := http.NewRequest("GET", s.URL+"/api/skills/"+got.ID+"/files?path=assets/raw.bin", nil)
		req.Header.Set("Authorization", "Bearer "+a)
		req.Header.Set("X-Workspace-ID", w.ID)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		data, err := io.ReadAll(resp.Body)
		if err != nil || resp.StatusCode != 200 || !bytes.Equal(data, binary) || resp.Header.Get("Content-Type") == "" {
			t.Fatalf("binary file corrupted: %d %v", resp.StatusCode, data)
		}
	}
	read()
	if c := s.call(t, "PATCH", "/api/skills/"+got.ID, a, w.ID, map[string]any{"revision": 1, "content": got.Content + "Edited\n"}, nil); c != 200 {
		t.Fatal(c)
	}
	read()
	if c := s.call(t, "GET", "/api/skills/"+got.ID+"/files?path=../SKILL.md", a, w.ID, nil, nil); c != 400 {
		t.Fatalf("unsafe file path: %d", c)
	}
}

func TestSkillMaximumMainFileUsesBoundedJSONEnvelope(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	got := createSkillFixture(t, s, a, w.ID, "large-method")
	content := got.Content + strings.Repeat("\x01", (1<<20)-len(got.Content))
	var saved skillView
	if c := s.call(t, "PATCH", "/api/skills/"+got.ID, a, w.ID, map[string]any{"revision": 1, "content": content}, &saved); c != 200 || saved.Content != content || saved.Revision != 2 {
		t.Fatalf("valid 1MiB main file: %d revision %d", c, saved.Revision)
	}
	if c := s.call(t, "PATCH", "/api/skills/"+got.ID, a, w.ID, map[string]any{"revision": 2, "content": content + "x"}, nil); c != 400 {
		t.Fatalf("oversized main file: %d", c)
	}
	var after skillView
	s.call(t, "GET", "/api/skills/"+got.ID, a, w.ID, nil, &after)
	if after.Revision != 2 || after.Content != content {
		t.Fatal("oversized edit changed saved content")
	}
}

type skillBlobHook struct {
	storage.Storage
	hook func() error
}

func (b skillBlobHook) Put(ctx context.Context, key string, r io.Reader, size int64) error {
	if err := b.Storage.Put(ctx, key, r, size); err != nil {
		return err
	}
	return b.hook()
}

func TestWorkspaceSkillRechecksMembershipAfterBlobWrite(t *testing.T) {
	s := newServer(t)
	owner := s.signIn(t, "owner@lab.org")
	w := s.workspace(t, owner, "lab")
	importer := s.member(t, owner, w.ID, "importer@lab.org", "member")
	ctx := context.Background()
	var user uuid.UUID
	if err := s.Env.Owner.QueryRow(ctx, `SELECT id FROM "user" WHERE email='importer@lab.org'`).Scan(&user); err != nil {
		t.Fatal(err)
	}
	// Taking the workspace lock here proves the service released its read transaction before blob I/O.
	s.H.Blobs = skillBlobHook{Storage: s.H.Blobs, hook: func() error {
		tx, err := s.Env.Owner.Begin(ctx)
		if err != nil {
			return err
		}
		defer tx.Rollback(ctx)
		if _, err = tx.Exec(ctx, `SELECT id FROM workspace WHERE id=$1 FOR UPDATE NOWAIT`, w.ID); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `DELETE FROM member WHERE workspace_id=$1 AND user_id=$2`, w.ID, user); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}}
	if c := s.call(t, "POST", "/api/skills", importer, w.ID, map[string]string{"name": "revoked-method", "description": "recheck"}, nil); c != 404 {
		t.Fatalf("membership lost during blob write: %d", c)
	}
	if s.count(t, `SELECT count(*) FROM workspace_skill`) != 0 || s.count(t, `SELECT count(*) FROM workspace_skill_version`) != 0 || s.count(t, `SELECT count(*) FROM event WHERE type='skill.created'`) != 0 {
		t.Fatal("failed publication left visible skill metadata")
	}
}

func TestSkillConcurrentSavesHaveOneWinner(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	got := createSkillFixture(t, s, a, w.ID, "race-method")
	var arrivals atomic.Int32
	release := make(chan struct{})
	s.H.Blobs = skillBlobHook{Storage: s.H.Blobs, hook: func() error {
		if arrivals.Add(1) == 2 {
			close(release)
		}
		select {
		case <-release:
			return nil
		case <-time.After(5 * time.Second):
			return fmt.Errorf("second concurrent save never reached blob write")
		}
	}}
	results := raceCalls(t, func() int {
		return s.call(t, "PATCH", "/api/skills/"+got.ID, a, w.ID, map[string]any{"revision": 1, "content": got.Content + "one\n"}, nil)
	}, func() int {
		return s.call(t, "PATCH", "/api/skills/"+got.ID, a, w.ID, map[string]any{"revision": 1, "content": got.Content + "two\n"}, nil)
	})
	if !(results[0] == 200 && results[1] == 409 || results[0] == 409 && results[1] == 200) {
		t.Fatalf("concurrent saves: %v", results)
	}
	if s.count(t, `SELECT count(*) FROM workspace_skill_version WHERE skill_id=$1`, got.ID) != 2 || s.count(t, `SELECT count(*) FROM event WHERE type='skill.updated'`) != 1 {
		t.Fatal("concurrent saves published extra versions/events")
	}
}
