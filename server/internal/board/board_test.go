package board

import (
	"slices"
	"testing"
	"time"
)

var lim = Limits{ConsecutiveFails: 3, RouteBacks: 5}

func at(col string) *State { return &State{Column: col, State: InReview, Round: 4} }

func fail(col, routeCol, step string) *Gate {
	return &Gate{Column: col, Verdict: VFail, Reason: "x", Route: &Route{Column: routeCol, Step: step}}
}

func TestFromP(t *testing.T) {
	for p, want := range map[float64]Result{0.70: Pass, 0.95: Pass, 0.69: Attention, 0.35: Attention, 0.34: Fail, 0: Fail} {
		if got := FromP(p); got != want {
			t.Errorf("FromP(%v) = %s, want %s", p, got, want)
		}
	}
}

func TestDecide(t *testing.T) {
	g := &Gate{Column: Grounding}
	Decide(g)
	if g.Verdict != VFail {
		t.Fatal("a gate with no checks must fail")
	}
	g = &Gate{Column: Grounding, Checks: []Check{{ID: "a", Result: Pass}, {ID: "b", Result: Attention, Owner: "contrarian"}}}
	Decide(g)
	if g.Verdict != VFail || g.Route.Step != "contrarian" || g.Route.Column != Grounding {
		t.Fatalf("unsettled attention must fail and route to its owner: %+v", g.Route)
	}
	g = &Gate{Column: Hypotheses, Checks: []Check{{ID: "review.verdict", Result: Fail, Owner: "scoper", To: Grounding}}}
	Decide(g)
	if g.Route.Column != Grounding || g.Route.Step != "scoper" {
		t.Fatalf("%+v", g.Route)
	}
	g = &Gate{Checks: []Check{{ID: "a", Result: Pass}}}
	Decide(g)
	if g.Verdict != VPass || g.Route != nil {
		t.Fatalf("%+v", g)
	}
	g = &Gate{Verdict: CannotProceed}
	Decide(g)
	if g.Verdict != CannotProceed {
		t.Fatal("cannot_proceed must survive Decide")
	}
}

func TestPassMovesToNextColumnInANewRound(t *testing.T) {
	s := at(Grounding)
	s.Rounds, s.Rerun = 2, "contrarian"
	Apply(s, &Gate{Column: Grounding, Verdict: VPass}, lim)
	if s.Column != Hypotheses || s.State != Working || s.Rounds != 0 || s.Rerun != "" || s.Round != 5 || s.HandoffNote != "" {
		t.Fatalf("%+v", s)
	}
}

func TestPassInReportingReleases(t *testing.T) {
	s := at(Reporting)
	items := Apply(s, &Gate{Column: Reporting, Verdict: VPass}, lim)
	if s.Column != Done || s.Ending != "released" || len(items) != 1 || items[0].Kind != "released" {
		t.Fatalf("%+v %+v", s, items)
	}
}

func TestCannotProceedStops(t *testing.T) {
	s := at(Grounding)
	Apply(s, &Gate{Column: Grounding, Verdict: CannotProceed, Reason: "no usable data"}, lim)
	if s.Column != Done || s.Ending != "stopped" || s.EndReason != "no usable data" {
		t.Fatalf("%+v", s)
	}
}

func TestThirdConsecutiveFailEscalates(t *testing.T) {
	s := at(Grounding)
	for i := 0; i < 2; i++ {
		if items := Apply(s, fail(Grounding, Grounding, "contrarian"), lim); len(items) != 0 {
			t.Fatalf("escalated at fail %d", i+1)
		}
	}
	if s.Rerun != "contrarian" || s.State != Working || s.Round != 6 || s.HandoffNote != "x" {
		t.Fatalf("%+v", s)
	}
	items := Apply(s, fail(Grounding, Grounding, "contrarian"), lim)
	if s.Flag != Escalated || len(items) != 1 || items[0].Kind != "escalated" {
		t.Fatalf("%+v %+v", s, items)
	}
	Resume(s)
	if s.Flag != "" || s.Rounds != 0 {
		t.Fatalf("resume: %+v", s)
	}
}

func TestRouteBackMarksLaterColumnsStale(t *testing.T) {
	s := at(Validation)
	Apply(s, fail(Validation, Hypotheses, "selector"), lim)
	if s.Column != Hypotheses || s.Rerun != "selector" || s.RouteBacks != 1 || s.Round != 5 {
		t.Fatalf("%+v", s)
	}
	if !slices.Equal(s.Stale, []string{Experimentation, Validation}) {
		t.Fatalf("stale %v", s.Stale)
	}
}

func TestFifthRouteBackEscalates(t *testing.T) {
	s := at(Hypotheses)
	s.RouteBacks = 4
	if items := Apply(s, fail(Hypotheses, Grounding, ""), lim); s.Flag != Escalated || len(items) != 1 {
		t.Fatalf("%+v", s)
	}
}

func TestSendBackRefusesForward(t *testing.T) {
	if _, err := SendBack(at(Grounding), Validation, "", "why", lim); err == nil {
		t.Fatal("sent forward")
	}
}

func TestSendBackCarriesItsReason(t *testing.T) {
	s := at(Validation)
	if _, err := SendBack(s, Grounding, "contrarian", "missed the 2019 null result", lim); err != nil {
		t.Fatal(err)
	}
	if s.Column != Grounding || s.Rerun != "contrarian" || s.HandoffNote != "missed the 2019 null result" {
		t.Fatalf("%+v", s)
	}
}

func TestEscalateAgentRunRerunsTheRole(t *testing.T) {
	s := at(Grounding)
	s.State = Working
	items := EscalateAgentRun(s, "contrarian", "agent crashed")
	if s.Flag != Escalated || s.Rerun != "contrarian" || s.Round != 5 || len(items) != 1 || s.HandoffNote != "agent crashed" {
		t.Fatalf("%+v", s)
	}
}

func TestBudgetNeverStops(t *testing.T) {
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	s := at(Grounding)
	s.BudgetUSD, s.SpentUSD = 5, 5
	if items := CheckBudget(s, now); s.Flag != OutOfBudget || len(items) != 1 || s.Ending != "" {
		t.Fatalf("%+v", s)
	}
	if items := CheckBudget(s, now); len(items) != 0 {
		t.Fatal("flagged twice")
	}
	past := now.Add(-time.Minute)
	s = at(Grounding)
	s.Deadline = &past
	if CheckBudget(s, now); s.Flag != OutOfBudget {
		t.Fatal("past deadline must flag out of budget")
	}
}

func TestStartBeginsRoundOne(t *testing.T) {
	s := &State{Column: Backlog}
	Start(s)
	if s.Column != Grounding || s.State != Working || s.Round != 1 {
		t.Fatalf("%+v", s)
	}
}
