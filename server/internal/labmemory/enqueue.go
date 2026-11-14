package labmemory

import (
	"context"

	"github.com/google/uuid"
	"lykeion/server/internal/service"
	"lykeion/server/internal/store"
	"lykeion/server/pkg/agent"
	db "lykeion/server/pkg/db/generated"
)

// Enqueue freezes one private chat turn in the same transaction as its question.
func Enqueue(ctx context.Context, tx *store.Tx, runs *service.AgentRuns, chat db.LabChat, asker uuid.UUID, question string, contextCard *uuid.UUID, harness, model string) (db.AgentRun, error) {
	return EnqueueWithContext(ctx, tx, runs, chat, asker, question, contextCard, nil, harness, model)
}

func EnqueueWithContext(ctx context.Context, tx *store.Tx, runs *service.AgentRuns, chat db.LabChat, asker uuid.UUID, question string, contextCard *uuid.UUID, contextRef *string, harness, model string) (db.AgentRun, error) {
	if err := agent.LabAvailability(harness); err != nil {
		return db.AgentRun{}, err
	}
	return enqueueTurn(ctx, tx, runs, chat, asker, question, contextCard, contextRef, harness, model)
}

// enqueueTurn writes the turn after the execution policy admits its harness.
func enqueueTurn(ctx context.Context, tx *store.Tx, runs *service.AgentRuns, chat db.LabChat, asker uuid.UUID, question string, contextCard *uuid.UUID, contextRef *string, harness, model string) (db.AgentRun, error) {
	if chat.WorkspaceID != tx.WS || chat.OwnerID != asker {
		return db.AgentRun{}, service.ErrNotYours
	}
	if _, err := tx.Q.LockWorkspaceExecution(ctx, tx.WS); err != nil {
		return db.AgentRun{}, err
	}
	a, err := tx.Q.EnsureAristotle(ctx)
	if err != nil {
		return db.AgentRun{}, err
	}
	run, err := tx.Q.InsertLabAgentRun(ctx, db.InsertLabAgentRunParams{ChatID: chat.ID, Asker: asker, AgentID: &a.ID, Harness: harness, Model: model, Skill: a.Instructions + "\n\n" + protocolForChat(chat)})
	if err != nil {
		return run, err
	}
	if err = tx.Q.InsertLabQuestion(ctx, db.InsertLabQuestionParams{ChatID: chat.ID, Body: question, ContextCardID: contextCard, ContextRef: contextRef, AgentRunID: &run.ID}); err != nil {
		return run, err
	}
	run, err = tx.Q.RefreshLabRunPrompt(ctx, run.ID)
	if err != nil {
		return run, err
	}
	if err = runs.NotifyQueued(ctx, tx, run); err != nil {
		return run, err
	}
	if chat.Purpose == ManualDraftPurpose {
		return run, tx.EmitPrivate(ctx, asker, "lab.message.created", store.User(asker), map[string]any{"chat_id": chat.ID, "owner_id": asker})
	}
	return run, tx.Emit(ctx, "lab.message.created", nil, store.User(asker), map[string]any{"chat_id": chat.ID, "owner_id": asker})
}

func protocolForChat(chat db.LabChat) string {
	if chat.Purpose == ManualDraftPurpose {
		return ManualDraftProtocol()
	}
	return Protocol()
}
