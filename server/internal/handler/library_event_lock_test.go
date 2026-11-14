package handler

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	db "lykeion/server/pkg/db/generated"
)

// Hold the event writer immediately before its real insert, then let the HTTP
// mutation reach the event lock. Both transactions must commit without retries.
func libraryMutationWithEventWriter(t *testing.T, s *server, ws string, mutation func() int, want int, publication ...bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	event, err := s.Env.Owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer event.Rollback(context.Background())
	done := make(chan int, 1)
	received := false
	if len(publication) > 0 && publication[0] {
		atBlob, resume := make(chan struct{}), make(chan struct{}, 1)
		s.H.Blobs = skillBlobHook{Storage: s.H.Blobs, hook: func() error {
			close(atBlob)
			select {
			case <-resume:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}}
		go func() {
			status := 0
			defer func() { done <- status }()
			status = mutation()
		}()
		defer close(resume)
		select {
		case <-atBlob:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		// Complete the authorization preflight before blocking publication.
		if err := db.New(event).LockWorkspaceEvents(ctx, uuid.MustParse(ws)); err != nil {
			t.Fatal(err)
		}
		// A buffered signal avoids closing the channel twice during cleanup.
		resume <- struct{}{}
	} else {
		if err := db.New(event).LockWorkspaceEvents(ctx, uuid.MustParse(ws)); err != nil {
			t.Fatal(err)
		}
		go func() {
			status := 0
			defer func() { done <- status }()
			status = mutation()
		}()
	}
	defer func() {
		event.Rollback(context.Background())
		if !received {
			<-done
		}
	}()
	q := db.New(event)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var blocked bool
		if err := s.Env.Owner.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND $1::int=ANY(pg_blocking_pids(pid)) AND query LIKE '%LockWorkspaceEvents%')", event.Conn().PgConn().PID()).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		select {
		case status := <-done:
			received = true
			t.Fatalf("mutation finished before event writer: %d", status)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-ticker.C:
		}
	}
	// This is the production event INSERT, including its workspace foreign key.
	// No synthetic row-lock probe can substitute for the writer's progress.
	written, err := q.InsertEvent(ctx, db.InsertEventParams{WorkspaceID: uuid.MustParse(ws), Type: "agent.updated", ActorType: "system", Payload: []byte(`{"action":"lock_regression"}`)})
	if err != nil {
		t.Fatalf("event writer insert failed: %v", err)
	}
	if err := event.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	status := <-done
	received = true
	if status != want {
		t.Fatalf("mutation status=%d want=%d", status, want)
	}
	if n := s.count(t, "SELECT count(*) FROM event WHERE workspace_id=$1 AND id=$2 AND type='agent.updated' AND payload->>'action'='lock_regression'", ws, written.ID); n != 1 {
		t.Fatalf("committed event writer receipts=%d", n)
	}
}

