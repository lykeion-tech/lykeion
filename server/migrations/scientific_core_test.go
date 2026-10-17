package migrations_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"lykeion/server/internal/testutil"
)

var coreInboxKinds = []string{"escalated", "released", "middle_score", "blocked", "done"}

func TestScientificCore(t *testing.T) {
	env := testutil.Setup(t)
	ctx := context.Background()
	u, ws, card := seedWorkspace(t, env)

	for name, q := range map[string]string{
		"block_kind other": `UPDATE card SET block_kind = 'other' WHERE id = $1`,
	} {
		if _, err := env.Owner.Exec(ctx, q, card); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}

	pack := `INSERT INTO field_pack (workspace_id, name, version, sha256, manifest, blob_key, source)
		VALUES ($1, 'p', '1', 'h', '{}', 'k', 'shipped')`
	if _, err := env.Owner.Exec(ctx, pack, ws); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Owner.Exec(ctx, pack, ws); err == nil {
		t.Error("a second (workspace, name, version) pack was accepted")
	}

	if _, err := env.Owner.Exec(ctx, `INSERT INTO inbox_item (workspace_id, user_id, card_id, kind, text)
		VALUES ($1, $2, $3, 'blocked', 't')`, ws, u, card); err != nil {
		t.Errorf("blocked inbox item: %v", err)
	}
	if _, err := env.Owner.Exec(ctx, `INSERT INTO autoresearch (workspace_id, owner_id, title, execution_mode)
		VALUES ($1, $2, 't', 'watch_released')`, ws, u); err == nil {
		t.Error("watch_released autoresearch was accepted")
	}

	tx, err := env.App.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SELECT set_config('app.workspace_id', $1, true)", uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM field_pack`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("another workspace saw %d packs", n)
	}
}

func TestCoreInboxKinds(t *testing.T) {
	env := testutil.Setup(t)
	ctx := context.Background()
	u, ws, card := seedWorkspace(t, env)
	for _, k := range coreInboxKinds {
		if _, err := env.Owner.Exec(ctx, `INSERT INTO inbox_item (workspace_id, user_id, card_id, kind, text)
			VALUES ($1, $2, $3, $4, 't')`, ws, u, card, k); err != nil {
			t.Errorf("inbox kind %s: %v", k, err)
		}
	}
}
