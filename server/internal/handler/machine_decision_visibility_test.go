package handler

import "testing"

func TestMachineNoticesStayOutOfDecisions(t *testing.T) {
	s := newServer(t)
	s.H.Engine = nil
	owner := s.signIn(t, "machine-notices@lab.org")
	w := s.workspace(t, owner, "lab")
	card := s.idleCard(t, owner, w.ID, "study")
	user := s.userID(t, owner)
	research := s.seedDecision(t, w.ID, card.ID, user, "A research question")
	note := s.createManual(t, owner, w.ID, "")
	var open, history string
	if err := s.Env.Owner.QueryRow(t.Context(), `INSERT INTO inbox_item (workspace_id, user_id, kind, text)
		VALUES ($1, $2, 'waiting_for_machine', 'Codex is offline') RETURNING id`, w.ID, user).Scan(&open); err != nil {
		t.Fatal(err)
	}
	if err := s.Env.Owner.QueryRow(t.Context(), `INSERT INTO inbox_item (workspace_id, user_id, kind, text, status, resolved_at)
		VALUES ($1, $2, 'waiting_for_machine', 'Previously offline', 'dismissed', now()) RETURNING id`, w.ID, user).Scan(&history); err != nil {
		t.Fatal(err)
	}
	var actionable string
	if err := s.Env.Owner.QueryRow(t.Context(), `INSERT INTO inbox_item (workspace_id, user_id, kind, text, options)
		VALUES ($1, $2, 'waiting_for_machine', 'Existing actionable item', $3::jsonb) RETURNING id`, w.ID, user, stopOptions).Scan(&actionable); err != nil {
		t.Fatal(err)
	}
	rows := s.decisions(t, owner, w.ID, "")
	ids := map[string]bool{}
	for _, row := range rows {
		ids[row.ID] = true
	}
	if len(rows) != 3 || !ids[research] || !ids[note.ID] || !ids[actionable] || ids[open] {
		t.Fatalf("open decisions: %+v", rows)
	}
	if rows := s.decisions(t, owner, w.ID, "?view=history"); len(rows) != 0 {
		t.Fatalf("historical machine notice in decisions: %+v", rows)
	}
	if n := s.decisionCount(t, owner, w.ID); n != 3 {
		t.Fatalf("decision count = %d, want 3", n)
	}
	if code := s.call(t, "POST", "/api/decisions/"+open+"/read", owner, w.ID, nil, nil); code != 204 {
		t.Fatalf("legacy machine notice read failed: %d", code)
	}
	var count int
	if err := s.Env.Owner.QueryRow(t.Context(), `SELECT count(*) FROM inbox_item WHERE id IN ($1, $2)`, open, history).Scan(&count); err != nil || count != 2 {
		t.Fatalf("underlying notices changed: %d, %v", count, err)
	}
}
