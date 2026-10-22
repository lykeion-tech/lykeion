package migrations_test

import (
	"io/fs"
	"testing"
	"time"

	"lykeion/server/internal/testutil"
	"lykeion/server/migrations"
)

func TestAttachmentsMigration(t *testing.T) {
	env := testutil.Setup(t)
	ctx := t.Context()
	u, ws, card := seedWorkspace(t, env)
	ins := `INSERT INTO attachment (workspace_id, uploader_id, card_id, name, content_type, size_bytes, sha256, blob_key)
		VALUES ($1, $2, $3, 'a.csv', 'text/csv', $4, 'h', 'k')`
	if _, err := env.Owner.Exec(ctx, ins, ws, u, card, int64(104857601)); err == nil {
		t.Fatal("a file over 100 MB was accepted")
	}
	if _, err := env.Owner.Exec(ctx, ins, ws, u, card, int64(104857600)); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Owner.Exec(ctx, ins, ws, u, nil, int64(1)); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Owner.Exec(ctx, `DELETE FROM card WHERE id = $1`, card); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := env.Owner.QueryRow(ctx, `SELECT count(*) FROM attachment`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("after the study went: %d rows (%v), want only the pending one", n, err)
	}

	// The sweep reads across workspaces through a function the app role may call.
	if _, err := env.Owner.Exec(ctx, `UPDATE attachment SET created_at = now() - interval '2 days'`); err != nil {
		t.Fatal(err)
	}
	var stale int
	if err := env.App.QueryRow(ctx, `SELECT count(*) FROM stale_pending_attachments($1)`, time.Now().Add(-24*time.Hour)).Scan(&stale); err != nil || stale != 1 {
		t.Fatalf("stale pending: %d (%v), want 1", stale, err)
	}

	read := func(name string) string {
		b, err := fs.ReadFile(migrations.FS, name)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	if _, err := env.Owner.Exec(ctx, read("0021_study_attachments.down.sql")); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Owner.Exec(ctx, read("0021_study_attachments.up.sql")); err != nil {
		t.Fatal(err)
	}
}
