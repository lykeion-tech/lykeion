package migrations_test

import (
	"context"
	"testing"

	"lykeion/server/internal/testutil"
)

// Dropping the column's CHECK accepts the array.
func TestRuntimeBasesIsAnObject(t *testing.T) {
	env := testutil.Setup(t)
	ctx := context.Background()
	u, ws, _ := seedWorkspace(t, env)
	runtime := seedUI2Runtime(t, env, ws, u)
	_, err := env.Owner.Exec(ctx, `UPDATE runtime SET bases = '[]' WHERE id = $1`, runtime)
	wantCode(t, err, "23514", "array bases")
	if _, err := env.Owner.Exec(ctx, `UPDATE runtime SET bases = '{"python":"ready"}' WHERE id = $1`, runtime); err != nil {
		t.Fatal(err)
	}
}
