package board

// Item is an Inbox entry the caller delivers to the card's owner.
type Item struct {
	Kind, Text string
}

// Stop ends the study for reason; an ended study is never reopened.
func Stop(s *State, reason string) {
	s.Column, s.State, s.Ending, s.EndReason, s.Flag, s.BlockKind = Done, "stopped", "stopped", reason, "", ""
}

// Park holds the study where it stopped until someone redoes its column.
func Park(s *State) {
	s.State, s.BlockKind = "blocked", "parked"
}