func TestLibraryMutationsCompatibleWithEventWriter(t *testing.T) {
	for _, operation := range []string{"skill-create", "skill-update", "skill-delete", "mcp-create", "mcp-update", "mcp-delete", "team-create", "runtime-import"} {
		t.Run(operation, func(t *testing.T) {
			f := runtimeSkillSetup(t)
			s, token, ws := f.s, f.jwt, f.ws
			var mutation func() int
			var verify func()
			want := 200
			switch operation {
			case "skill-create", "skill-update", "skill-delete":
				var original, got skillView
				if operation != "skill-create" {
					original = createSkillFixture(t, s, token, ws, "lock-method")
				}
				method, path, body := "POST", "/api/skills", any(map[string]string{"name": "lock-method", "description": "Lock regression"})
				want, eventType := 201, "skill.created"
				if operation == "skill-update" {
					method, path, body, want, eventType = "PATCH", path+"/"+original.ID, map[string]any{"revision": 1, "content": original.Content + "Updated content\n"}, 200, "skill.updated"
				}
				if operation == "skill-delete" {
					method, path, body, want, eventType = "DELETE", path+"/"+original.ID, map[string]any{"revision": 1}, 204, "skill.deleted"
				}
				mutation = func() int {
					if method == "DELETE" {
						return s.call(t, method, path, token, ws, body, nil)
					}
					return s.call(t, method, path, token, ws, body, &got)
				}
				libraryMutationWithEventWriter(t, s, ws, mutation, want)
				if n := s.count(t, "SELECT count(*) FROM event WHERE workspace_id=$1 AND type=$2", ws, eventType); n != 1 {
					t.Fatalf("mutation events=%d", n)
				}
				if operation == "skill-delete" {
					if status := s.call(t, "GET", "/api/skills/"+original.ID, token, ws, nil, nil); status != 404 {
						t.Fatalf("deleted read=%d", status)
					}
					if n := s.count(t, "SELECT count(*) FROM workspace_skill_version WHERE skill_id=$1", original.ID); n != 1 {
						t.Fatalf("retained versions=%d", n)
					}
				} else {
					var saved skillView
					if status := s.call(t, "GET", "/api/skills/"+got.ID, token, ws, nil, &saved); status != 200 || saved.Content != got.Content || saved.Name != "lock-method" {
						t.Fatalf("saved skill=%+v status=%d", saved, status)
					}
					revision := int64(1)
					if operation == "skill-update" {
						revision = 2
					}
					if saved.Revision != revision {
						t.Fatalf("revision=%d want=%d", saved.Revision, revision)
					}
				}
				return
			case "mcp-create", "mcp-update", "mcp-delete":
				var original, got mcpView
				config := map[string]any{"url": "https://example.org/mcp"}
				if operation != "mcp-create" {
					if status := s.call(t, "POST", "/api/mcp-servers", token, ws, mcpInput("lock-server", 0, config), &original); status != 201 {
						t.Fatal(status)
					}
				}
				method, path, body := "POST", "/api/mcp-servers", any(mcpInput("lock-server", 0, config))
				want, eventType := 201, "connector.created"
				if operation == "mcp-update" {
					method, path, body, want, eventType = "PUT", path+"/"+original.ID, mcpInput("renamed-server", 1, config), 200, "connector.updated"
				}
				if operation == "mcp-delete" {
					method, path, body, want, eventType = "DELETE", path+"/"+original.ID, map[string]any{"revision": 1}, 204, "connector.deleted"
				}
				libraryMutationWithEventWriter(t, s, ws, func() int {
					if method == "DELETE" {
						return s.call(t, method, path, token, ws, body, nil)
					}
					return s.call(t, method, path, token, ws, body, &got)
				}, want)
				if n := s.count(t, "SELECT count(*) FROM event WHERE workspace_id=$1 AND type=$2", ws, eventType); n != 1 {
					t.Fatalf("mutation events=%d", n)
				}
				if operation == "mcp-delete" {
					if n := s.count(t, "SELECT count(*) FROM workspace_mcp_server WHERE workspace_id=$1", ws); n != 0 {
						t.Fatalf("remaining servers=%d", n)
					}
				} else {
					name, revision := "lock-server", int64(1)
					if operation == "mcp-update" {
						name, revision = "renamed-server", 2
					}
					if got.Name != name || got.Revision != revision || s.count(t, "SELECT count(*) FROM workspace_mcp_server WHERE workspace_id=$1 AND id=$2 AND name=$3 AND revision=$4", ws, got.ID, name, revision) != 1 {
						t.Fatalf("saved server=%+v", got)
					}
				}
				return
			case "team-create":
				lead, aud, mem := s.newAgent(t, token, ws, "lead", "claude"), s.newAgent(t, token, ws, "aud", "codex"), s.newAgent(t, token, ws, "member", "claude")
				var got struct{ ID string }
				want = 201
				mutation = func() int { return s.call(t, "POST", "/api/teams", token, ws, formFor(lead, aud, mem), &got) }
				verify = func() {
					if got.ID == "" || s.count(t, "SELECT count(*) FROM field_pack WHERE workspace_id=$1 AND id=$2", ws, got.ID) != 1 {
						t.Fatalf("missing saved team: %+v", got)
					}
					if n := s.count(t, "SELECT count(*) FROM event WHERE workspace_id=$1 AND type='team.created' AND payload->>'pack_id'=$2", ws, got.ID); n != 1 {
						t.Fatalf("team events=%d", n)
					}
				}
			case "runtime-import":
				started := f.start(t, []string{"selected"})
				claimed := f.claim(t, "import")
				data := runtimeSkillZip(t, "lock-import")
				mutation = func() int { return f.upload(t, claimed, "selected", data) }
				verify = func() {
					if got := f.poll(t, started); got.Status != "completed" {
						t.Fatalf("import status=%s", got.Status)
					}
					if n := s.count(t, "SELECT count(*) FROM workspace_skill WHERE workspace_id=$1 AND name='lock-import' AND source_runtime_id=$2", ws, f.rt); n != 1 {
						t.Fatalf("imported skills=%d", n)
					}
					if n := s.count(t, "SELECT count(*) FROM event WHERE workspace_id=$1 AND type='skill.created'", ws); n != 1 {
						t.Fatalf("skill events=%d", n)
					}
					if n := s.count(t, "SELECT count(*) FROM event WHERE workspace_id=$1 AND type='runtime_skill_request.updated'", ws); n != 3 {
						t.Fatalf("job events=%d", n)
					}
				}
			}
			libraryMutationWithEventWriter(t, s, ws, mutation, want, operation == "runtime-import")
			verify()
		})
	}
}
