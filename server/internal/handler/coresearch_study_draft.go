package handler

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/coresearch"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

type coresearchDraftSource struct {
	ProjectID uuid.UUID `json:"project_id"`
	ChatID    uuid.UUID `json:"chat_id"`
}

type coresearchMessageView struct {
	db.ListCoresearchMessagesRow
	StudyDraft   *coresearch.StudyDraft `json:"study_draft,omitempty"`
	StudyID      *uuid.UUID             `json:"study_id,omitempty"`
	StudyDeleted bool                   `json:"study_deleted,omitempty"`
}

func coresearchStudyDraft(ctx context.Context, tx *store.Tx, message db.ListCoresearchMessagesRow, priorTurns int) (*coresearch.StudyDraft, error) {
	if message.Kind != "reply" || message.AgentRunID == nil || message.Status == nil || *message.Status != "completed" {
		return nil, nil
	}
	draft := coresearch.ParseStudyDraft(message.Body, priorTurns)
	if draft == nil {
		return nil, nil
	}
	run, err := tx.Q.GetAgentRun(ctx, db.GetAgentRunParams{WorkspaceID: tx.WS, ID: *message.AgentRunID})
	if err != nil || run.AgentID == nil {
		return nil, err
	}
	a, err := tx.Q.GetAgent(ctx, db.GetAgentParams{WorkspaceID: tx.WS, ID: *run.AgentID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil || a.BuiltInKey != "aristotle" {
		return nil, err
	}
	return draft, nil
}

func priorCoresearchTurns(rows []db.ListCoresearchMessagesRow) map[uuid.UUID]int {
	counts := make(map[uuid.UUID]int)
	prior := 0
	for _, row := range rows {
		if row.Kind == "turn" && row.AgentRunID != nil {
			counts[*row.AgentRunID] = prior
			if row.Status != nil && *row.Status == "completed" {
				prior++
			}
		}
	}
	return counts
}

func validateCoresearchDraft(ctx context.Context, tx *store.Tx, user, id uuid.UUID, source coresearchDraftSource) error {
	unavailable := conflict("This Coresearch study draft is no longer available.")
	chat, err := tx.Q.GetCoresearchChat(ctx, db.GetCoresearchChatParams{WorkspaceID: tx.WS, ProjectID: source.ProjectID, ID: source.ChatID})
	if errors.Is(err, pgx.ErrNoRows) {
		return unavailable
	}
	if err != nil {
		return err
	}
	if chat.OwnerID != user || chat.Mode != "experiment" {
		return unavailable
	}
	project, err := tx.Q.GetCoresearchProject(ctx, db.GetCoresearchProjectParams{WorkspaceID: tx.WS, ID: source.ProjectID})
	if err != nil {
		return err
	}
	if project.ArchivedAt != nil {
		return unavailable
	}
	rows, err := tx.Q.ListCoresearchMessages(ctx, db.ListCoresearchMessagesParams{WorkspaceID: tx.WS, ChatID: chat.ID})
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.ID != id || row.AgentRunID == nil {
			continue
		}
		draft, err := coresearchStudyDraft(ctx, tx, row, priorCoresearchTurns(rows)[*row.AgentRunID])
		if err != nil {
			return err
		}
		if draft != nil {
			return nil
		}
		break
	}
	return unavailable
}
