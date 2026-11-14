package service

import (
	"github.com/google/uuid"
	db "lykeion/server/pkg/db/generated"
	"testing"
)

func TestTargetedFileRecipientRequiresSameAgentStageAndEligibleSource(t *testing.T) {
	agent, other, card := uuid.New(), uuid.New(), uuid.New()
	source := db.AgentRun{ID: uuid.New(), AgentID: &agent, CardID: &card, Role: "leader", BoardColumn: "grounding", Round: 1, Status: "completed"}
	next := source
	next.ID = uuid.New()
	if !targetedFileRecipient(source, next) {
		t.Fatal("same agent follow-up rejected")
	}
	next.AgentID = &other
	if targetedFileRecipient(source, next) {
		t.Fatal("different agent accepted")
	}
	next.AgentID = &agent
	next.Role = "member"
	if targetedFileRecipient(source, next) {
		t.Fatal("different role accepted")
	}
	next.Role = source.Role
	next.Round++
	if targetedFileRecipient(source, next) {
		t.Fatal("different round accepted")
	}
	next.Round = source.Round
	source.Status = "cancelled"
	if targetedFileRecipient(source, next) {
		t.Fatal("cancelled source accepted")
	}
	source.Status = "running"
	if targetedFileRecipient(source, next) {
		t.Fatal("active source accepted")
	}
	source.Status = "failed"
	if !targetedFileRecipient(source, next) {
		t.Fatal("failed run retry rejected")
	}
	source.AgentID = nil
	if targetedFileRecipient(source, next) {
		t.Fatal("missing identity accepted")
	}
}
