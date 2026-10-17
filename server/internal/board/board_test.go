package board

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func at(col string) *State { return &State{Column: col, State: InReview, Round: 4, Cap: DefaultCap} }

func fail(col, routeCol, step string) *Gate {
	return &Gate{Column: col, Verdict: VFail, Reason: "x", Route: &Route{Column: routeCol, Step: step}}
}

func TestDefaultBands(t *testing.T) {
	for p, want := range map[float64]Result{0.70: Pass, 0.95: Pass, 0.69: Attention, 0.35: Attention, 0.34: Fail, 0: Fail} {
		if got := DefaultBands.Result(p); got != want {
			t.Errorf("Result(%v) = %s, want %s", p, got, want)
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
	Apply(s, &Gate{Column: Grounding, Verdict: VPass})
	if s.Column != Hypotheses || s.State != Working || s.Rounds != 0 || s.Rerun != "" || s.Round != 5 || s.HandoffNote != "" {
		t.Fatalf("%+v", s)
	}
}

func TestPassInReportingReleases(t *testing.T) {
	s := at(Reporting)
	items := Apply(s, &Gate{Column: Reporting, Verdict: VPass})
	if s.Column != Done || s.Ending != "released" || len(items) != 1 || items[0].Kind != "released" {
		t.Fatalf("%+v %+v", s, items)
	}
}

func TestCannotProceedBlocks(t *testing.T) {
	s := at(Grounding)
	items := Apply(s, &Gate{Column: Grounding, Verdict: CannotProceed, Block: BlockNoTest, Reason: "no usable data"})
	if s.Column != Grounding || s.Ending != "" || s.Flag != Escalated || s.BlockKind != BlockNoTest || s.Round != 4 ||
		len(items) != 1 || items[0].Kind != "escalated" || items[0].Text != "no usable data" {
		t.Fatalf("%+v %+v", s, items)
	}
}

func TestCannotProceedUntypedStops(t *testing.T) {
	s := at(Grounding)
	items := Apply(s, &Gate{Column: Grounding, Verdict: CannotProceed, Reason: "no usable data"})
	if s.Column != Done || s.Ending != "stopped" || s.EndReason != "no usable data" || s.Flag != "" || s.BlockKind != "" || len(items) != 0 {
		t.Fatalf("%+v %+v", s, items)
	}
}

func TestResumeClearsAnEscalation(t *testing.T) {
	s := at(Grounding)
	EscalateAgentRun(s, "contrarian", "agent crashed")
	s.Rounds = 2
	Resume(s)
	if s.Flag != "" || s.Rounds != 0 {
		t.Fatalf("resume: %+v", s)
	}
}

func TestTheFourthSameColumnFailBlocks(t *testing.T) {
	s := at(Grounding)
	for i := 0; i < 3; i++ {
		if items := Apply(s, fail(Grounding, Grounding, "contrarian")); len(items) != 0 {
			t.Fatalf("escalated at fail %d", i+1)
		}
	}
	if s.Rerun != "contrarian" || s.State != Working || s.Round != 7 || s.HandoffNote != "x" || s.Rounds != 3 || s.RouteBacks != 3 || s.Flag != "" {
		t.Fatalf("%+v", s)
	}
	items := Apply(s, fail(Grounding, Grounding, "contrarian"))
	if s.Flag != Escalated || s.BlockKind != BlockCap || s.Round != 7 || s.State != Working || len(items) != 1 || items[0].Kind != "escalated" {
		t.Fatalf("%+v %+v", s, items)
	}
}

func TestRouteBackMarksLaterColumnsStale(t *testing.T) {
	s := at(Validation)
	Apply(s, fail(Validation, Hypotheses, "selector"))
	if s.Column != Hypotheses || s.Rerun != "selector" || s.RouteBacks != 1 || s.Round != 5 {
		t.Fatalf("%+v", s)
	}
	if !slices.Equal(s.Stale, []string{Experimentation, Validation}) {
		t.Fatalf("stale %v", s.Stale)
	}
}

func TestARouteBackPastTheCapBlocks(t *testing.T) {
	s := at(Hypotheses)
	s.RouteBacks = 2
	if items := Apply(s, fail(Hypotheses, Grounding, "")); s.Flag != "" || len(items) != 0 || s.RouteBacks != 3 {
		t.Fatalf("the cap's last use blocked: %+v", s)
	}
	s.Column, s.State = Hypotheses, InReview
	if items := Apply(s, fail(Hypotheses, Grounding, "")); s.Flag != Escalated || s.BlockKind != BlockCap || len(items) != 1 {
		t.Fatalf("%+v", s)
	}
}

func TestAPackCapIsHonoured(t *testing.T) {
	s := at(Grounding)
	s.Cap = 1
	Apply(s, fail(Grounding, Grounding, ""))
	if s.Flag != "" {
		t.Fatalf("blocked within the cap: %+v", s)
	}
	if Apply(s, fail(Grounding, Grounding, "")); s.BlockKind != BlockCap {
		t.Fatalf("a cap of 1 allowed a second failure: %+v", s)
	}
}

func TestCapCountsRedosAndRouteBacks(t *testing.T) {
	s := at(Validation)
	Apply(s, fail(Validation, Validation, ""))
	s.State = InReview
	Apply(s, fail(Validation, Validation, ""))
	s.State = InReview
	Apply(s, fail(Validation, Hypotheses, ""))
	if s.State != Working || s.Flag != "" || s.RouteBacks != 3 || s.Column != Hypotheses {
		t.Fatalf("blocked within the cap: %+v", s)
	}
	s.State = InReview
	items := Apply(s, fail(Hypotheses, Hypotheses, ""))
	if s.Flag != Escalated || s.BlockKind != BlockCap || len(items) != 1 || !strings.Contains(items[0].Text, "4") {
		t.Fatalf("%+v %+v", s, items)
	}
}

func TestDecideEarliestColumnWins(t *testing.T) {
	g := &Gate{Column: Reporting, Checks: []Check{{ID: "a", Result: Fail, To: Validation}, {ID: "b", Result: Fail, To: Grounding, Owner: "scoper"},
		{ID: "c", Result: Fail, To: Grounding}, {ID: "d", Result: Fail}}}
	Decide(g)
	if g.Route.Column != Grounding || g.Route.Step != "scoper" || !strings.HasPrefix(g.Reason, "b:") {
		t.Fatalf("%+v %q", g.Route, g.Reason)
	}
	g = &Gate{Column: Validation, Checks: []Check{{ID: "a", Result: Fail, To: "nowhere"}, {ID: "b", Result: Fail, To: Experimentation}}}
	Decide(g)
	if g.Route.Column != Experimentation {
		t.Fatalf("an unknown target counted as the earliest column: %+v", g.Route)
	}
	g = &Gate{Column: Validation, Checks: []Check{{ID: "a", Result: Fail, To: "nowhere"}, {ID: "b", Result: Attention}}}
	Decide(g)
	if g.Route.Column != Validation || !strings.HasPrefix(g.Reason, "a:") {
		t.Fatalf("an unknown target must count as the gate's own column: %+v %q", g.Route, g.Reason)
	}
	g = &Gate{Column: Validation, Checks: []Check{{ID: "a", Result: Fail, To: Hypotheses}, {ID: "b", Result: Attention}}}
	Decide(g)
	if g.Route.Column != Hypotheses {
		t.Fatalf("the gate's own column outranked an earlier one: %+v", g.Route)
	}
}

func blocked(kind string) *State {
	s := at(Validation)
	s.RouteBacks = 4
	Block(s, kind, "why")
	return s
}

func TestUnblockActions(t *testing.T) {
	for _, c := range []struct {
		kind, action string
		cap          int32
		column       string
	}{
		{BlockCap, "raise_cap", 6, Validation},
		{BlockNoTest, "add_data", 0, Hypotheses},
		{BlockInfra, "resume", 0, Validation},
		{BlockDrift, "resume", 0, Validation},
		{BlockHumanRun, "human_run_arrived", 0, Experimentation},
	} {
		s := blocked(c.kind)
		if err := Unblock(s, c.action, c.cap); err != nil {
			t.Fatalf("%s on %s: %v", c.action, c.kind, err)
		}
		if s.Flag != "" || s.BlockKind != "" || s.State != Working || s.Round != 5 || s.Column != c.column {
			t.Fatalf("%s on %s: %+v", c.action, c.kind, s)
		}
		if c.action == "raise_cap" && s.Cap != 6 {
			t.Fatalf("cap %d", s.Cap)
		}
		if c.action == "add_data" && !slices.Equal(s.Stale, []string{Experimentation, Validation, Reporting}) {
			t.Fatalf("stale %v", s.Stale)
		}
	}
	for _, c := range []struct {
		kind, action string
		cap          int32
	}{
		{BlockCap, "add_data", 0}, {BlockCap, "raise_cap", 4}, {BlockInfra, "raise_cap", 6}, {BlockNoTest, "resume", 0},
		{BlockCap, "human_run_arrived", 0}, {BlockTooSmall, "resume", 0}, {BlockInfra, "bogus", 0}, {"", "resume", 0},
	} {
		s := blocked(c.kind)
		before := *s
		if err := Unblock(s, c.action, c.cap); err == nil {
			t.Fatalf("%s on %q was accepted", c.action, c.kind)
		}
		if !reflect.DeepEqual(*s, before) {
			t.Fatalf("a refused unblock changed the card: %+v", s)
		}
	}
}

func TestCloseStopsABlockedCard(t *testing.T) {
	s := blocked(BlockTooSmall)
	Close(s, "not worth splitting")
	if s.Column != Done || s.Ending != "stopped" || s.EndReason != "not worth splitting" || s.Flag != "" || s.BlockKind != "" {
		t.Fatalf("%+v", s)
	}
}

func TestReopen(t *testing.T) {
	for _, s := range []*State{at(Validation), blocked(BlockCap), {Column: Done, Ending: "stopped", Cap: DefaultCap}} {
		before := *s
		if _, err := Reopen(s, "new data"); err == nil || !reflect.DeepEqual(*s, before) {
			t.Fatalf("reopened a card that was not released: %+v %v", s, err)
		}
	}
	s := at(Reporting)
	s.RouteBacks = 3
	Apply(s, &Gate{Column: Reporting, Verdict: VPass})
	items, err := Reopen(s, "a reviewer asked for more")
	if err != nil {
		t.Fatal(err)
	}
	if s.Column != Grounding || s.Ending != "" || s.RouteBacks != 0 || s.Reopens != 1 || s.State != Working || s.Round != 5 ||
		!slices.Equal(s.Stale, Stages[1:]) || len(items) != 1 || items[0] != (Item{"reopened", "a reviewer asked for more"}) {
		t.Fatalf("%+v %+v", s, items)
	}
	s.Column, s.Ending = Done, "released"
	if _, err := Reopen(s, "again"); err != nil || s.Reopens != 2 {
		t.Fatalf("second reopen: %+v %v", s, err)
	}
}

func TestRevertRevision(t *testing.T) {
	s := at(Validation)
	s.RouteBacks = 2
	items := RevertRevision(s)
	if s.Column != Grounding || s.RouteBacks != 2 || s.State != Working || s.Round != 5 || !slices.Equal(s.Stale, Stages[1:]) ||
		len(items) != 1 || items[0] != (Item{"revised", "reverted to the original starting point"}) {
		t.Fatalf("%+v %+v", s, items)
	}
}

func TestUseCap(t *testing.T) {
	s := at(Hypotheses)
	if items := UseCap(s, "a revision", false); items != nil || s.RouteBacks != 1 || s.Round != 4 || s.State != InReview {
		t.Fatalf("a use without a new round: %+v %+v", s, items)
	}
	if items := UseCap(s, "redo", true); items != nil || s.RouteBacks != 2 || s.Round != 5 || s.State != Working {
		t.Fatalf("a use with a new round: %+v %+v", s, items)
	}
	s.RouteBacks = s.Cap
	items := UseCap(s, "a revision", true)
	if s.BlockKind != BlockCap || s.Round != 5 || len(items) != 1 || items[0].Text != "failure 4 with a cap of 3, in hypotheses: a revision" {
		t.Fatalf("past the cap: %+v %+v", s, items)
	}
}

func TestAnInvalidRouteEscalatesWithoutMoving(t *testing.T) {
	for _, to := range []string{Validation, Reporting, Done, Backlog, "nowhere"} {
		s := at(Hypotheses)
		items := Apply(s, fail(Hypotheses, to, "selector"))
		if s.Column != Hypotheses || s.Flag != Escalated || s.Round != 4 || s.RouteBacks != 0 || s.Rerun != "" || len(s.Stale) != 0 ||
			len(items) != 1 || items[0].Kind != "escalated" || !strings.Contains(items[0].Text, to) {
			t.Fatalf("route to %s: %+v %+v", to, s, items)
		}
	}
}

func TestAGateForACardOutsideTheStagesEscalates(t *testing.T) {
	for _, col := range []string{Backlog, Done} {
		s := at(col)
		if items := Apply(s, &Gate{Column: col, Verdict: VPass}); s.Column != col || s.Flag != Escalated || len(items) != 1 {
			t.Fatalf("%s: %+v %+v", col, s, items)
		}
	}
}

func TestRouteTarget(t *testing.T) {
	for _, c := range []struct {
		from, to string
		ok       bool
	}{{Validation, Grounding, true}, {Validation, Experimentation, true}, {Validation, Validation, false},
		{Validation, Reporting, false}, {Grounding, Grounding, false}, {Validation, Done, false}, {Validation, "", false}, {Done, Grounding, false}} {
		if err := RouteTarget(c.from, c.to); (err == nil) != c.ok {
			t.Errorf("RouteTarget(%q, %q) = %v", c.from, c.to, err)
		}
	}
}

func TestSendBackRefusesForward(t *testing.T) {
	if _, err := SendBack(at(Grounding), Validation, "", "why"); err == nil {
		t.Fatal("sent forward")
	}
}

func TestSendBackCarriesItsReason(t *testing.T) {
	s := at(Validation)
	if _, err := SendBack(s, Grounding, "contrarian", "missed the 2019 null result"); err != nil {
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
