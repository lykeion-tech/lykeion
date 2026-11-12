package board

import (
	"errors"
	"strings"
)

// CheckAgentStage refuses an agent with no stage, or a scope beyond its stage.
func CheckAgentStage(stage, from, to string) error {
	if stage == "" {
		return errors.New("this agent has no stage")
	}
	if from != stage || to != stage {
		return errors.New("this agent works only on " + strings.ToUpper(stage[:1]) + stage[1:])
	}
	return nil
}
