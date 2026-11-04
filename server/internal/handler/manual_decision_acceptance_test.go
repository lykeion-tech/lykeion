package handler

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"lykeion/server/internal/handin"
	"lykeion/server/internal/labmemory"
	"lykeion/server/internal/service"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

// This exercises real HTTP handlers and app-role SQL. The provider hand-in is
// synthetic: it validates integration without launching an agent CLI.
func TestManualDecisionCrossUserAcceptance(t *testing.T) {
	s := newServer(t)
	owner := s.signIn(t, "acceptance-owner@lab.org")
	ws := s.workspace(t, owner, "acceptance").ID
	creator := s.member(t, owner, ws, "acceptance-creator@lab.org", "member")
	other := s.member(t, owner, ws, "acceptance-other@lab.org", "member")
	admin := s.member(t, owner, ws, "acceptance-admin@lab.org", "admin")
	study := s.idleCard(t, other, ws, "Colleague study")
	who, workspace := uuid.MustParse(s.userID(t, creator)), uuid.MustParse(ws)
	linked := s.createManual(t, creator, ws, study.ID)
	unlinked := s.createManual(t, creator, ws, "")
	shared := s.seedDecision(t, ws, study.ID, s.userID(t, other), "Shared agent question")
	if _, err := s.Env.Owner.Exec(t.Context(), "UPDATE inbox_item SET kind='agent_decision',expires_at='2026-11-06T00:00:00Z' WHERE id=$1", shared); err != nil {
		t.Fatal(err)
	}

	var created struct{ Chat struct{ ID string } }
	if code := s.call(t, "POST", labChats, creator, ws, map[string]string{"purpose": "manual_decision_draft"}, &created); code != 201 {
		t.Fatal(code)
	}
	chatPath := labChats + "/" + created.Chat.ID
	draft := map[string]any{"title": "Private human draft", "context": "Private acceptance context", "deadline": nil, "card_id": study.ID}
	if code := s.call(t, "PUT", chatPath+"/draft", creator, ws, draft, nil); code != 200 {
		t.Fatal(code)
	}
	input := map[string]any{"user_request": "Refine my note", "current_draft": draft, "harness": "claude", "request_id": uuid.NewString()}
	if code := s.call(t, "POST", chatPath+"/questions", creator, ws, input, nil); code != 409 {
		t.Fatalf("offline ask: %d", code)
	}
	var saved struct{ Draft map[string]any }
	if code := s.call(t, "GET", chatPath+"/draft", creator, ws, nil, &saved); code != 200 || saved.Draft["title"] != draft["title"] {
		t.Fatalf("offline draft lost: %d %+v", code, saved)
	}
	runtime := csRegister(t, s, creator, ws, "acceptance-machine", "claude")
	var asked struct{ Message struct{ ID string } }
	if code := s.call(t, "POST", chatPath+"/questions", creator, ws, input, &asked); code != 202 {
		t.Fatal(code)
	}
	var run uuid.UUID
	if err := s.Env.Owner.QueryRow(t.Context(), "SELECT agent_run_id FROM lab_message WHERE id=$1", asked.Message.ID).Scan(&run); err != nil {
		t.Fatal(err)
	}
	claims, err := s.H.AgentRuns.Claim(t.Context(), who, []service.RuntimeRef{runtime}, 1)
	if err != nil || len(claims) != 1 {
		t.Fatal(err, claims)
	}
	runtime.Attempt = claims[0].AgentRun.Attempt
	if _, err = s.H.AgentRuns.StartLab(t.Context(), who, runtime, run, labmemory.LaunchPrompt); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"message": "A proposal for your review", "draft": map[string]any{"title": "Private AI proposal", "context": "Private acceptance context", "deadline": nil, "card_id": study.ID}, "statements": []any{}})
	h := handin.HandIns{Runs: s.H.AgentRuns, Aristotle: labmemory.Answers{Store: s.H.Store}}
	if problems, err := h.Submit(t.Context(), who, runtime, run, raw); err != nil || len(problems) > 0 {
		t.Fatal(err, problems)
	}
	if n := s.count(t, "SELECT count(*) FROM inbox_item WHERE workspace_id=$1 AND kind='manual_decision'", ws); n != 2 {
		t.Fatalf("AI created a note: %d", n)
	}
	draft["title"] = "Private reviewed AI note"
	if code := s.call(t, "PUT", chatPath+"/draft", creator, ws, draft, nil); code != 200 {
		t.Fatal(code)
	}
	var ai manualRow
	if code := s.call(t, "POST", "/api/decisions/manual", creator, ws, draft, &ai); code != 201 || ai.Title != draft["title"] {
		t.Fatal(code, ai)
	}
	if code := s.call(t, "POST", chatPath+"/archive", creator, ws, nil, nil); code != 204 {
		t.Fatal(code)
	}
	notes := []manualRow{linked, unlinked, ai}
	assertPrivacy := func() {
		t.Helper()
		for _, token := range []string{other, admin, owner} {
			for _, path := range []string{"/api/decisions", "/api/decisions?view=history", "/api/cards/" + study.ID + "/events?after=0", labChats + "?purpose=manual_decision_draft", labChats + "?purpose=manual_decision_draft&archived=true"} {
				var response json.RawMessage
				if code := s.call(t, "GET", path, token, ws, nil, &response); code != 200 {
					t.Fatalf("GET %s: %d", path, code)
				}
				for _, note := range notes {
					if strings.Contains(string(response), note.ID) || strings.Contains(string(response), "Private acceptance") {
						t.Fatalf("private data escaped %s: %s", path, response)
					}
				}
			}
			if code := s.call(t, "GET", chatPath+"/draft", token, ws, nil, nil); code != 404 {
				t.Fatalf("foreign draft: %d", code)
			}
			user := uuid.MustParse(s.userID(t, token))
			err := s.H.Store.InWorkspace(t.Context(), workspace, user, func(tx *store.Tx) error {
				events, e := tx.Q.EventsAfter(t.Context(), db.EventsAfterParams{WorkspaceID: workspace, After: 0, Lim: 500})
				if e != nil {
					return e
				}
				for _, note := range notes {
					for _, event := range events {
						if strings.Contains(string(event.Payload), note.ID) {
							t.Fatalf("private replay escaped: %s", event.Payload)
						}
					}
					_, e = labmemory.Read(t.Context(), tx, user, labmemory.Ref{Kind: "decision", ID: uuid.MustParse(note.ID)})
					if !errors.Is(e, labmemory.ErrNotFound) {
						t.Fatalf("foreign Aristotle note %s: %v", note.ID, e)
					}
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	assertPrivacy()
	if n := s.decisionCount(t, creator, ws); n != 3 {
		t.Fatalf("creator count %d", n)
	}
	if n := s.decisionCount(t, other, ws); n != 1 {
		t.Fatalf("colleague count %d", n)
	}
	if n := s.decisionCount(t, admin, ws); n != 1 {
		t.Fatalf("admin count %d", n)
	}
	for _, token := range []string{other, admin} {
		rows := s.decisions(t, token, ws, "")
		if len(rows) != 1 || rows[0].ID != shared || !rows[0].CanAnswer {
			t.Fatalf("shared question changed: %+v", rows)
		}
	}
	// Run the production maintenance path at the creator's calendar boundary.
	if _, err := s.Env.Owner.Exec(t.Context(), `UPDATE "user" SET timezone='Europe/Copenhagen' WHERE id=$1`, who); err != nil {
		t.Fatal(err)
	}
	if code := s.call(t, "PATCH", "/api/decisions/"+linked.ID+"/manual", creator, ws, map[string]any{"title": "Private reminder note", "card_id": study.ID, "deadline": "2026-11-05"}, nil); code != 200 {
		t.Fatal(code)
	}
	now := time.Date(2026, 11, 3, 23, 0, 0, 0, time.UTC)
	s.H.Engine.Now = func() time.Time { return now }
	for range 2 {
		if err := s.H.Engine.Tick(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if n := s.count(t, `SELECT count(*) FROM event WHERE type='decision.reminder' AND payload->>'inbox_item_id'=$1`, linked.ID); n != 1 {
		t.Fatalf("reminder count %d", n)
	}
	assertPrivacy()
	now = now.AddDate(0, 0, 4)
	if err := s.H.Engine.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if n := s.count(t, `SELECT count(*) FROM inbox_item WHERE id=$1 AND status='open' AND reminded_at IS NOT NULL`, linked.ID); n != 1 {
		t.Fatal("overdue note closed")
	}
	for i, action := range []string{"resolve", "dismiss"} {
		if code := s.call(t, "POST", "/api/decisions/"+notes[i].ID+"/"+action, creator, ws, nil, nil); code != 200 {
			t.Fatal(code)
		}
	}
	if rows := s.decisions(t, creator, ws, "?view=history"); len(rows) != 3 {
		t.Fatalf("creator terminal history: %+v", rows)
	}
	assertPrivacy()
	if n := s.count(t, `SELECT count(*) FROM inbox_item WHERE id=$1 AND status='expired'`, shared); n != 1 {
		t.Fatal("ordinary question did not expire")
	}
	if code := s.call(t, "POST", "/api/cards/"+study.ID+"/stop", other, ws, map[string]string{"reason": "Acceptance finished"}, nil); code != 200 {
		t.Fatalf("stop: %d", code)
	}
	if n := s.count(t, `SELECT count(*) FROM inbox_item WHERE id=$1 AND status='open'`, ai.ID); n != 1 {
		t.Fatal("study stop closed private note")
	}
	assertPrivacy()
	// Private replay is still available to its creator without Redis wakeups.
	err = s.H.Store.InWorkspace(t.Context(), workspace, who, func(tx *store.Tx) error {
		events, e := tx.Q.EventsAfter(t.Context(), db.EventsAfterParams{WorkspaceID: workspace, After: 0, Lim: 500})
		if e != nil {
			return e
		}
		found := false
		for _, event := range events {
			if event.Type == "decision.reminder" && strings.Contains(string(event.Payload), linked.ID) {
				found = true
			}
		}
		if !found {
			t.Fatal("creator reminder absent from durable replay")
		}
		_, e = labmemory.Read(t.Context(), tx, who, labmemory.Ref{Kind: "decision", ID: uuid.MustParse(ai.ID)})
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
}
