package handler

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestEveryRoleRecordSerializesBeforeInsertion(t *testing.T) {
	for _, kind := range []string{"plain"} {
		t.Run(kind, func(t *testing.T) {
			s := newServer(t)
			_, _, card, run := s.roleFixture(t)
			ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
			defer cancel()
			gate, err := s.Env.Owner.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer gate.Rollback(context.Background())
			// A foreign-key-compatible card lock must still exclude every record writer.
			if _, err = gate.Exec(ctx, `SELECT id FROM card WHERE id=$1 FOR NO KEY UPDATE`, card.ID); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				req, err := http.NewRequestWithContext(ctx, "POST", s.URL+"/api/role/records", strings.NewReader(fmt.Sprintf(`{"kind":%q,"body":{}}`, kind)))
				if err != nil {
					done <- err
					return
				}
				req.Header.Set("Authorization", "Bearer "+run.token)
				req.Header.Set("Content-Type", "application/json")
				res, err := http.DefaultClient.Do(req)
				if err == nil {
					res.Body.Close()
					if res.StatusCode != 201 {
						err = fmt.Errorf("record status %d", res.StatusCode)
					}
				}
				done <- err
			}()
			tick := time.NewTicker(time.Millisecond)
			defer tick.Stop()
			for {
				var query string
				err = s.Env.Owner.QueryRow(ctx, `SELECT COALESCE((SELECT query FROM pg_stat_activity WHERE datname=current_database() AND $1::int=ANY(pg_blocking_pids(pid)) LIMIT 1),'')`, gate.Conn().PgConn().PID()).Scan(&query)
				if err != nil {
					t.Fatal(err)
				}
				if query != "" {
					if !strings.Contains(query, "LockCard") {
						t.Fatalf("writer reached insertion before card serialization: %s", query)
					}
					break
				}
				select {
				case err := <-done:
					t.Fatalf("record crossed card snapshot fence: %v", err)
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				case <-tick.C:
				}
			}
			if err = gate.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if err = <-done; err != nil {
				t.Fatal(err)
			}
			var n int
			if err = s.Env.Owner.QueryRow(ctx, `SELECT count(*) FROM record WHERE card_id=$1 AND kind=$2`, card.ID, kind).Scan(&n); err != nil || n != 1 {
				t.Fatalf("committed record count %d: %v", n, err)
			}
		})
	}
}
