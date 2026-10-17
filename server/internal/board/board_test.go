package board

import "testing"

func TestStartBeginsRoundOne(t *testing.T) {
	s := &State{Column: Backlog}
	Start(s)
	if s.Column != Grounding || s.State != Working || s.Round != 1 {
		t.Fatalf("%+v", s)
	}
}

func TestResumeClearsAnEscalation(t *testing.T) {
	s := &State{Column: Grounding, Flag: Escalated, Rounds: 2}
	Resume(s)
	if s.Flag != "" || s.Rounds != 0 {
		t.Fatalf("resume: %+v", s)
	}
}

func TestStopEndsTheCard(t *testing.T) {
	s := &State{Column: Hypotheses, Flag: Escalated, BlockKind: "x"}
	Stop(s, "enough")
	if s.Column != Done || s.Ending != "stopped" || s.EndReason != "enough" || s.Flag != "" || s.BlockKind != "" {
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
