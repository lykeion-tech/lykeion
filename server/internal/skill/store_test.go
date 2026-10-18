package skill

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

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

func TestWorkspaceSkillLongPathArchiveLifecycle(t *testing.T) {
	main := []byte("---\nname: long-path-method\ndescription: Preserve the accepted folder\n---\nResearch\n")
	binary := bytes.Repeat([]byte{0, 255}, 16384)
	files := map[string][]byte{"SKILL.md": main}
	prefix := strings.Repeat("p", 32400) + "/"
	target := ""
	for i := 0; i < 256; i++ {
		p := prefix + fmt.Sprintf("file-%03d.bin", i)
		files[p] = binary
		if i == 0 {
			target = p
		}
	}
	uploaded := zipFixture(t, files)
	if len(uploaded) > 16<<20 {
		t.Fatalf("fixture ZIP exceeds approved upload size: %d", len(uploaded))
	}
	bundle, err := ParseArchive(uploaded)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := Tar(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) <= 16<<20 {
		t.Fatalf("fixture tar does not expose upload/storage mismatch: %d", len(encoded))
	}
	t.Logf("accepted ZIP=%d bytes, persisted tar=%d bytes, supporting bytes=%d", len(uploaded), len(encoded), len(binary)*256)

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
	created, err := service.Create(ctx, ws, user, bundle, Source{Kind: "local", Location: "long-path.zip"})
	if err != nil {
		t.Fatal(err)
	}
	saved, err := service.Get(ctx, ws, user, created.ID)
	if err != nil {
		t.Fatalf("Get accepted ZIP after publication: %v", err)
	}
	if saved.Revision != 1 || saved.Content != string(main) || len(saved.Files) != 257 {
		t.Fatalf("saved folder metadata changed: revision=%d files=%d", saved.Revision, len(saved.Files))
	}
	checkFile := func() {
		t.Helper()
		got, err := service.File(ctx, ws, user, created.ID, target)
		if err != nil || !bytes.Equal(got, binary) {
			t.Fatalf("long-path binary file corrupted: %v", err)
		}
	}
	checkFile()
	edited, err := service.Update(ctx, ws, user, created.ID, 1, string(main)+"Edited research\n")
	if err != nil {
		t.Fatalf("edit accepted folder: %v", err)
	}
	if edited.Revision != 2 {
		t.Fatalf("edited revision: %d", edited.Revision)
	}
	saved, err = service.Get(ctx, ws, user, created.ID)
	if err != nil || saved.Content != string(main)+"Edited research\n" {
		t.Fatalf("read edited folder: %v", err)
	}
	checkFile()
	if err := service.Delete(ctx, ws, user, created.ID, 2); err != nil {
		t.Fatalf("delete accepted folder: %v", err)
	}
	if _, err := service.Get(ctx, ws, user, created.ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("deleted folder: %v", err)
	}
	var retained int
	if err := env.Owner.QueryRow(ctx, `SELECT count(*) FROM workspace_skill_version WHERE workspace_id=$1 AND skill_id=$2`, ws, created.ID).Scan(&retained); err != nil || retained != 2 {
		t.Fatalf("retained revisions: %d %v", retained, err)
	}
}

func TestSkillStoredArchiveEnvelopeUsesPayloadBounds(t *testing.T) {
	size, err := storedArchiveSize([]FileMeta{{Path: "SKILL.md", Size: 1}})
	if err != nil || size != 2048 {
		t.Fatalf("single-byte tar requires a header, padded payload and footer: %d %v", size, err)
	}
	oversizedAggregate := []FileMeta{{Path: "SKILL.md", Size: 1}}
	for i := 0; i < 9; i++ {
		oversizedAggregate = append(oversizedAggregate, FileMeta{Path: fmt.Sprintf("asset-%d", i), Size: 1 << 20})
	}
	excessiveCount := []FileMeta{{Path: "SKILL.md", Size: 1}}
	for i := 0; i < 257; i++ {
		excessiveCount = append(excessiveCount, FileMeta{Path: fmt.Sprintf("asset-%d", i), Size: 0})
	}
	for _, files := range [][]FileMeta{
		nil, {{Path: "SKILL.md", Size: (1 << 20) + 1}}, {{Path: "SKILL.md", Size: -1}},
		{{Path: "../SKILL.md", Size: 1}}, {{Path: "asset", Size: 1}},
		{{Path: "SKILL.md", Size: 1}, {Path: "SKILL.md", Size: 1}},
		{{Path: "SKILL.md", Size: 1}, {Path: "assets", Size: 0}, {Path: "assets/a.bin", Size: 1}},
		oversizedAggregate, excessiveCount,
	} {
		if _, err := storedArchiveSize(files); err == nil {
			t.Fatal("invalid manifest obtained a storage read envelope")
		}
	}
}
