package handler

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"lykeion/server/internal/jev"
	db "lykeion/server/pkg/db/generated"
	"strings"
	"testing"
)

func TestPeopleSteerButNeverMoveForward(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	var c card
	s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "q", "budget_usd": 1}, &c)
	s.Env.Owner.Exec(t.Context(), `UPDATE card SET board_column = 'validation', round = 3 WHERE id = $1`, c.ID)

	if code := s.call(t, "POST", "/api/cards/"+c.ID+"/send-back", a, w.ID, map[string]string{"to": "reporting", "reason": "x"}, nil); code != 400 {
		t.Fatalf("sent forward: %d", code)
	}
	var out struct {
		BoardColumn string   `json:"board_column"`
		Stale       []string `json:"stale"`
		Round       int      `json:"round"`
		Flag        string   `json:"flag"`
		Ending      string   `json:"ending"`
		BudgetUsd   float64  `json:"budget_usd"`
	}
	if code := s.call(t, "POST", "/api/cards/"+c.ID+"/send-back", a, w.ID, map[string]string{"to": "hypotheses", "reason": "wrong rivals"}, &out); code != 200 {
		t.Fatalf("send-back: %d", code)
	}
	if out.BoardColumn != "hypotheses" || out.Round != 4 || len(out.Stale) != 2 {
		t.Fatalf("%+v", out)
	}
	s.Env.Owner.Exec(t.Context(), `UPDATE card SET flag = 'out_of_budget' WHERE id = $1`, c.ID)
	s.call(t, "POST", "/api/cards/"+c.ID+"/budget", a, w.ID, map[string]float64{"usd": 40}, &out)
	if out.Flag != "" || out.BudgetUsd != 40 {
		t.Fatalf("budget: %+v", out)
	}
	s.call(t, "POST", "/api/cards/"+c.ID+"/stop", a, w.ID, map[string]string{"reason": "superseded by #2"}, &out)
	if out.BoardColumn != "done" || out.Ending != "stopped" {
		t.Fatalf("stop: %+v", out)
	}
	if code := s.call(t, "POST", "/api/cards/"+c.ID+"/resume", a, w.ID, nil, nil); code != 409 {
		t.Fatalf("resumed an ended card: %d", code)
	}
}

func TestResumeRefusesABlockedCard(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	var c card
	s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "q"}, &c)
	s.Env.Owner.Exec(t.Context(), `UPDATE card SET board_column = 'validation', round = 3, flag = 'escalated', block_kind = 'cap', route_backs = 4 WHERE id = $1`, c.ID)
	var refused struct {
		Error string `json:"error"`
	}
	if code := s.call(t, "POST", "/api/cards/"+c.ID+"/resume", a, w.ID, nil, &refused); code != 409 || refused.Error != "this study is blocked: use unblock" {
		t.Fatalf("resumed a blocked card: %d %+v", code, refused)
	}
	var flag, kind string
	var round, routeBacks int
	s.Env.Owner.QueryRow(t.Context(), `SELECT flag, block_kind, round, route_backs FROM card WHERE id = $1`, c.ID).Scan(&flag, &kind, &round, &routeBacks)
	if flag != "escalated" || kind != "cap" || round != 3 || routeBacks != 4 {
		t.Fatalf("a refused resume changed the card: %s %s %d %d", flag, kind, round, routeBacks)
	}
	s.Env.Owner.Exec(t.Context(), `UPDATE card SET block_kind = '' WHERE id = $1`, c.ID)
	if code := s.call(t, "POST", "/api/cards/"+c.ID+"/resume", a, w.ID, nil, nil); code != 200 {
		t.Fatalf("an escalated card without a block kind did not resume: %d", code)
	}
}

