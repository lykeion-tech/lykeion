package labmemory

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"lykeion/server/internal/handin"
	"lykeion/server/internal/service"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

const ManualDraftPurpose = "manual_decision_draft"

var ErrDraftArchived = errors.New("draft conversation is archived")

type DraftProposal struct {
	Message string             `json:"message"`
	Draft   handin.ManualDraft `json:"draft"`
}

type DraftTurn struct {
	UserRequest  string             `json:"user_request"`
	CurrentDraft handin.ManualDraft `json:"current_draft"`
}

func ManualDraftProtocol() string {
	return `Help the person propose and refine a private manual decision note. The user_request and current_draft are supplied every turn. Preserve their current edits unless the request asks to change them. Return message, draft and supporting statements in the supplied schema. The message and draft are proposals, never recorded facts or outcomes. Do not claim to have created a note: only the person can explicitly create it. A draft has only title, optional context, nullable YYYY-MM-DD deadline and nullable authorized study card_id. Do not invent study IDs. Put any factual supporting claim in statements and follow the evidence rules below.
` + Protocol()
}

func DraftStudy(ctx context.Context, tx *store.Tx, who uuid.UUID, d handin.ManualDraft) (*uuid.UUID, error) {
	if d.CardID == nil {
		return nil, nil
	}
	id, e := uuid.Parse(*d.CardID)
	if e != nil || id == uuid.Nil {
		return nil, ErrNotFound
	}
	ok, e := CanOpen(ctx, tx, who, Ref{Kind: "study", ID: id})
	if e != nil {
		return nil, e
	}
	if !ok {
		return nil, ErrNotFound
	}
	return &id, nil
}

// A saved form can contain text derived from a proposal. Retain that source
// dependency through human edits so revocation cannot turn it into new input.
func DraftReadable(ctx context.Context, tx *store.Tx, chat db.LabChat) (bool, error) {
	if chat.Purpose != ManualDraftPurpose {
		return false, nil
	}
	var d handin.ManualDraft
	if json.Unmarshal(chat.Draft, &d) != nil {
		return false, nil
	}
	if _, e := DraftStudy(ctx, tx, chat.OwnerID, d); e != nil {
		if errors.Is(e, ErrNotFound) {
			return false, nil
		}
		return false, e
	}
	if chat.AppliedMessageID != nil {
		ok, e := NewDisplayReader(tx, chat.OwnerID, chat.ID).visible(ctx, *chat.AppliedMessageID)
		if e != nil || !ok {
			return ok, e
		}
	}
	if chat.DraftSource != nil {
		return NewDisplayReader(tx, chat.OwnerID, chat.ID).visible(ctx, *chat.DraftSource)
	}
	return true, nil
}

func SaveDraft(ctx context.Context, tx *store.Tx, id uuid.UUID, d handin.ManualDraft, applied ...*uuid.UUID) (db.LabChat, error) {
	if _, e := tx.Q.LockWorkspaceExecution(ctx, tx.WS); e != nil {
		return db.LabChat{}, e
	}
	chat, e := tx.Q.GetPrivateLabChat(ctx, id)
	if e != nil {
		return chat, e
	}
	if chat.ArchivedAt != nil {
		return chat, ErrDraftArchived
	}
	ok, e := DraftReadable(ctx, tx, chat)
	if e != nil {
		return chat, e
	}
	if !ok {
		return chat, pgx.ErrNoRows
	}
	if len(handin.ValidateManualDraft(d, true)) > 0 {
		return chat, service.ErrConflict
	}
	if _, e = DraftStudy(ctx, tx, chat.OwnerID, d); e != nil {
		return chat, e
	}
	marker := chat.AppliedMessageID
	if len(applied) > 0 {
		marker = applied[0]
	}
	if marker != nil {
		row, e := tx.Q.GetPrivateAnswerSources(ctx, db.GetPrivateAnswerSourcesParams{ChatID: chat.ID, ID: *marker})
		if e != nil {
			return chat, e
		}
		var proposal DraftProposal
		if json.Unmarshal(row.Proposal, &proposal) != nil || len(handin.ValidateManualDraft(proposal.Draft, false)) > 0 {
			return chat, pgx.ErrNoRows
		}
		ok, e := NewDisplayReader(tx, chat.OwnerID, chat.ID).visible(ctx, *marker)
		if e != nil {
			return chat, e
		}
		if !ok {
			return chat, pgx.ErrNoRows
		}
	}
	raw, _ := json.Marshal(d)
	return tx.Q.SaveManualDraft(ctx, db.SaveManualDraftParams{ID: id, Draft: raw, AppliedMessageID: marker})
}

func (a Answers) Draft(ctx context.Context, tx *store.Tx, run db.AgentRun, raw json.RawMessage) ([]string, error) {
	if run.LabPurpose != ManualDraftPurpose {
		return nil, service.ErrNotYours
	}
	return a.answer(ctx, tx, run, raw, handin.ManualDecisionDraft)
}

// The envelope retains its study identity even when a deleted study causes the
// message's relational context pointer to be cleared.
func DraftQuestionStudy(ctx context.Context, tx *store.Tx, who uuid.UUID, body string, contextCard *uuid.UUID) (*uuid.UUID, error) {
	var input DraftTurn
	if json.Unmarshal([]byte(body), &input) != nil {
		return nil, ErrNotFound
	}
	card, e := DraftStudy(ctx, tx, who, input.CurrentDraft)
	if e != nil {
		return nil, e
	}
	if contextCard != nil {
		ok, e := CanOpen(ctx, tx, who, Ref{Kind: "study", ID: *contextCard})
		if e != nil {
			return nil, e
		}
		if !ok {
			return nil, ErrNotFound
		}
		if card == nil {
			card = contextCard
		}
	}
	return card, nil
}
