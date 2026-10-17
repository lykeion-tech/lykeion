package board

import (
	"fmt"
	"slices"
	"time"
)

// DefaultCap is how many failures a card absorbs (route-backs, same-column
// redos and revisions together) before the next one blocks it.
const DefaultCap = 3

// Block kinds: why a card sits in Blocked.
const (
	BlockCap      = "cap"
	BlockInfra    = "infra"
	BlockNoTest   = "no_feasible_test"
	BlockHumanRun = "human_run"
	BlockTooSmall = "too_small_to_split"
	BlockDrift    = "drift"
)

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

// markStale marks every stage after from, up to and including through, to be redone.
func markStale(s *State, from, through string) {
	for _, c := range Stages[StageIndex(from)+1 : StageIndex(through)+1] {
		if !slices.Contains(s.Stale, c) {
			s.Stale = append(s.Stale, c)
		}
	}
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

// Apply moves a card by its gate. Every failure uses the cap, whether it
// routes back or redoes the column; the one past the cap blocks the card
// where the route left it, without opening a round. A gate that cannot
// proceed blocks the card when it names a block kind and stops it otherwise.
func Apply(s *State, g *Gate) []Item {
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
		if g.Block == "" {
			Stop(s, g.Reason)
			return nil
		}
		return Block(s, g.Block, g.Reason)
	}
	if r := g.Route; r != nil && r.Column != "" && r.Column != s.Column {
		return routeBack(s, r.Column, r.Step, g.Reason)
	}
	s.Rounds++
	s.HandoffNote = g.Reason
	if g.Route != nil {
		s.Rerun = g.Route.Step
	}
	return UseCap(s, g.Reason, true)
}

// UseCap counts one use of the cap, opening the next round when asked, or
// blocks the card once the uses exceed it.
func UseCap(s *State, reason string, openRound bool) []Item {
	s.RouteBacks++
	if s.RouteBacks > s.Cap {
		return Block(s, BlockCap, fmt.Sprintf("failure %d with a cap of %d, in %s: %s", s.RouteBacks, s.Cap, s.Column, reason))
	}
	if openRound {
		newRound(s)
	}
	return nil
}

// routeBack moves the card to an earlier column; every column after it must
// be redone, so those are marked stale.
func routeBack(s *State, to, step, reason string) []Item {
	markStale(s, to, s.Column)
	s.Column, s.Rounds, s.Rerun, s.HandoffNote = to, 0, step, reason
	s.Stale = without(s.Stale, to)
	return UseCap(s, reason, true)
}

func SendBack(s *State, to, step, reason string) ([]Item, error) {
	if err := RouteTarget(s.Column, to); err != nil {
		return nil, fmt.Errorf("a card can only be sent back to an earlier stage: %w", err)
	}
	return routeBack(s, to, step, reason), nil
}

// Block puts the card in Blocked: it waits for a person, who unblocks or closes it.
func Block(s *State, kind, reason string) []Item {
	s.Flag, s.BlockKind = Escalated, kind
	return []Item{{"escalated", reason}}
}

// Unblock takes a person's way out of Blocked. Each action fits only some
// block kinds; any other pairing is refused and leaves the card as it was.
func Unblock(s *State, action string, cap int32) error {
	switch {
	case action == "raise_cap" && s.BlockKind == BlockCap:
		if cap <= s.RouteBacks {
			return fmt.Errorf("the cap must exceed the %d already used", s.RouteBacks)
		}
		s.Cap = cap
	case action == "add_data" && s.BlockKind == BlockNoTest:
		markStale(s, Hypotheses, Reporting)
		s.Column, s.Rounds, s.Rerun = Hypotheses, 0, ""
		s.Stale = without(s.Stale, Hypotheses)
	case action == "resume" && (s.BlockKind == BlockInfra || s.BlockKind == BlockDrift):
	case action == "human_run_arrived" && s.BlockKind == BlockHumanRun:
		s.Column = Experimentation
		s.Stale = without(s.Stale, Experimentation)
	default:
		return fmt.Errorf("%q does not unblock a card blocked for %q", action, s.BlockKind)
	}
	s.Flag, s.BlockKind = "", ""
	newRound(s)
	return nil
}

func Stop(s *State, reason string) {
	s.Column, s.Ending, s.EndReason, s.Flag, s.BlockKind = Done, "stopped", reason, "", ""
}

// Close stops a Blocked card.
func Close(s *State, reason string) { Stop(s, reason) }

// Reopen sends a released card back to Grounding with a fresh cap; every
// later stage must be redone.
func Reopen(s *State, reason string) ([]Item, error) {
	if s.Ending != "released" {
		return nil, fmt.Errorf("only a released card can be reopened")
	}
	restart(s, reason)
	s.Ending, s.EndReason, s.RouteBacks = "", "", 0
	s.Reopens++
	return []Item{{"reopened", reason}}, nil
}

// RevertRevision returns the card to Grounding on its original starting
// point; every later stage must be redone. The cap is not refunded.
func RevertRevision(s *State) []Item {
	const why = "reverted to the original starting point"
	restart(s, why)
	return []Item{{"revised", why}}
}

func restart(s *State, note string) {
	markStale(s, Grounding, Reporting)
	s.Column, s.Rounds, s.Rerun, s.HandoffNote = Grounding, 0, "", note
	s.Stale = without(s.Stale, Grounding)
	newRound(s)
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