func TestSendBackRefusesABlockedCard(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	var c card
	s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "q"}, &c)
	s.Env.Owner.Exec(t.Context(), `UPDATE card SET board_column = 'validation', round = 3, flag = 'escalated', block_kind = 'cap', route_backs = 4 WHERE id = $1`, c.ID)
	var refused struct {
		Error string `json:"error"`
	}
	if code := s.call(t, "POST", "/api/cards/"+c.ID+"/send-back", a, w.ID, map[string]string{"to": "grounding", "reason": "redo"}, &refused); code != 409 || refused.Error != "this study is blocked: unblock or close it first" {
		t.Fatalf("sent back a blocked card: %d %+v", code, refused)
	}
	var column, flag, kind string
	var round, routeBacks int
	s.Env.Owner.QueryRow(t.Context(), `SELECT board_column, flag, block_kind, round, route_backs FROM card WHERE id = $1`, c.ID).Scan(&column, &flag, &kind, &round, &routeBacks)
	if column != "validation" || flag != "escalated" || kind != "cap" || round != 3 || routeBacks != 4 {
		t.Fatalf("a refused send-back changed the card: %s %s %s %d %d", column, flag, kind, round, routeBacks)
	}
}

func TestSendBackReasonIsTheHandoffNote(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	var c card
	s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "q"}, &c)
	s.Env.Owner.Exec(t.Context(), `UPDATE card SET board_column = 'hypotheses', round = 2 WHERE id = $1`, c.ID)
	var out struct {
		HandoffNote string `json:"handoff_note"`
		Rerun       string `json:"rerun"`
	}
	s.call(t, "POST", "/api/cards/"+c.ID+"/send-back", a, w.ID, map[string]string{"to": "grounding", "step": "contrarian", "reason": "missed the null result"}, &out)
	if out.HandoffNote != "missed the null result" || out.Rerun != "contrarian" {
		t.Fatalf("%+v", out)
	}
}

func TestDuplicateCards(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	var first card
	s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "Does X raise Y?"}, &first)

	var dup struct {
		Error  string `json:"error"`
		CardID string `json:"card_id"`
		Number int    `json:"number"`
	}
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "  does x\n raise   Y? "}, &dup); code != 409 {
		t.Fatalf("duplicate question: %d", code)
	}
	if dup.CardID != first.ID || dup.Number != first.Number || !strings.Contains(dup.Error, "#1") {
		t.Fatalf("%+v", dup)
	}
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"title": "DOES X RAISE Y?", "question": "other words"}, nil); code != 409 {
		t.Fatalf("duplicate title: %d", code)
	}
	var follow card
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "Does X raise Y?", "follows_from": []string{first.ID}}, &follow); code != 201 {
		t.Fatalf("a follow-up was refused: %d", code)
	}

	// two identical cards at once: exactly one is created
	codes := make(chan int, 2)
	for range 2 {
		go func() {
			codes <- s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "Is Z lower in mice?"}, nil)
		}()
	}
	if c1, c2 := <-codes, <-codes; c1+c2 != 201+409 {
		t.Fatalf("concurrent creates: %d %d", c1, c2)
	}

	var out struct {
		Ending    string `json:"ending"`
		EndReason string `json:"end_reason"`
	}
	if code := s.call(t, "POST", "/api/cards/"+follow.ID+"/stop", a, w.ID, map[string]any{"duplicate_of": follow.Number}, nil); code != 400 {
		t.Fatalf("a card was stopped as a duplicate of itself: %d", code)
	}
	s.call(t, "POST", "/api/cards/"+follow.ID+"/stop", a, w.ID, map[string]any{"duplicate_of": first.Number}, &out)
	if out.Ending != "stopped" || out.EndReason != "duplicate of #1" {
		t.Fatalf("%+v", out)
	}
}

