package handler

import (
	"context"
	"encoding/json"
	"testing"
)

type listedRun struct {
	ID             string
	Role           string
	AgentID        *string `json:"agent_id"`
	AgentName      *string `json:"agent_name"`
	Assignment     string
	LatestText     string          `json:"latest_text"`
	Answer         string          `json:"answer"`
	HandIn         json.RawMessage `json:"hand_in"`
	LastActivityAt *string         `json:"last_activity_at"`
}

func (l *teamLab) runs(t *testing.T) map[string]listedRun {
	t.Helper()
	var runs []listedRun
	if c := l.s.call(t, "GET", "/api/cards/"+l.card.ID+"/runs", l.jwt, l.ws, nil, &runs); c != 200 {
		t.Fatalf("runs %d", c)
	}
	out := map[string]listedRun{}
	for _, r := range runs {
		out[r.ID] = r
	}
	return out
}

func (l *teamLab) messages(t *testing.T, r teamRun, ms ...map[string]any) {
	t.Helper()
	body := map[string]any{"messages": ms}
	for k, v := range r.ref {
		body[k] = v
	}
	if c := l.s.call(t, "POST", "/api/daemon/agent-runs/"+r.id+"/messages", l.personal, "", body, nil); c != 204 {
		t.Fatalf("messages %d", c)
	}
}

// Each run names the agent it was started as and what it was asked to do,
// without the prompt's framing, and previews its latest text.
func TestStudyRunsNameTheirAgentAssignmentAndLatestText(t *testing.T) {
	l := newTeamLab(t, 0)
	leader := l.claim(t)
	got := l.runs(t)[leader.id]
	if got.AgentID == nil || got.AgentName == nil || *got.AgentName == "" {
		t.Fatalf("leader names no agent: %+v", got)
	}
	if want := l.s.count(t, "SELECT count(*) FROM agent WHERE id = $1 AND name = $2", *got.AgentID, *got.AgentName); want != 1 {
		t.Fatalf("agent %s is not named %q", *got.AgentID, *got.AgentName)
	}
	if got.Assignment != "Why do cells age?" {
		t.Fatalf("leader assignment %q", got.Assignment)
	}
	if got.LatestText != "" || got.LastActivityAt != nil {
		t.Fatalf("a run with no messages previews %+v", got)
	}

	l.messages(t, leader,
		map[string]any{"seq": 1, "type": "text", "content": "Planning the rivals."},
		map[string]any{"seq": 2, "type": "tool_use", "tool": "Bash", "content": map[string]any{"id": "t1", "input": map[string]any{"command": "lyk records"}}},
		map[string]any{"seq": 3, "type": "text", "content": "Delegating the first step."},
		map[string]any{"seq": 4, "type": "tool_result", "content": map[string]any{"tool_use_id": "t1", "content": "none"}},
	)
	got = l.runs(t)[leader.id]
	if got.LatestText != "Delegating the first step." || got.LastActivityAt == nil {
		t.Fatalf("preview %+v", got)
	}

	c, out := l.step(t, leader.token, map[string]any{"member": "member-1", "content": "List three rival explanations."})
	if c != 201 {
		t.Fatalf("step %d %+v", c, out)
	}
	member := l.runs(t)[out.AgentRunID]
	if member.Role != "member-1" || member.AgentName == nil || *member.AgentName == *got.AgentName {
		t.Fatalf("member run %+v, leader agent %q", member, *got.AgentName)
	}
	if member.Assignment != "List three rival explanations." {
		t.Fatalf("member assignment %q", member.Assignment)
	}
}

// Historical runs retain their recorded text-piece preview behavior.
func TestHistoricalHarnessPreviewJoinsItsTextPieces(t *testing.T) {
	l := newTeamLab(t, 0)
	leader := l.claim(t)
	if _, err := l.s.Env.Owner.Exec(context.Background(), "UPDATE agent_run SET harness = 'antigravity' WHERE id = $1", leader.id); err != nil {
		t.Fatal(err)
	}
	l.messages(t, leader,
		map[string]any{"seq": 1, "type": "text", "content": "Earlier."},
		map[string]any{"seq": 2, "type": "tool_use", "tool": "run_command", "content": map[string]any{}},
		map[string]any{"seq": 3, "type": "text", "content": "Scoped"},
		map[string]any{"seq": 4, "type": "text", "content": " the question."},
	)
	if got := l.runs(t)[leader.id].LatestText; got != "Scoped the question." {
		t.Fatalf("preview %q", got)
	}
}

func TestDaemonReportsItsPlatform(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "platform@lab.org")
	w := s.workspace(t, a, "lab")
	tok := s.personalToken(t, a)
	platform := map[string]string{"os": "linux", "arch": "arm64", "kernel": "Linux 6.8.0", "language": "Python 3.12.3"}
	if code := s.call(t, "POST", "/api/daemon/register", tok, "", map[string]any{"workspace_id": w.ID, "daemon_id": "d1", "device_name": "laptop",
		"harnesses": []string{"claude"}, "platform": platform}, nil); code != 200 {
		t.Fatalf("register: %d", code)
	}
	if code := s.call(t, "POST", "/api/daemon/register", tok, "", map[string]any{"workspace_id": w.ID, "daemon_id": "d2", "device_name": "old",
		"harnesses": []string{"claude"}}, nil); code != 200 {
		t.Fatalf("register without platform: %d", code)
	}
	var rts []struct {
		DaemonID                   string `json:"daemon_id"`
		OS, Arch, Kernel, Language string
	}
	if code := s.call(t, "GET", "/api/runtimes", a, w.ID, nil, &rts); code != 200 || len(rts) != 2 {
		t.Fatalf("runtimes: %d %+v", code, rts)
	}
	for _, r := range rts {
		switch r.DaemonID {
		case "d1":
			if r.OS != "linux" || r.Arch != "arm64" || r.Kernel != "Linux 6.8.0" || r.Language != "Python 3.12.3" {
				t.Fatalf("reported platform %+v", r)
			}
		case "d2":
			if r.OS != "" || r.Arch != "" || r.Kernel != "" || r.Language != "" {
				t.Fatalf("an unreported platform was filled in: %+v", r)
			}
		}
	}
}

