package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"lykeion/server/internal/flow"
	"lykeion/server/internal/testutil"
	db "lykeion/server/pkg/db/generated"
)

type handInOut struct {
	Accepted bool
	Problems []string
}

// handIn posts r's hand-in h as its daemon does.
func (l *teamLab) handIn(t *testing.T, r teamRun, h any) handInOut {
	t.Helper()
	in := maps.Clone(r.ref)
	in["hand_in"] = h
	var out handInOut
	if c := l.s.call(t, "POST", "/api/daemon/agent-runs/"+r.id+"/hand-in", l.personal, "", in, &out); c != 200 {
		t.Fatalf("hand-in %d", c)
	}
	return out
}

// record stores a record on the study as the sources server or the engine
// would, in r's column and round minus back.
func (l *teamLab) record(t *testing.T, r teamRun, back int, kind, key string, body any, run *string) {
	t.Helper()
	b, _ := json.Marshal(body)
	if _, err := l.s.Env.Owner.Exec(t.Context(), `INSERT INTO record (workspace_id, card_id, board_column, round, kind, role, key, body, agent_run_id)
		SELECT workspace_id, card_id, board_column, round - $2, $3, 'server', $4, $5, $6 FROM agent_run WHERE id = $1`,
		r.id, back, kind, key, b, run); err != nil {
		t.Fatal(err)
	}
}

func (l *teamLab) kinds(t *testing.T, kinds ...string) int {
	return l.s.count(t, "SELECT count(*) FROM record WHERE card_id = $1 AND kind = ANY($2)", l.card.ID, kinds)
}

func groundingLab(t *testing.T) (*teamLab, teamRun) {
	t.Helper()
	l := newTeamLabWith(t, testutil.PackOpts{Name: "gr", Stage: "grounding", Members: 1})
	leader := l.claim(t)
	if leader.role != "leader" {
		t.Fatalf("first run %q", leader.role)
	}
	return l, leader
}

func grounding(papers ...string) map[string]any {
	ps := []any{}
	for _, p := range papers {
		ps = append(ps, map[string]any{"id": p, "why": "meets the criterion"})
	}
	return map[string]any{"status": "done", "answer": "Found papers.", "reason": nil, "replies": []any{},
		"papers": ps, "datasets": []any{}, "no_qualifying": nil}
}

func TestSubmitWritesItemRecords(t *testing.T) {
	l, leader := groundingLab(t)
	if !leader.lead || leader.outputSchema == "" {
		t.Fatalf("claim: lead %v schema %q", leader.lead, leader.outputSchema)
	}
	l.record(t, leader, 0, "search", "", map[string]any{"results": []string{"doi:10.1001/a", "doi:10.1001/b"}}, nil)
	l.record(t, leader, 0, "call", "c1", map[string]any{"server": "sources", "tool": "search", "answer": "doi:10.1001/a\ndoi:10.1001/b"}, &leader.id)
	h := grounding("https://doi.org/10.1001/A", "10.1001/b")
	if out := l.handIn(t, leader, h); !out.Accepted || len(out.Problems) > 0 {
		t.Fatalf("hand-in %+v", out)
	}
	rows, err := l.s.Env.Owner.Query(t.Context(), "SELECT kind, body FROM record WHERE card_id = $1 AND role = 'leader' ORDER BY key", l.card.ID)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][]map[string]any{}
	for rows.Next() {
		var kind string
		var b []byte
		if err := rows.Scan(&kind, &b); err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		got[kind] = append(got[kind], m)
	}
	if len(got["hand_in"]) != 1 || len(got["paper"]) != 2 || len(got["summary"]) != 1 || len(got) != 3 {
		t.Fatalf("records %v", got)
	}
	if hi := got["hand_in"][0]; hi["agent_run_id"] != leader.id || hi["attempt"] != float64(1) || hi["hand_in"].(map[string]any)["answer"] != "Found papers." {
		t.Errorf("hand_in %v", hi)
	}
	for _, p := range got["paper"] {
		if c := p["cites"].([]any); len(c) != 1 || c[0] != "call:c1" {
			t.Errorf("paper %v", p)
		}
	}
	if s := got["summary"][0]; s["text"] != "Found papers." {
		t.Errorf("summary %v", s)
	}
	// The same attempt handed in again writes nothing new.
	if out := l.handIn(t, leader, h); !out.Accepted {
		t.Fatalf("again %+v", out)
	}
	if n := l.kinds(t, "hand_in", "paper", "summary"); n != 4 {
		t.Fatalf("records after the replay: %d", n)
	}
}