func TestSettingsAreOwnerOnly(t *testing.T) {
	s := newServer(t)
	a, b := s.signIn(t, "a@lab.org"), s.signIn(t, "b@lab.org")
	w := s.workspace(t, a, "lab")
	s.call(t, "POST", "/api/invitations/"+s.invite(t, a, w.ID, "b@lab.org", "admin")+"/accept", b, "", nil, nil)
	body := map[string]any{"settings": map[string]any{"jev": map[string]any{"enabled": true}}}
	if code := s.call(t, "PATCH", "/api/workspaces/"+w.ID+"/settings", b, "", body, nil); code != 403 {
		t.Fatalf("an admin turned on text egress: %d", code)
	}
	if code := s.call(t, "PATCH", "/api/workspaces/"+w.ID+"/settings", a, "", body, nil); code != 204 {
		t.Fatalf("owner: %d", code)
	}
	other := map[string]any{"settings": map[string]any{"jev": map[string]any{"enabled": true, "model": "jev-1.14.0"}}}
	var rejected struct {
		Error string `json:"error"`
	}
	if code := s.call(t, "PATCH", "/api/workspaces/"+w.ID+"/settings", a, "", other, &rejected); code != 400 {
		t.Fatalf("another Jev model was accepted: %d", code)
	}
	if !strings.Contains(rejected.Error, "pinned to "+jev.Model) {
		t.Fatalf("pin explanation: %q", rejected.Error)
	}
}

func TestSettingsAcceptPinnedModel(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	body := map[string]any{"settings": map[string]any{"jev": map[string]any{"model": jev.Model}}}
	if code := s.call(t, "PATCH", "/api/workspaces/"+w.ID+"/settings", a, "", body, nil); code != 204 {
		t.Fatalf("matching model: %d", code)
	}
}

func TestSteeringCancellationFailureRemainsVisible(t *testing.T) {
	for _, action := range []string{"stop", "send-back"} {
		t.Run(action, func(t *testing.T) {
			s := newServer(t)
			s.H.Engine = nil
			s.H.AgentRuns.OnSettled = nil
			a := s.signIn(t, "a@lab.org")
			w := s.workspace(t, a, "lab")
			var c card
			if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "q"}, &c); code != 201 {
				t.Fatal(code)
			}
			s.queue(t, w.ID, c.ID, s.userID(t, a), "claude")
			s.queue(t, w.ID, c.ID, s.userID(t, a), "codex")
			var failedRun, laterRun string
			if err := s.Env.Owner.QueryRow(t.Context(), `SELECT id FROM agent_run WHERE card_id=$1 AND harness='claude'`, c.ID).Scan(&failedRun); err != nil {
				t.Fatal(err)
			}
			if err := s.Env.Owner.QueryRow(t.Context(), `SELECT id FROM agent_run WHERE card_id=$1 AND harness='codex'`, c.ID).Scan(&laterRun); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Env.Owner.Exec(t.Context(), `UPDATE card SET board_column='grounding', round=1 WHERE id=$1`, c.ID); err != nil {
				t.Fatal(err)
			}
			if action == "send-back" {
				if _, err := s.Env.Owner.Exec(t.Context(), `UPDATE card SET board_column='hypotheses' WHERE id=$1`, c.ID); err != nil {
					t.Fatal(err)
				}
				if _, err := s.Env.Owner.Exec(t.Context(), `UPDATE agent_run SET board_column='hypotheses' WHERE card_id=$1`, c.ID); err != nil {
					t.Fatal(err)
				}
			}
			// Fail the first cancellation inside its real transaction; the later run
			// must still be cancelled after this transaction rolls back.
			if _, err := s.Env.Owner.Exec(t.Context(), fmt.Sprintf(`CREATE FUNCTION reject_cancel() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
    IF NEW.id='%s'::uuid AND NEW.status='cancelled' THEN RAISE EXCEPTION 'forced cancellation failure'; END IF;
    RETURN NEW; END $$;
    CREATE TRIGGER reject_cancel BEFORE UPDATE ON agent_run FOR EACH ROW EXECUTE FUNCTION reject_cancel();`, failedRun)); err != nil {
				t.Fatal(err)
			}
			var logs bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
			defer slog.SetDefault(previous)
			body := map[string]string{"reason": "reconsider"}
			if action == "send-back" {
				body["to"] = "grounding"
			}
			var response struct {
				Error string `json:"error"`
			}
			code := s.call(t, "POST", "/api/cards/"+c.ID+"/"+action, a, w.ID, body, &response)
			var column, ending, firstStatus, lastStatus string
			if err := s.Env.Owner.QueryRow(t.Context(), `SELECT board_column,ending FROM card WHERE id=$1`, c.ID).Scan(&column, &ending); err != nil {
				t.Fatal(err)
			}
			if err := s.Env.Owner.QueryRow(t.Context(), `SELECT status FROM agent_run WHERE id=$1`, failedRun).Scan(&firstStatus); err != nil {
				t.Fatal(err)
			}
			if err := s.Env.Owner.QueryRow(t.Context(), `SELECT status FROM agent_run WHERE id=$1`, laterRun).Scan(&lastStatus); err != nil {
				t.Fatal(err)
			}
			if code != 500 || !strings.Contains(response.Error, "steering was applied") || !strings.Contains(response.Error, "cancellation incomplete") {
				t.Errorf("partial failure: code=%d response=%+v", code, response)
			}
			if action == "stop" && (column != "done" || ending != "stopped") {
				t.Errorf("steering rolled back: %s %s", column, ending)
			}
			if action == "send-back" && column != "grounding" {
				t.Errorf("send-back rolled back: %s", column)
			}
			if firstStatus != "queued" || lastStatus != "cancelled" {
				t.Errorf("cancellation attempts: first=%s later=%s", firstStatus, lastStatus)
			}
			if !strings.Contains(logs.String(), c.ID) || !strings.Contains(logs.String(), failedRun) || !strings.Contains(logs.String(), "forced cancellation failure") {
				t.Errorf("missing cancellation identities: %s", logs.String())
			}
		})
	}
}

