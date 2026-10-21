package analytics

import (
	"github.com/google/uuid"
	"testing"
	"time"
)

func instant(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}
func TestCalendarAndAttemptLedger(t *testing.T) {
	now := instant("2026-03-30T10:00:00Z")
	run, study := uuid.New(), uuid.New()
	data := []Run{{ID: run, StudyID: study, Attempt: 2, Status: "cancelled", Role: "researcher", Harness: "codex"}}
	events := []Event{
		{RunID: run, Attempt: 1, Type: "agent_run.started", At: instant("2026-03-28T22:00:00Z")},
		{RunID: run, Attempt: 1, Type: "agent_run.queued", At: instant("2026-03-29T01:00:00Z")},
		{RunID: run, Attempt: 2, Type: "agent_run.started", At: instant("2026-03-29T22:30:00Z")},
		{RunID: run, Attempt: 2, Type: "agent_run.cancelled", At: instant("2026-03-29T23:30:00Z")},
	}
	usage := []Usage{{RunID: run, Attempt: 1, Input: 10, CacheRead: 20, CacheWrite: 30, Output: 5, Cost: 0.1, Reported: true}, {RunID: run, Attempt: 2, Input: 2, Output: 3, Cost: 0.2}}
	got, err := Aggregate(now, "Europe/Copenhagen", "7d", "daily", "all", data, events, usage)
	if err != nil {
		t.Fatal(err)
	}
	if got.Summary.InputTokens != 62 || got.Summary.OutputTokens != 8 || got.Summary.Studies != 1 || got.Summary.RuntimeSeconds != 14400 {
		t.Fatalf("summary: %+v", got.Summary)
	}
	if len(got.Buckets) != 7 || got.Buckets[5].End.Sub(got.Buckets[5].Start) != 23*time.Hour {
		t.Fatalf("DST buckets: %+v", got.Buckets)
	}
	if len(got.Agents) != 1 || got.Agents[0].AgentID != nil {
		t.Fatalf("unattributed: %+v", got.Agents)
	}
	if got.Summary.CostReportedUSD != 0.1 || got.Summary.CostEstimatedUSD != 0.2 {
		t.Fatalf("cost: %+v", got.Summary)
	}
	if got.Buckets[5].Studies != 1 || got.Buckets[6].Studies != 1 {
		t.Fatalf("distinct bucket studies: %+v", got.Buckets)
	}
}
func TestMissingAndUnassignedAreNotDatedZeroes(t *testing.T) {
	now := instant("2026-10-21T12:00:00Z")
	a, b, study := uuid.New(), uuid.New(), uuid.New()
	runs := []Run{{ID: a, StudyID: study, Attempt: 1, Status: "running"}, {ID: b, StudyID: study, Attempt: 1, Status: "failed"}}
	events := []Event{{RunID: a, Attempt: 1, Type: "agent_run.started", At: now.Add(-time.Hour)}}
	got, err := Aggregate(now, "UTC", "1d", "daily", "all", runs, events, []Usage{{RunID: b, Attempt: 1, Input: 99, Cost: 3}})
	if err != nil {
		t.Fatal(err)
	}
	if got.Summary.RuntimeSeconds != 3600 || got.Summary.Studies != 1 || got.Summary.Coverage.Tokens != "missing" || got.Summary.Coverage.Time != "complete" {
		t.Fatalf("coverage: %+v", got.Summary)
	}
	if got.Unassigned.Attempts != 1 || got.Unassigned.InputTokens != 99 || got.Unassigned.CostUSD != 3 || got.Summary.InputTokens != 0 {
		t.Fatalf("unassigned: %+v %+v", got.Unassigned, got.Summary)
	}
}
func TestWeeklyClippingAndEmpty(t *testing.T) {
	now := instant("2026-10-21T12:00:00Z")
	got, err := Aggregate(now, "UTC", "7d", "weekly", "all", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Buckets) != 2 || got.Buckets[0].Start != instant("2026-10-15T00:00:00Z") || got.Buckets[1].Start != instant("2026-10-19T00:00:00Z") || got.Summary.Coverage.Tokens != "complete" {
		t.Fatalf("weekly: %+v", got)
	}
	for _, r := range []string{"2d", "", "365d"} {
		if _, err := Aggregate(now, "UTC", r, "daily", "all", nil, nil, nil); err == nil {
			t.Fatalf("accepted range %q", r)
		}
	}
}
func TestRetryModelsAndUnknownTiming(t *testing.T) {
	now := instant("2026-10-21T12:00:00Z")
	run, study, agent := uuid.New(), uuid.New(), uuid.New()
	runs := []Run{{ID: run, StudyID: study, AgentID: &agent, AgentName: "Scientist", Attempt: 2, Status: "completed"}}
	events := []Event{{RunID: run, Attempt: 1, Type: "agent_run.started", At: now.Add(-4 * time.Hour)}, {RunID: run, Attempt: 1, Type: "agent_run.requeued", At: now.Add(-3 * time.Hour)}, {RunID: run, Attempt: 2, Type: "agent_run.completed", At: now.Add(-time.Hour)}}
	usage := []Usage{{RunID: run, Attempt: 1, Input: 10, Cost: 0, Reported: true}, {RunID: run, Attempt: 1, Input: 20, Output: 5, Cost: 1, Reported: true}, {RunID: run, Attempt: 2, Input: 30, Cost: 2}}
	got, err := Aggregate(now, "UTC", "1d", "daily", "all", runs, events, usage)
	if err != nil {
		t.Fatal(err)
	}
	if got.Summary.InputTokens != 60 || got.Summary.CostUSD != 3 || got.Summary.RuntimeSeconds != 3600 || got.Summary.Studies != 1 || got.Summary.Coverage.Time != "partial" || got.Summary.Coverage.Tokens != "complete" {
		t.Fatalf("retry: %+v", got.Summary)
	}
	if len(got.Agents) != 1 || *got.Agents[0].AgentID != agent || got.Agents[0].InputTokens != 60 {
		t.Fatalf("agents: %+v", got.Agents)
	}
}
func TestCardlessAndQueuedAreExcluded(t *testing.T) {
	now := instant("2026-10-21T12:00:00Z")
	run := uuid.New()
	got, err := Aggregate(now, "UTC", "1d", "daily", "all", []Run{{ID: run, Attempt: 1, Status: "running"}, {ID: uuid.New(), StudyID: uuid.New(), Status: "queued"}}, []Event{{RunID: run, Attempt: 1, Type: "agent_run.started", At: now.Add(-time.Hour)}}, []Usage{{RunID: run, Attempt: 1, Input: 99}})
	if err != nil {
		t.Fatal(err)
	}
	if got.Summary.Studies != 0 || got.Summary.InputTokens != 0 || got.Unassigned.Attempts != 0 || got.Summary.Coverage.Tokens != "complete" {
		t.Fatalf("cardless: %+v", got)
	}
}

