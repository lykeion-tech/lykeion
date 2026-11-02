package handler

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// Renaming or deleting a machine and resolving its Inbox item lock the
// workspace row before its rows (every member write does, in the workspace
// middleware), as a health report takes the workspace's locks first: one
// racing a writer that holds those locks and then reaches for the runtime
// row and the item must not deadlock.
func TestMachineWritesTakeLocksInWorkspaceOrder(t *testing.T) {
	for _, tc := range []struct{ name, method, path string }{
		{"rename", "PATCH", "/api/runtimes/%s"},
		{"delete", "DELETE", "/api/runtimes/%s"},
		{"resolve", "POST", "/api/inbox/%s/resolve"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newServer(t)
			ctx := context.Background()
			a := s.signIn(t, "a@lab.org")
			w := s.workspace(t, a, "lab")
			tok := s.personalToken(t, a)
			var rts []struct{ ID string }
			if code := s.call(t, "POST", "/api/daemon/register", tok, "", map[string]any{"workspace_id": w.ID, "daemon_id": "d1", "device_name": "laptop", "harnesses": []string{"claude"}}, &rts); code != 200 || len(rts) != 1 {
				t.Fatalf("register: %d %+v", code, rts)
			}
			rt := rts[0].ID
			if code := s.call(t, "POST", "/api/daemon/runtimes/"+rt+"/health", tok, "", map[string]any{"workspace_id": w.ID, "ok": false, "reason": "cli_broken"}, nil); code != 204 {
				t.Fatalf("unhealthy report: %d", code)
			}
			var item string
			if err := s.Env.Owner.QueryRow(ctx, `SELECT id::text FROM inbox_item WHERE runtime_id = $1 AND resolved_at IS NULL`, rt).Scan(&item); err != nil {
				t.Fatal(err)
			}
			target := rt
			if tc.name == "resolve" {
				target = item
			}

			holder, err := s.Env.Owner.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer holder.Rollback(ctx)
			var pid int
			if err := holder.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
				t.Fatal(err)
			}
			// The locks a health report takes first, as LockWorkspaceExecution
			// and LockWorkspaceEvents take them.
			if _, err := holder.Exec(ctx, `SELECT id FROM workspace WHERE id = $1 FOR NO KEY UPDATE`, w.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := holder.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1::uuid::text, 0))`, w.ID); err != nil {
				t.Fatal(err)
			}
			code := make(chan int, 1)
			go func() {
				code <- s.call(t, tc.method, fmt.Sprintf(tc.path, target), a, w.ID, map[string]any{"custom_name": "bench"}, nil)
			}()
			deadline := time.Now().Add(10 * time.Second)
			for {
				var blocked int
				if err := s.Env.Owner.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE $1 = ANY(pg_blocking_pids(pid))`, pid).Scan(&blocked); err != nil {
					t.Fatal(err)
				}
				if blocked > 0 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("the request never waited for the workspace's locks")
				}
				time.Sleep(20 * time.Millisecond)
			}
			// Then, as a health report does, the runtime row and its item.
			if _, err := holder.Exec(ctx, `SELECT 1 FROM runtime WHERE id = $1 FOR UPDATE`, rt); err != nil {
				t.Fatalf("holder: %v", err)
			}
			if _, err := holder.Exec(ctx, `SELECT 1 FROM inbox_item WHERE id = $1 FOR UPDATE`, item); err != nil {
				t.Fatalf("holder: %v", err)
			}
			if err := holder.Commit(ctx); err != nil {
				t.Fatalf("holder: %v", err)
			}
			if c := <-code; c != 204 {
				t.Fatalf("%s: %d", tc.name, c)
			}
		})
	}
}