func TestSteeringAlreadyTerminalCancellationIsBenign(t *testing.T) {
	s := newServer(t)
	s.H.Engine = nil
	s.H.AgentRuns.OnSettled = nil
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	var c card
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "q"}, &c); code != 201 {
		t.Fatal(code)
	}
	s.queue(t, w.ID, c.ID, s.userID(t, a), "claude")
	if _, err := s.Env.Owner.Exec(t.Context(), `UPDATE card SET board_column='grounding', round=1 WHERE id=$1`, c.ID); err != nil {
		t.Fatal(err)
	}
	previous := s.H.Store.Publish
	s.H.Store.Publish = func(ctx context.Context, events []db.Event) {
		for _, event := range events {
			if event.Type == "card.steered" {
				if _, err := s.Env.Owner.Exec(ctx, `UPDATE agent_run SET status='completed' WHERE card_id=$1`, c.ID); err != nil {
					t.Error(err)
				}
			}
		}
		if previous != nil {
			previous(ctx, events)
		}
	}
	if code := s.call(t, "POST", "/api/cards/"+c.ID+"/stop", a, w.ID, map[string]string{"reason": "finished"}, nil); code != 200 {
		t.Fatalf("already terminal cancellation: %d", code)
	}
	var status string
	if err := s.Env.Owner.QueryRow(t.Context(), `SELECT status FROM agent_run WHERE card_id=$1`, c.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "completed" {
		t.Fatalf("terminal result changed: %s", status)
	}
}

