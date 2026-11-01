package handler

import (
	"context"
	"testing"
	"time"
)

func TestDeleteCustomTeamAndItsAgentCopies(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	a := s.signIn(t, "team-delete@lab.org")
	w := s.workspace(t, a, "lab")
	id := s.addedTeam(t, a, w.ID, "disposable", "grounding")
	var agent string
	if err := s.Env.Owner.QueryRow(t.Context(), `SELECT agent_id FROM team_agent WHERE pack_id = $1 LIMIT 1`, id).Scan(&agent); err != nil {
		t.Fatal(err)
	}
	if code := s.call(t, "DELETE", "/api/teams/"+id, a, w.ID, nil, nil); code != 200 {
		t.Fatalf("delete Team: %d", code)
	}
	if code := s.call(t, "GET", "/api/teams/"+id, a, w.ID, nil, nil); code != 404 {
		t.Fatalf("deleted Team visible: %d", code)
	}
	if code := s.call(t, "GET", "/api/agents/"+agent, a, w.ID, nil, nil); code != 404 {
		t.Fatalf("deleted Team agent visible: %d", code)
	}
}

func TestDeleteTeamBlockedWhenInUse(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	a := s.signIn(t, "team-used@lab.org")
	w := s.workspace(t, a, "lab")
	id := s.addedTeam(t, a, w.ID, "used", "grounding")
	validation := s.addedTeam(t, a, w.ID, "validator", "validation")
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "q", "assignee": teamAssignee(map[string]string{"grounding": id, "validation": validation})}, nil); code != 201 {
		t.Fatalf("study: %d", code)
	}
	if code := s.call(t, "DELETE", "/api/teams/"+id, a, w.ID, nil, nil); code != 409 {
		t.Fatalf("delete used Team: %d", code)
	}
}

func TestTeamCopySoloStudyBlocksTeamDeletion(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	a := s.signIn(t, "team-copy-solo@lab.org")
	w := s.workspace(t, a, "lab")
	id := s.addedTeam(t, a, w.ID, "solo-copy", "grounding")
	var agent string
	if err := s.Env.Owner.QueryRow(t.Context(), `SELECT agent_id FROM team_agent WHERE pack_id = $1 LIMIT 1`, id).Scan(&agent); err != nil {
		t.Fatal(err)
	}
	s.startStudy(t, a, w.ID, agent, "solo question")
	if code := s.call(t, "DELETE", "/api/teams/"+id, a, w.ID, nil, nil); code != 409 {
		t.Fatalf("delete Team with copy working solo: %d", code)
	}
}

func TestDeleteBuiltInTeamForbidden(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	a := s.signIn(t, "builtin-delete@lab.org")
	w := s.workspace(t, a, "lab")
	id := s.builtInID(t, w.ID, "grounding")
	if code := s.call(t, "DELETE", "/api/teams/"+id, a, w.ID, nil, nil); code != 403 {
		t.Fatalf("delete built-in Team: %d", code)
	}
}

func TestDeleteTeamBlockedByAutoresearchAndActiveAgentRun(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	s.H.Engine = nil
	a := s.signIn(t, "team-uses@lab.org")
	w := s.workspace(t, a, "lab")
	auto := s.addedTeam(t, a, w.ID, "scheduled", "grounding")
	s.autoresearch(t, a, w.ID, map[string]any{"title": "Digest", "execution_mode": "create_card", "assignee": teamAssignee(map[string]string{"grounding": auto})})
	if code := s.call(t, "DELETE", "/api/teams/"+auto, a, w.ID, nil, nil); code != 409 {
		t.Fatalf("delete Autoresearch Team: %d", code)
	}
	id := s.addedTeam(t, a, w.ID, "running", "grounding")
	var agent string
	if err := s.Env.Owner.QueryRow(t.Context(), `SELECT agent_id FROM team_agent WHERE pack_id = $1 LIMIT 1`, id).Scan(&agent); err != nil {
		t.Fatal(err)
	}
	c := s.idleCard(t, a, w.ID, "run")
	s.mustUpdate(t, `INSERT INTO agent_run(workspace_id, card_id, responsible_user_id, board_column, round, role, harness, prompt, status, agent_id) SELECT workspace_id, id, owner_id, 'grounding', 1, 'leader', 'claude', 'test', 'queued', $2 FROM card WHERE id = $1`, c.ID, agent)
	if code := s.call(t, "DELETE", "/api/teams/"+id, a, w.ID, nil, nil); code != 409 {
		t.Fatalf("delete Team with active agent run: %d", code)
	}
}

