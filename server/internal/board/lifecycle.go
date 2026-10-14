package board

import (
	"fmt"
	"slices"
	"time"
)

type Limits struct {
	ConsecutiveFails int32 `json:"consecutive_fails"`
	RouteBacks       int32 `json:"route_backs"`
}

// Item is an Inbox entry the caller delivers to the card's owner.
type Item struct {
	Kind, Text string
}

func newRound(s *State) {
	s.State = Working
	s.Round++
}

func without(xs []string, x string) []string {
	return slices.DeleteFunc(xs, func(y string) bool { return y == x })
}

// RouteTarget accepts only a stage earlier than from.
func RouteTarget(from, to string) error {
	if i := StageIndex(to); i < 0 || i >= StageIndex(from) {
		return fmt.Errorf("%q is not a stage before %s", to, from)
	}
	return nil
}

// applicable checks a gate before Apply changes anything: the card must be in
// a stage, and a failing gate may route only to its own column or an earlier one.
func applicable(s *State, g *Gate) error {
	if StageIndex(s.Column) < 0 {
		return fmt.Errorf("a %s gate reached a card in %s, which is not a stage", g.Column, s.Column)
	}
	if r := g.Route; g.Verdict == VFail && r != nil && r.Column != "" && r.Column != s.Column {
		if err := RouteTarget(s.Column, r.Column); err != nil {
			return fmt.Errorf("the %s gate routes to %s: %w", s.Column, r.Column, err)
		}
	}
	return nil
}

func Apply(s *State, g *Gate, lim Limits) []Item {
	if err := applicable(s, g); err != nil {
		s.Flag = Escalated
		return []Item{{"escalated", err.Error()}}
	}
	switch g.Verdict {
	case VPass:
		s.Rounds, s.Rerun, s.HandoffNote = 0, "", ""
		if s.Column == Reporting {
			s.Column, s.State, s.Ending = Done, Ready, "released"
			return []Item{{"released", "release sealed"}}
		}
		s.Column = Stages[StageIndex(s.Column)+1]
		s.Stale = without(s.Stale, s.Column)
		newRound(s)
		return nil
	case CannotProceed:
		Stop(s, g.Reason)
		return nil
	}
	if r := g.Route; r != nil && r.Column != "" && r.Column != s.Column {
		return routeBack(s, r.Column, r.Step, g.Reason, lim)
	}
	s.Rounds++
	s.HandoffNote = g.Reason
	if g.Route != nil {
		s.Rerun = g.Route.Step
	}
	newRound(s)
	if s.Rounds >= lim.ConsecutiveFails {
		s.Flag = Escalated
		return []Item{{"escalated", fmt.Sprintf("%d failed gates in %s: %s", s.Rounds, s.Column, g.Reason)}}
	}
	return nil
}

// routeBack moves the card to an earlier column; every column after it must
// be redone, so those are marked stale.
func routeBack(s *State, to, step, reason string, lim Limits) []Item {
	for _, c := range Stages[StageIndex(to)+1 : StageIndex(s.Column)+1] {
		if !slices.Contains(s.Stale, c) {
			s.Stale = append(s.Stale, c)
		}
	}
	s.Column, s.Rounds, s.Rerun, s.HandoffNote = to, 0, step, reason
	s.Stale = without(s.Stale, to)
	s.RouteBacks++
	newRound(s)
	if s.RouteBacks >= lim.RouteBacks {
		s.Flag = Escalated
		return []Item{{"escalated", fmt.Sprintf("%d route-backs; last to %s: %s", s.RouteBacks, to, reason)}}
	}
	return nil
}

func SendBack(s *State, to, step, reason string, lim Limits) ([]Item, error) {
	if err := RouteTarget(s.Column, to); err != nil {
		return nil, fmt.Errorf("a card can only be sent back to an earlier stage: %w", err)
	}
	return routeBack(s, to, step, reason, lim), nil
}

func Stop(s *State, reason string) {
	s.Column, s.Ending, s.EndReason, s.Flag = Done, "stopped", reason, ""
}

func Resume(s *State) { s.Flag, s.Rounds = "", 0 }

func Start(s *State) {
	s.Column, s.HandoffNote = Grounding, ""
	newRound(s)
}

// EscalateAgentRun handles a role run that failed — at once for agent_error,
// context_exhausted, a failed result or no result at all, after its attempts
// for the retried reasons — or a role that could not be queued: the card waits
// for a person, and resuming reruns that role in a fresh round.
func EscalateAgentRun(s *State, role, text string) []Item {
	s.Rerun, s.Flag, s.HandoffNote = role, Escalated, text
	newRound(s)
	return []Item{{"escalated", text}}
}

// CheckBudget flags a card that has spent its money or passed its deadline.
// It never stops the card: a person decides.
func CheckBudget(s *State, now time.Time) []Item {
	over := (s.BudgetUSD > 0 && s.SpentUSD >= s.BudgetUSD) || (s.Deadline != nil && now.After(*s.Deadline))
	if !over || s.Flag == OutOfBudget {
		return nil
	}
	s.Flag = OutOfBudget
	return []Item{{"out_of_budget", fmt.Sprintf("spent $%.2f of $%.2f", s.SpentUSD, s.BudgetUSD)}}
}
