package handler

import (
	"testing"

	"github.com/google/uuid"

	"lykeion/server/internal/analytics"
)

func TestAnalyticsAuthorizationAndEmpty(t *testing.T) {
	s := newServer(t)
	token := s.signIn(t, "analytics@lab.org")
	w := s.workspace(t, token, "analytics-lab")
	var got analytics.Response
	if code := s.call(t, "GET", "/api/analytics", token, w.ID, nil, &got); code != 200 {
		t.Fatalf("analytics: %d", code)
	}
	if got.Summary.Coverage.Tokens != "complete" || got.Summary.Studies != 0 || len(got.Buckets) != 30 {
		t.Fatalf("empty response: %+v", got)
	}
	for _, query := range []string{"range=2d", "cadence=monthly", "scope=bad"} {
		if code := s.call(t, "GET", "/api/analytics?"+query, token, w.ID, nil, nil); code != 400 {
			t.Fatalf("invalid %s: %d", query, code)
		}
	}
	if code := s.call(t, "GET", "/api/analytics?scope="+uuid.NewString(), token, w.ID, nil, nil); code != 404 {
		t.Fatalf("unavailable research: %d", code)
	}
	outsider := s.signIn(t, "analytics-outsider@lab.org")
	if code := s.call(t, "GET", "/api/analytics", outsider, w.ID, nil, nil); code != 404 && code != 403 {
		t.Fatalf("outsider: %d", code)
	}
}

func TestAnalyticsPersistedReportAndCurrentResearchScope(t *testing.T) {
	l := newTeamLab(t, 0)
	run := l.claim(t)
	report := map[string]any{}
	for k, v := range run.ref {
		report[k] = v
	}
	report["usage"] = []map[string]any{{"model": "m1", "input_tokens": 10, "cache_read_tokens": 20, "cache_write_tokens": 30, "output_tokens": 5, "cost_usd": 0.25}, {"model": "m2", "input_tokens": 2, "output_tokens": 3, "cost_usd": 0}}
	if code := l.s.call(t, "POST", "/api/daemon/agent-runs/"+run.id+"/complete", l.personal, "", report, nil); code != 200 {
		t.Fatalf("report: %d", code)
	}
	read := func(scope string) analytics.Response {
		t.Helper()
		var got analytics.Response
		if code := l.s.call(t, "GET", "/api/analytics?range=1d&scope="+scope, l.jwt, l.ws, nil, &got); code != 200 {
			t.Fatalf("read %s: %d", scope, code)
		}
		return got
	}
	got := read("standalone")
	if got.Summary.InputTokens != 62 || got.Summary.OutputTokens != 8 || got.Summary.CostUSD != 0.25 || got.Summary.CostReportedUSD != 0.25 || got.Summary.Studies != 1 || len(got.Agents) != 1 {
		t.Fatalf("persisted report: %+v", got)
	}
	var project struct{ ID string }
	if code := l.s.call(t, "POST", "/api/researches", l.jwt, l.ws, map[string]any{"name": "Analytics research"}, &project); code != 201 {
		t.Fatalf("research: %d", code)
	}
	if code := l.s.call(t, "PATCH", "/api/cards/"+l.card.ID+"/properties", l.jwt, l.ws, map[string]any{"research_id": project.ID}, nil); code != 200 {
		t.Fatalf("associate: %d", code)
	}
	if got = read("standalone"); got.Summary.Studies != 0 || got.Summary.InputTokens != 0 {
		t.Fatalf("standalone after move: %+v", got)
	}
	if got = read(project.ID); got.Summary.Studies != 1 || got.Summary.InputTokens != 62 {
		t.Fatalf("research after move: %+v", got)
	}
	if code := l.s.call(t, "PATCH", "/api/me", l.jwt, "", map[string]any{"timezone": "Europe/Copenhagen"}, nil); code != 200 {
		t.Fatalf("timezone: %d", code)
	}
	if got = read(project.ID); got.Timezone != "Europe/Copenhagen" {
		t.Fatalf("stored timezone: %+v", got)
	}
	other := l.s.workspace(t, l.jwt, "analytics-other")
	if code := l.s.call(t, "GET", "/api/analytics?scope="+project.ID, l.jwt, other.ID, nil, nil); code != 404 {
		t.Fatalf("cross-workspace research: %d", code)
	}
}
