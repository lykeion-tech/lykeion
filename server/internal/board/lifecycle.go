package board

// Item is an Inbox entry the caller delivers to the card's owner.
type Item struct {
	Kind, Text string
}

func newRound(s *State) {
	s.State = Working
	s.Round++
}

func Stop(s *State, reason string) {
	s.Column, s.Ending, s.EndReason, s.Flag, s.BlockKind = Done, "stopped", reason, "", ""
}

func Resume(s *State) { s.Flag, s.Rounds = "", 0 }

func Start(s *State) {
	s.Column, s.HandoffNote = Grounding, ""
	newRound(s)
}
