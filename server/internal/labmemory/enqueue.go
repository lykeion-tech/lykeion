package labmemory

import (
	"context"

	"github.com/google/uuid"
	"lykeion/server/internal/service"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

// Enqueue freezes one private chat turn in the same transaction as its question.
func Enqueue(ctx context.Context, tx *store.Tx, runs *service.AgentRuns, chat db.LabChat, asker uuid.UUID, question string, contextCard *uuid.UUID, harness, model string) (db.AgentRun, error) {
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
	run, err := tx.Q.InsertLabAgentRun(ctx, db.InsertLabAgentRunParams{ChatID: chat.ID, Asker: asker, AgentID: &a.ID, Harness: harness, Model: model, Skill: a.Instructions + "\n\n" + Protocol()})
	if err != nil {
		return run, err
	}
	if err = tx.Q.InsertLabQuestion(ctx, db.InsertLabQuestionParams{ChatID: chat.ID, Body: question, ContextCardID: contextCard, AgentRunID: &run.ID}); err != nil {
		return run, err
	}
	run, err = tx.Q.RefreshLabRunPrompt(ctx, run.ID)
	if err != nil {
		return run, err
	}
	if err = runs.NotifyQueued(ctx, tx, run); err != nil {
		return run, err
	}
	return run, tx.Emit(ctx, "lab.message.created", nil, store.User(asker), map[string]any{"chat_id": chat.ID, "owner_id": asker})
}
