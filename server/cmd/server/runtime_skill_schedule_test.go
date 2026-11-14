package main

import (
	"errors"
	"testing"
	"time"
)

func TestRuntimeSkillSweepDelay(t *testing.T) {
	if got := runtimeSkillSweepDelay(true, nil); got != time.Second {
		t.Fatalf("continuation delay: %v", got)
	}
	if got := runtimeSkillSweepDelay(true, errors.New("failed")); got != 30*time.Second {
		t.Fatalf("failure backoff: %v", got)
	}
	if got := runtimeSkillSweepDelay(false, nil); got != 30*time.Second {
		t.Fatalf("idle delay: %v", got)
	}
}