func TestDeleteTeamRequiresVisibility(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	a := s.signIn(t, "team-owner@lab.org")
	w := s.workspace(t, a, "lab")
	b := s.member(t, a, w.ID, "team-other@lab.org", "member")
	id := s.addedTeam(t, a, w.ID, "private", "grounding")
	if code := s.call(t, "DELETE", "/api/teams/"+id, b, w.ID, nil, nil); code != 404 {
		t.Fatalf("delete private Team: %d", code)
	}
}

func TestDeleteTeamPreservesFinishedStudyAndAgentHistory(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	s.H.Engine = nil
	a := s.signIn(t, "history-team@lab.org")
	w := s.workspace(t, a, "lab")
	id := s.addedTeam(t, a, w.ID, "history", "grounding")
	validation := s.addedTeam(t, a, w.ID, "history-validator", "validation")
	var c studyJSON
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "q", "assignee": teamAssignee(map[string]string{"grounding": id, "validation": validation})}, &c); code != 201 {
		t.Fatalf("study: %d", code)
	}
	var agent, name string
	if err := s.Env.Owner.QueryRow(t.Context(), `SELECT a.id, a.name FROM agent a JOIN team_agent t ON t.agent_id = a.id WHERE t.pack_id = $1 LIMIT 1`, id).Scan(&agent, &name); err != nil {
		t.Fatal(err)
	}
	s.mustUpdate(t, `UPDATE card SET state = 'done', machine = 'history-daemon' WHERE id = $1`, c.ID)
	s.mustUpdate(t, `INSERT INTO runtime(workspace_id, owner_id, daemon_id, harness) VALUES($1, $2, 'history-daemon', 'claude')`, w.ID, s.userID(t, a))
	s.mustUpdate(t, `INSERT INTO agent_run(workspace_id, card_id, responsible_user_id, board_column, round, role, harness, prompt, status, agent_id) SELECT workspace_id, id, owner_id, 'grounding', 1, 'leader', 'claude', 'test', 'completed', $2 FROM card WHERE id = $1`, c.ID, agent)
	if code := s.call(t, "DELETE", "/api/teams/"+id, a, w.ID, nil, nil); code != 200 {
		t.Fatalf("delete Team: %d", code)
	}
	var study map[string]any
	if code := s.call(t, "GET", "/api/cards/"+c.ID, a, w.ID, nil, &study); code != 200 {
		t.Fatalf("historical study: %d", code)
	}
	var runs []map[string]any
	if code := s.call(t, "GET", "/api/cards/"+c.ID+"/runs", a, w.ID, nil, &runs); code != 200 || len(runs) == 0 || runs[0]["agent_name"] != name {
		t.Fatalf("historical runs: %d %+v", code, runs)
	}
}

func TestTeamAddAndDuplicateWaitForDeletion(t *testing.T) {
	for _, action := range []string{"add", "duplicate"} {
		t.Run(action, func(t *testing.T) {
			s := newServer(t)
			s.builtIns(t)
			a := s.signIn(t, "race-"+action+"@lab.org")
			w := s.workspace(t, a, "lab")
			id := s.addedTeam(t, a, w.ID, "race", "grounding")
			tx, err := s.Env.Owner.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(context.Background())
			if _, err := tx.Exec(t.Context(), `SELECT pg_advisory_xact_lock(hashtextextended($1::uuid::text, 0))`, w.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(t.Context(), `UPDATE field_pack SET deleted_at = now() WHERE id = $1`, id); err != nil {
				t.Fatal(err)
			}
			done := make(chan int, 1)
			go func() {
				done <- s.call(t, "POST", "/api/teams/"+id+"/"+action, a, w.ID, map[string]string{"name": "copy"}, nil)
			}()
			select {
			case code := <-done:
				t.Fatalf("%s returned %d before deletion committed", action, code)
			case <-time.After(500 * time.Millisecond):
			}
			if err := tx.Commit(t.Context()); err != nil {
				t.Fatal(err)
			}
			select {
			case code := <-done:
				if code != 404 {
					t.Fatalf("%s after deletion: %d", action, code)
				}
			case <-time.After(10 * time.Second):
				t.Fatalf("%s did not finish", action)
			}
		})
	}
}
