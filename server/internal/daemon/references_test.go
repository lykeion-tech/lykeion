package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"lykeion/server/internal/service"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

func TestRevokedReferencesDoNotStarveHealthyWorkspace(t *testing.T) {
	for _, removal := range []string{"membership", "runtime"} {
		t.Run(removal, func(t *testing.T) {
			f := fixture(t)
			// Leave the first workspace busy so losing authority must cancel local work.
			bin := f.cfg.Harnesses["claude"]
			must(t, os.WriteFile(bin, []byte(`#!/bin/sh
IFS= read -r first
case "$PWD" in *`+f.id.String()+`*) echo ready > "$PWD/ready";exec sleep 300;; esac
echo '{"type":"result","is_error":false}'
`), 0700))
			healthyWS, healthyCard := uuid.New(), uuid.Nil
			must(t, f.runs.Store.InWorkspace(t.Context(), healthyWS, f.user, func(tx *store.Tx) error {
				if _, err := tx.Q.CreateWorkspace(t.Context(), db.CreateWorkspaceParams{ID: healthyWS, Slug: "healthy", Name: "Healthy"}); err != nil {
					return err
				}
				if err := tx.Q.AddMember(t.Context(), db.AddMemberParams{WorkspaceID: healthyWS, UserID: f.user, Role: "owner"}); err != nil {
					return err
				}
				c, err := tx.Q.CreateCard(t.Context(), db.CreateCardParams{BoardColumn: "grounding", WorkspaceID: healthyWS, Number: 1, Title: "healthy", Question: "q", OwnerID: f.user, FollowsFrom: []uuid.UUID{}})
				healthyCard = c.ID
				return err
			}))
			cancel, done := startDaemon(t, f.cfg)
			defer cancel()
			dir := filepath.Join(f.cfg.Home, f.id.String()+"-1")
			waitFor(t, func() bool { _, err := os.Stat(filepath.Join(dir, "ready")); return err == nil })
			var deletedRuntime uuid.UUID
			must(t, f.env.Owner.QueryRow(t.Context(), "SELECT runtime_id FROM agent_run WHERE id=$1", f.id).Scan(&deletedRuntime))
			query := "DELETE FROM member WHERE workspace_id=$1 AND user_id=$2"
			args := []any{f.ws, f.user}
			if removal == "runtime" {
				query = "DELETE FROM runtime WHERE id=$1"
				args = []any{deletedRuntime}
			}
			_, err := f.env.Owner.Exec(t.Context(), query, args...)
			must(t, err)
			var healthyID uuid.UUID
			must(t, f.runs.Store.InWorkspace(t.Context(), healthyWS, f.user, func(tx *store.Tx) error {
				r, err := f.runs.Enqueue(t.Context(), tx, f.user, service.NewAgentRun{Card: healthyCard, Column: "grounding", Round: 1, Role: "scoper", Harness: "claude", Prompt: "healthy", MaxAttempts: 1})
				healthyID = r.ID
				return err
			}))
			waitFor(t, func() bool {
				var status string
				must(t, f.env.Owner.QueryRow(t.Context(), "SELECT status FROM agent_run WHERE id=$1", healthyID).Scan(&status))
				return status == "completed"
			})
			waitFor(t, func() bool { _, err := os.Stat(dir); return os.IsNotExist(err) })
			var touched time.Time
			must(t, f.env.Owner.QueryRow(t.Context(), "SELECT last_seen_at FROM runtime WHERE workspace_id=$1", healthyWS).Scan(&touched))
			if time.Since(touched) > time.Second {
				t.Fatal("healthy heartbeat starved")
			}
			if removal == "runtime" {
				var n int
				must(t, f.env.Owner.QueryRow(t.Context(), "SELECT count(*) FROM runtime WHERE workspace_id=$1", f.ws).Scan(&n))
				if n != 0 {
					t.Fatal("explicitly deleted runtime recreated")
				}
			}
			stopDaemon(t, cancel, done)
			t.Log(fmt.Sprintf("%s removal preserved healthy completion and stopped revoked local work", removal))
		})
	}
}