func TestZeroEstimatedCostRetainsProvenance(t *testing.T) {
	now := instant("2026-10-21T12:00:00Z")
	run, study := uuid.New(), uuid.New()
	got, err := Aggregate(now, "UTC", "1d", "daily", "all", []Run{{ID: run, StudyID: study, Attempt: 1, Status: "completed"}}, []Event{{RunID: run, Attempt: 1, Type: "agent_run.completed", At: now}}, []Usage{{RunID: run, Attempt: 1, Input: 1, Cost: 0, Reported: false}})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Summary.IncludesEstimates || !got.Buckets[0].IncludesEstimates || !got.Agents[0].IncludesEstimates {
		t.Fatalf("zero estimate provenance lost: %+v", got)
	}
}
func TestMissingStartEventKeepsKnownRunTimingIncomplete(t *testing.T) {
	now := instant("2026-10-21T12:00:00Z")
	run, study := uuid.New(), uuid.New()
	started := now.Add(-time.Hour)
	got, err := Aggregate(now, "UTC", "1d", "daily", "all", []Run{{ID: run, StudyID: study, Attempt: 1, Status: "failed", StartedAt: &started}}, []Event{{RunID: run, Attempt: 1, Type: "agent_run.dispatched", At: now.Add(-2 * time.Hour)}, {RunID: run, Attempt: 1, Type: "agent_run.failed", At: now}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Summary.Coverage.Time != "missing" || got.Summary.Coverage.Tokens != "missing" {
		t.Fatalf("missing lifecycle incorrectly empty: %+v", got)
	}
}

func TestUnassignedUsageDoesNotImplyDatedActivity(t *testing.T) {
	now := instant("2026-10-21T12:00:00Z")
	run := uuid.New()
	got, err := Aggregate(now, "UTC", "90d", "daily", "all", []Run{{ID: run, StudyID: uuid.New(), Attempt: 1, Status: "failed"}}, nil, []Usage{{RunID: run, Attempt: 1, Input: 100, Cost: 4}})
	if err != nil {
		t.Fatal(err)
	}
	if got.Unassigned.Attempts != 1 || got.Unassigned.InputTokens != 100 || got.Unassigned.CostUSD != 4 {
		t.Fatalf("unassigned: %+v", got)
	}
	if len(got.Agents) != 0 || got.Summary.InputTokens != 0 || got.Summary.Coverage.Tokens != "complete" {
		t.Fatalf("unassigned leaked dated activity: %+v", got)
	}
	for _, b := range got.Buckets {
		if b.Coverage.Tokens != "complete" || b.Studies != 0 {
			t.Fatalf("unassigned leaked bucket: %+v", b)
		}
	}
}