func TestSubmitReturnsProblems(t *testing.T) {
	l, leader := groundingLab(t)
	l.record(t, leader, 0, "call", "c1", map[string]any{"answer": "doi:10.1001/a"}, &leader.id)
	cases := []struct {
		h    any
		want []string
	}{
		{grounding("doi:10.1001/a", "10.1000/xyz"), []string{"paper 2: 10.1000/xyz was not found through Lykeion's sources"}},
		{map[string]any{"status": "done", "answer": "a", "extra": 1}, []string{"extra: not a field of this hand-in"}},
		{grounding(), []string{"a done Grounding hand-in needs at least one paper, dataset or no_qualifying"}},
	}
	for _, c := range cases {
		if out := l.handIn(t, leader, c.h); out.Accepted || !slices.Equal(out.Problems, c.want) {
			t.Errorf("got %+v, want %q", out, c.want)
		}
	}
	if c, out := l.step(t, leader.token, map[string]any{"member": "member-1", "content": "Search further."}); c != 201 {
		t.Fatalf("step %d %+v", c, out)
	}
	want := []string{"a member is still working: wait for its result with member_result"}
	if out := l.handIn(t, leader, grounding("doi:10.1001/a")); out.Accepted || !slices.Equal(out.Problems, want) {
		t.Errorf("member in flight: %+v", out)
	}
	if n := l.kinds(t, "hand_in", "paper", "dataset", "no_qualifying", "summary"); n != 0 {
		t.Fatalf("%d records written", n)
	}
}

func TestMemberHandInIsNotReviewed(t *testing.T) {
	l, leader := groundingLab(t)
	if c, out := l.step(t, leader.token, map[string]any{"member": "member-1", "content": "Search."}); c != 201 {
		t.Fatalf("step %d %+v", c, out)
	}
	member := l.claim(t)
	if member.role != "member-1" || member.lead || member.outputSchema == "" {
		t.Fatalf("member claim %q lead %v", member.role, member.lead)
	}
	if out := l.handIn(t, member, grounding("10.5/never-recorded")); !out.Accepted {
		t.Fatalf("member hand-in %+v", out)
	}
	if n := l.kinds(t, "hand_in"); n != 1 {
		t.Fatalf("hand_in records %d", n)
	}
	if n := l.kinds(t, "paper", "summary"); n != 0 {
		t.Fatalf("member items written: %d", n)
	}
}

func TestRepliesAnswerSentBackItems(t *testing.T) {
	l, leader := groundingLab(t)
	l.record(t, leader, 0, "call", "c1", map[string]any{"answer": "10.1001/b 10.1001/c"}, &leader.id)
	l.record(t, leader, 1, "review", "s1", map[string]any{"column": "grounding", "attempt": 1, "decision": "redo", "items": []any{
		map[string]any{"key": "r4", "kind": "paper", "label": "doi:10.1001/a", "identity": "doi:10.1001/a", "status": "fail", "checks": []any{}},
		map[string]any{"key": "r5", "kind": "paper", "label": "doi:10.1001/b", "identity": "doi:10.1001/b", "status": "fail", "checks": []any{}},
	}}, nil)
	h := grounding("10.1001/c")
	h["replies"] = []any{
		map[string]any{"item": "https://doi.org/10.1001/A", "action": "withdrawn", "reason": "off topic"},
		map[string]any{"item": "10.1001/b", "action": "corrected", "reason": "fixed the why"},
	}
	want := []string{"reply 2: corrected, but no item 10.1001/b is in this hand-in"}
	if out := l.handIn(t, leader, h); out.Accepted || !slices.Equal(out.Problems, want) {
		t.Fatalf("corrected without the paper: %+v", out)
	}
	h["papers"] = grounding("10.1001/b", "10.1001/c")["papers"]
	if out := l.handIn(t, leader, h); !out.Accepted {
		t.Fatalf("corrected with the paper: %+v", out)
	}
	var b []byte
	if err := l.s.Env.Owner.QueryRow(t.Context(), "SELECT body FROM record WHERE card_id = $1 AND kind = 'withdrawn'", l.card.ID).Scan(&b); err != nil {
		t.Fatal(err)
	}
	var w map[string]any
	_ = json.Unmarshal(b, &w)
	if w["item"] != "r4" || w["reason"] != "off topic" {
		t.Fatalf("withdrawn %v", w)
	}
}