func TestDuplicateLookupInfrastructureFailureIsServerError(t *testing.T) {
	s := newServer(t)
	s.H.Engine = nil
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	var c card
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "q"}, &c); code != 201 {
		t.Fatal(code)
	}
	for _, number := range []int{c.Number, c.Number + 1} {
		if code := s.call(t, "POST", "/api/cards/"+c.ID+"/stop", a, w.ID, map[string]any{"duplicate_of": number}, nil); code != 400 {
			t.Fatalf("invalid duplicate %d: %d", number, code)
		}
	}
	// The RLS guard raises only during the numbered lookup, leaving the card
	// lock and membership check real and successful in the same transaction.
	if _, err := s.Env.Owner.Exec(t.Context(), `CREATE FUNCTION reject_numbered_lookup() RETURNS boolean LANGUAGE plpgsql AS $$ BEGIN
  IF current_query() LIKE '%GetCardByNumber%' THEN RAISE EXCEPTION 'forced numbered lookup failure'; END IF;
  RETURN true; END $$;
  CREATE POLICY reject_numbered_lookup ON card AS RESTRICTIVE FOR SELECT TO lykeion_app USING (reject_numbered_lookup());`); err != nil {
		t.Fatal(err)
	}
	if code := s.call(t, "POST", "/api/cards/"+c.ID+"/stop", a, w.ID, map[string]any{"duplicate_of": c.Number}, nil); code != 500 {
		t.Fatalf("database failure misclassified: %d", code)
	}
	var ending string
	if err := s.Env.Owner.QueryRow(t.Context(), `SELECT ending FROM card WHERE id=$1`, c.ID).Scan(&ending); err != nil {
		t.Fatal(err)
	}
	if ending != "" {
		t.Fatalf("failed lookup changed the card: %s", ending)
	}
}

func TestExplicitSteeringCancelsInflightRunsAcrossRounds(t *testing.T) {
	for _, action := range []string{"stop", "send-back"} {
		t.Run(action, func(t *testing.T) {
			s := newServer(t)
			s.H.Engine = nil
			s.H.AgentRuns.OnSettled = nil
			a := s.signIn(t, "steer@lab.org")
			w := s.workspace(t, a, "lab")
			var c card
			if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "q"}, &c); code != 201 {
				t.Fatal(code)
			}
			for _, harness := range []string{"claude", "codex", "claude", "codex"} {
				s.queue(t, w.ID, c.ID, s.userID(t, a), harness)
			}
			if _, err := s.Env.Owner.Exec(t.Context(), `WITH card_update AS (UPDATE card SET board_column='hypotheses', round=2 WHERE id=$1 RETURNING id), numbered AS (SELECT id,row_number() OVER (ORDER BY created_at,id) AS n FROM agent_run WHERE card_id=$1) UPDATE agent_run SET status=CASE numbered.n WHEN 1 THEN 'running' WHEN 2 THEN 'queued' WHEN 3 THEN 'dispatched' ELSE 'completed' END, round=CASE WHEN numbered.n=3 THEN 2 ELSE 1 END, board_column=CASE WHEN numbered.n=3 THEN 'hypotheses' ELSE 'grounding' END FROM numbered WHERE agent_run.id=numbered.id`, c.ID); err != nil {
				t.Fatal(err)
			}
			committed := false
			previous := s.H.Store.Publish
			s.H.Store.Publish = func(ctx context.Context, events []db.Event) {
				for _, event := range events {
					if event.Type == "card.steered" {
						committed = true
						var active int
						if err := s.Env.Owner.QueryRow(ctx, `SELECT count(*) FROM agent_run WHERE card_id=$1 AND status IN ('running','dispatched','queued')`, c.ID).Scan(&active); err != nil {
							t.Error(err)
						}
						if active != 3 {
							t.Errorf("runs cancelled before steering commit: %d active", active)
						}
					}
				}
				if previous != nil {
					previous(ctx, events)
				}
			}
			body := map[string]string{"reason": "reconsider"}
			if action == "send-back" {
				body["to"] = "grounding"
			}
			if code := s.call(t, "POST", "/api/cards/"+c.ID+"/"+action, a, w.ID, body, nil); code != 200 {
				t.Fatalf("steering: %d", code)
			}
			if !committed {
				t.Fatal("steering event was not committed")
			}
			var cancelled, completed int
			if err := s.Env.Owner.QueryRow(t.Context(), `SELECT count(*) FILTER (WHERE status='cancelled'),count(*) FILTER (WHERE status='completed') FROM agent_run WHERE card_id=$1`, c.ID).Scan(&cancelled, &completed); err != nil {
				t.Fatal(err)
			}
			if cancelled != 3 || completed != 1 {
				t.Fatalf("all-round cancellation: cancelled=%d completed=%d", cancelled, completed)
			}
		})
	}
}
