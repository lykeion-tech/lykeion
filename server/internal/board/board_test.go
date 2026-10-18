package board

import "testing"

func TestStopEndsTheCard(t *testing.T) {
	s := &State{Column: Hypotheses, State: "blocked", Flag: "escalated", BlockKind: "x"}
	Stop(s, "enough")
	if s.Column != Done || s.State != "stopped" || s.Ending != "stopped" || s.EndReason != "enough" || s.Flag != "" || s.BlockKind != "" {
		t.Fatalf("%+v", s)
	}
}

func TestStageIndex(t *testing.T) {
	for i, c := range Stages {
		if StageIndex(c) != i {
			t.Fatalf("%s: %d", c, StageIndex(c))
		}
	}
	if StageIndex(Backlog) != -1 || StageIndex(Done) != -1 {
		t.Fatal("backlog and done are not stages")
	}
}