// A run that handed in lists its latest hand-in's answer; one that has not
// lists none.
func TestRunsListCarriesAnswer(t *testing.T) {
	l, leader := groundingLab(t)
	if got := l.runs(t)[leader.id]; got.Answer != "" {
		t.Fatalf("answer before hand-in %q", got.Answer)
	}
	l.record(t, leader, 0, "hand_in", "", map[string]any{"agent_run_id": leader.id, "attempt": 1, "hand_in": map[string]any{"answer": "Two papers fit."}}, &leader.id)
	if got := l.runs(t)[leader.id].Answer; got != "Two papers fit." {
		t.Fatalf("answer %q", got)
	}
}

func TestRunsListCarriesOnlyCurrentAttemptHandIn(t *testing.T) {
	l, leader := groundingLab(t)
	if got := l.runs(t)[leader.id]; len(got.HandIn) != 0 && string(got.HandIn) != "null" {
		t.Fatalf("unexpected hand-in %s", got.HandIn)
	}
	l.record(t, leader, 0, "hand_in", "", map[string]any{"agent_run_id": leader.id, "attempt": 1, "hand_in": map[string]any{"answer": "Found evidence.", "papers": []any{map[string]any{"id": "doi:10.1/example", "why": "relevant"}}}}, &leader.id)
	got := l.runs(t)[leader.id]
	var hand struct {
		Answer string `json:"answer"`
		Papers []struct {
			ID string `json:"id"`
		} `json:"papers"`
	}
	if err := json.Unmarshal(got.HandIn, &hand); err != nil || hand.Answer != "Found evidence." || len(hand.Papers) != 1 || hand.Papers[0].ID != "doi:10.1/example" {
		t.Fatalf("accepted hand-in %s: %v", got.HandIn, err)
	}
	if _, err := l.s.Env.Owner.Exec(context.Background(), "UPDATE record SET superseded_at = now() WHERE agent_run_id = $1 AND kind = 'hand_in'", leader.id); err != nil {
		t.Fatal(err)
	}
	got = l.runs(t)[leader.id]
	if got.Answer != "" || (len(got.HandIn) != 0 && string(got.HandIn) != "null") {
		t.Fatalf("superseded hand-in: %+v", got)
	}
	if _, err := l.s.Env.Owner.Exec(context.Background(), "UPDATE record SET superseded_at = null WHERE agent_run_id = $1 AND kind = 'hand_in'", leader.id); err != nil {
		t.Fatal(err)
	}
	if _, err := l.s.Env.Owner.Exec(context.Background(), "UPDATE agent_run SET attempt = 2 WHERE id = $1", leader.id); err != nil {
		t.Fatal(err)
	}
	got = l.runs(t)[leader.id]
	if got.Answer != "" || (len(got.HandIn) != 0 && string(got.HandIn) != "null") {
		t.Fatalf("stale hand-in: %+v", got)
	}
}

// A study held for its owner carries the text of the item asking for them.
func TestHeldStudyCarriesHoldText(t *testing.T) {
	l := newTeamLab(t, 0)
	read := func() string {
		var v struct {
			HoldText string `json:"hold_text"`
		}
		if c := l.s.call(t, "GET", "/api/cards/"+l.card.ID, l.jwt, l.ws, nil, &v); c != 200 {
			t.Fatalf("card %d", c)
		}
		return v.HoldText
	}
	if got := read(); got != "" {
		t.Fatalf("working study hold %q", got)
	}
	if _, err := l.s.Env.Owner.Exec(t.Context(), `UPDATE card SET state = 'blocked', block_kind = '' WHERE id = $1`, l.card.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := l.s.Env.Owner.Exec(t.Context(), `INSERT INTO inbox_item (workspace_id, card_id, user_id, kind, text, cause)
		SELECT workspace_id, id, owner_id, 'needs_decision', 'Study #1: the agent could not do this column: no data', 'could_not_do' FROM card WHERE id = $1`, l.card.ID); err != nil {
		t.Fatal(err)
	}
	if got := read(); got != "Study #1: the agent could not do this column: no data" {
		t.Fatalf("hold %q", got)
	}
	var list []struct {
		ID       string `json:"id"`
		HoldText string `json:"hold_text"`
	}
	if c := l.s.call(t, "GET", "/api/cards", l.jwt, l.ws, nil, &list); c != 200 {
		t.Fatalf("list %d", c)
	}
	if len(list) != 1 || list[0].ID != l.card.ID || list[0].HoldText != "Study #1: the agent could not do this column: no data" {
		t.Fatalf("listed hold %+v", list)
	}
}
