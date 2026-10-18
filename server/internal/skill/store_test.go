package skill

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"lykeion/server/internal/storage"
	"lykeion/server/internal/store"
	"lykeion/server/internal/testutil"
)

func TestWorkspaceSkillBlobIntegrityAndFailure(t *testing.T) {
	env := testutil.Setup(t)
	ctx := context.Background()
	ws, user := uuid.New(), uuid.New()
	for _, seed := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO "user" (id,email) VALUES ($1,'a@lab.org')`, []any{user}},
		{`INSERT INTO workspace (id,slug,name) VALUES ($1,'lab','Lab')`, []any{ws}},
		{`INSERT INTO member (workspace_id,user_id,role) VALUES ($1,$2,'owner')`, []any{ws, user}},
	} {
		if _, err := env.Owner.Exec(ctx, seed.sql, seed.args...); err != nil {
			t.Fatal(err)
		}
	}
	dir := t.TempDir()
	badDir := filepath.Join(dir, "not-a-directory")
	if err := os.WriteFile(badDir, []byte("file"), 0600); err != nil {
		t.Fatal(err)
	}
	service := Service{Store: &store.Store{Pool: env.App}, Blobs: storage.Local{Dir: badDir}}
	b := Bundle{Files: map[string][]byte{"SKILL.md": []byte("---\nname: integrity-method\ndescription: Integrity\n---\nResearch\n")}}
	if _, err := service.Create(ctx, ws, user, b, Source{Kind: "local", Location: "method.zip"}); err == nil {
		t.Fatal("storage failure reported success")
	}
	var count int
	if err := env.Owner.QueryRow(ctx, `SELECT count(*) FROM workspace_skill`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("storage failure published metadata: %d %v", count, err)
	}
	service.Blobs = storage.Local{Dir: filepath.Join(dir, "blobs")}
	created, err := service.Create(ctx, ws, user, b, Source{Kind: "local", Location: "method.zip"})
	if err != nil {
		t.Fatal(err)
	}
	var key string
	if err := env.Owner.QueryRow(ctx, `SELECT blob_key FROM workspace_skill_version WHERE skill_id=$1`, created.ID).Scan(&key); err != nil {
		t.Fatal(err)
	}
	b.Files["SKILL.md"] = []byte("---\nname: integrity-method\ndescription: Integrity\n---\nAltered research\n")
	corrupt, err := Tar(b)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Blobs.Put(ctx, key, bytes.NewReader(corrupt), int64(len(corrupt))); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Get(ctx, ws, user, created.ID); err == nil {
		t.Fatal("corrupt content-addressed blob was accepted")
	}
	// Listing reads metadata alone and must not need the corrupted folder blob.
	list, err := service.List(ctx, ws, user)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range list {
		if s.ID == created.ID {
			found = true
			if s.Name != "integrity-method" || s.Revision != 1 || s.Domain != "Workspace" {
				t.Fatalf("summary: %+v", s)
			}
		}
	}
	if !found {
		t.Fatal("list omitted saved skill")
	}
}

func TestSkillScriptMetadata(t *testing.T) {
	env := testutil.Setup(t)
	ctx := context.Background()
	ws, user := uuid.New(), uuid.New()
	if _, err := env.Owner.Exec(ctx, `INSERT INTO "user" (id,email) VALUES ($1,'a@lab.org')`, user); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Owner.Exec(ctx, `INSERT INTO workspace (id,slug,name) VALUES ($1,'lab','Lab')`, ws); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Owner.Exec(ctx, `INSERT INTO member (workspace_id,user_id,role) VALUES ($1,$2,'owner')`, ws, user); err != nil {
		t.Fatal(err)
	}
	service := Service{Store: &store.Store{Pool: env.App}, Blobs: storage.Local{Dir: t.TempDir()}}
	for i, tc := range []struct {
		path string
		data []byte
		want bool
	}{
		{"run.py", []byte("print('method')"), true}, {"analysis.R", []byte("x <- 1"), true},
		{"scripts/data.bin", []byte{255, 0}, true}, {"run", []byte("#!/bin/sh\necho method\n"), true},
		{"run", []byte{'#', '!', 255}, false}, {"assets/data.bin", []byte{0, 255}, false}, {"references/method.md", []byte("Research"), false},
	} {
		name := fmt.Sprintf("script-method-%d", i)
		b := Bundle{Files: map[string][]byte{"SKILL.md": []byte("---\nname: " + name + "\n---\n"), tc.path: tc.data}}
		got, err := service.Create(ctx, ws, user, b, Source{Kind: "local"})
		if err != nil {
			t.Fatal(err)
		}
		if got.ContainsScripts != tc.want {
			t.Errorf("create %s: scripts=%v want %v", tc.path, got.ContainsScripts, tc.want)
		}
		list, err := service.List(ctx, ws, user)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range list {
			if s.ID == got.ID && s.ContainsScripts != tc.want {
				t.Errorf("list %s lost script metadata", tc.path)
			}
		}
	}
	nameless := Bundle{Files: map[string][]byte{"SKILL.md": []byte("Research")}}
	if _, err := service.Create(ctx, ws, user, nameless, Source{}); err == nil {
		t.Fatal("nameless bundle persisted")
	}
}