func TestValueFileFromAnotherRun(t *testing.T) {
	l := newTeamLabWith(t, testutil.PackOpts{Name: "ex", Stage: "experimentation", Members: 1})
	leader := l.claim(t)
	if c, out := l.step(t, leader.token, map[string]any{"member": "member-1", "content": "Fit the slope."}); c != 201 {
		t.Fatalf("step %d %+v", c, out)
	}
	member := l.claim(t)
	l.record(t, member, 0, "output", "o1", map[string]any{"name": "fit.txt"}, &member.id)
	l.complete(t, member)
	h := map[string]any{"status": "done", "answer": "The slope is 0.42.", "reason": nil, "replies": []any{}, "outputs": []any{},
		"values": []any{map[string]any{"name": "slope", "value": "0.42", "file": "fit.txt"}}}
	if out := l.handIn(t, leader, h); !out.Accepted {
		t.Fatalf("hand-in %+v", out)
	}
	rows, err := l.s.Env.Owner.Query(t.Context(), "SELECT key, kind, body, agent_run_id FROM record WHERE card_id = $1 AND kind IN ('value', 'output')", l.card.ID)
	if err != nil {
		t.Fatal(err)
	}
	var recs []db.Record
	for rows.Next() {
		var r db.Record
		var run *uuid.UUID
		if err := rows.Scan(&r.Key, &r.Kind, &r.Body, &run); err != nil {
			t.Fatal(err)
		}
		r.AgentRunID = run
		recs = append(recs, r)
	}
	got := flow.MatchValuesIn(recs, nil, map[string]string{"o1": "slope 0.4213\n"})
	if len(got) != 1 || !got[0].Matched || got[0].File != "o1" {
		t.Fatalf("matches %+v", got)
	}
}

func TestDaemonHandInAcceptsLargeQuestionSetAtLimit(t *testing.T) {
	l, leader := groundingLab(t)
	questions := make([]any, 64)
	text := strings.Repeat("🧬", 4000)
	for i := range questions {
		questions[i] = map[string]any{"id": fmt.Sprintf("q_%d", i), "prompt": text, "help_text": text, "required": true, "mode": "text", "options": []any{}, "allow_other": false}
	}
	decision := map[string]any{"kind": "scope", "title": "Choose scope", "body": "Research population", "options": nil, "question_set": map[string]any{"questions": questions}, "expires_at": nil, "from_member": nil}
	hand := map[string]any{"status": "ask", "answer": "Need the researcher.", "reason": nil, "decision": decision, "papers": []any{}, "datasets": []any{}, "no_qualifying": nil, "replies": []any{}}
	in := maps.Clone(leader.ref)
	in["hand_in"] = hand
	payload, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(payload) <= 1<<20 || len(payload) >= 10<<20 {
		t.Fatalf("payload size %d", len(payload))
	}
	path := l.s.URL + "/api/daemon/agent-runs/" + leader.id + "/hand-in"
	post := func(size int) (int, handInOut) {
		t.Helper()
		body := append(bytes.Clone(payload), bytes.Repeat([]byte(" "), size-len(payload))...)
		req, err := http.NewRequest("POST", path, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+l.personal)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out handInOut
		if resp.StatusCode == 200 {
			if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
				t.Fatal(err)
			}
		}
		return resp.StatusCode, out
	}
	if status, _ := post((10 << 20) + 1); status != 413 {
		t.Fatalf("oversize status %d", status)
	}
	if n := l.kinds(t, "hand_in"); n != 0 {
		t.Fatalf("oversize wrote %d hand-ins", n)
	}
	if status, out := post(10 << 20); status != 200 || !out.Accepted {
		t.Fatalf("boundary status %d output %+v", status, out)
	}
	if n := l.kinds(t, "hand_in"); n != 1 {
		t.Fatalf("accepted hand-ins %d", n)
	}
}
