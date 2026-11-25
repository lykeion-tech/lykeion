package handler

import (
	"errors"
	"net/http"
	"reflect"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/store"
)

func (h *H) listStudyProposals(w http.ResponseWriter, r *http.Request) {
	page, ok := labPagination(w, r)
	if !ok {
		return
	}
	filters := r.URL.Query()
	var owner, project *uuid.UUID
	for key, field := range map[string]**uuid.UUID{"owner_id": &owner, "project_id": &project} {
		if filters.Has(key) {
			id, err := uuid.Parse(filters.Get(key))
			if err != nil {
				writeErr(w, 400, "invalid "+key)
				return
			}
			*field = &id
		}
	}
	var rows []store.StudyProposal
	err := h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) error {
		var err error
		rows, err = tx.StudyProposals(r.Context(), owner, project, page.before, page.id, page.limit+1)
		return err
	})
	if !answer(w, r, err) {
		return
	}
	var next *string
	if len(rows) > int(page.limit) {
		rows = rows[:page.limit]
		last := rows[len(rows)-1]
		token := labCursor(last.CreatedAt, last.ID)
		next = &token
	}
	writeJSON(w, 200, map[string]any{"items": rows, "next_cursor": next})
}

func proposalCard(content store.ProposalContent) (preparedCard, error) {
	if strings.ContainsRune(content.Description, 0) || strings.ContainsRune(content.Title, 0) {
		return preparedCard{}, cardInputError("invalid description")
	}
	a := content.Assignee
	in := newCard{Title: content.Title, Priority: content.Priority, ResearchID: content.ProjectID, Question: content.Description, Scope: &scopeIn{From: content.Stage, To: content.Stage}}
	switch {
	case a.Type == "agent" && a.AgentID != nil && a.PackID == nil:
		in.Assignee = &assigneeIn{Type: "agent", ID: a.AgentID}
	case a.Type == "team" && a.PackID != nil && a.AgentID == nil:
		in.Assignee = &assigneeIn{Type: "team", Teams: map[string]uuid.UUID{content.Stage: *a.PackID}}
	default:
		return preparedCard{}, cardInputError("select one agent or Team")
	}
	return prepareCard(in)
}

type startedProposal struct {
	cardID uuid.UUID
	number int32
}

func (e startedProposal) Error() string { return "proposal already started" }

type proposalRevisionConflict struct{ revision int64 }

func (c proposalRevisionConflict) Error() string {
	return "this proposal changed; refresh to see the current revision"
}
func answerProposal(w http.ResponseWriter, r *http.Request, err error) bool {
	var launched startedProposal
	if errors.As(err, &launched) {
		writeJSON(w, 409, map[string]any{"error": launched.Error(), "card_id": launched.cardID, "number": launched.number})
		return false
	}
	var stale proposalRevisionConflict
	if errors.As(err, &stale) {
		writeJSON(w, 409, map[string]any{"error": stale.Error(), "revision": stale.revision})
		return false
	}
	return answerCardCreate(w, r, err)
}

func lockedProposal(r *http.Request, tx *store.Tx, id, user uuid.UUID) (store.StudyProposal, error) {
	if id == uuid.Nil {
		return store.StudyProposal{}, cardInputError("proposal ID must be nonzero")
	}
	if err := lockCardCreation(r, tx); err != nil {
		return store.StudyProposal{}, err
	}
	p, err := tx.StudyProposal(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.StudyProposal{}, nil
	}
	if err == nil && p.OwnerID != user {
		return p, forbidden("only this proposal’s owner may change it")
	}
	return p, err
}

func (h *H) saveStudyProposal(w http.ResponseWriter, r *http.Request) {
	id, ok := cardID(w, r)
	if !ok {
		return
	}
	var in struct {
		store.ProposalContent
		FromSuggestion bool   `json:"from_suggestion"`
		Revision       *int64 `json:"revision"`
	}
	if err := readJSON(r, &in); err != nil || in.Revision == nil || *in.Revision < 0 {
		writeErr(w, 400, "invalid proposal or missing revision")
		return
	}
	study, err := proposalCard(in.ProposalContent)
	if !answerProposal(w, r, err) {
		return
	}
	var out store.StudyProposal
	err = h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) error {
		p, err := lockedProposal(r, tx, id, user)
		if err != nil {
			return err
		}
		if p.DiscardedAt != nil {
			return conflict("proposal discarded")
		}
		if p.CardID != nil {
			return startedProposal{*p.CardID, *p.CardNumber}
		}
		if p.LastSaveRevision != nil && *p.LastSaveRevision == *in.Revision && reflect.DeepEqual(p.ProposalContent, in.ProposalContent) {
			out = p
			return nil
		}
		if p.Revision != *in.Revision {
			return proposalRevisionConflict{p.Revision}
		}
		if p.ID == uuid.Nil {
			if err := h.validateSuggestion(r, tx, user, id, in.FromSuggestion); err != nil {
				return err
			}
		}
		if _, err := study.checkAssignment(r, tx, user); err != nil {
			return err
		}
		out, err = tx.WriteStudyProposal(r.Context(), id, user, *in.Revision, in.ProposalContent)
		if err != nil {
			return err
		}
		return tx.Emit(r.Context(), "study_proposal.saved", nil, store.User(user), map[string]any{"proposal_id": id, "revision": out.Revision})
	})
	if answerProposal(w, r, err) {
		writeJSON(w, 200, out)
	}
}

type proposalReceipt struct {
	ProposalID  uuid.UUID `json:"proposal_id"`
	OperationID uuid.UUID `json:"operation_id"`
	CardID      uuid.UUID `json:"card_id"`
	Number      int32     `json:"number"`
}

func (h *H) startStudyProposal(w http.ResponseWriter, r *http.Request) {
	id, ok := cardID(w, r)
	if !ok {
		return
	}
	var in struct {
		store.ProposalContent
		FromSuggestion bool                   `json:"from_suggestion"`
		OperationID    uuid.UUID              `json:"operation_id"`
		Revision       *int64                 `json:"revision"`
		Coresearch     *coresearchDraftSource `json:"coresearch,omitempty"`
	}
	if err := readJSON(r, &in); err != nil || in.OperationID == uuid.Nil || in.Revision == nil || *in.Revision < 0 {
		writeErr(w, 400, "invalid start request, operation ID or revision")
		return
	}
	rev := *in.Revision
	if in.Coresearch != nil && (in.FromSuggestion || rev != 0 || in.OperationID != id) {
		writeErr(w, 400, "invalid Coresearch draft start")
		return
	}
	if rev > 0 && !reflect.DeepEqual(in.ProposalContent, store.ProposalContent{}) {
		writeErr(w, 400, "save edits before starting this revision")
		return
	}
	var receipt proposalReceipt
	err := h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) error {
		p, err := lockedProposal(r, tx, id, user)
		if err != nil {
			return err
		}
		if p.DiscardedAt != nil {
			return conflict("proposal discarded")
		}
		if p.CardID != nil {
			if in.Coresearch != nil {
				if p.OperationID == nil || *p.OperationID != in.OperationID {
					return proposalRevisionConflict{p.Revision}
				}
			} else if rev != *p.LaunchRevision || (rev == 0 && !reflect.DeepEqual(p.ProposalContent, in.ProposalContent)) {
				return proposalRevisionConflict{p.Revision}
			}
			receipt = proposalReceipt{id, *p.OperationID, *p.CardID, *p.CardNumber}
			return nil
		}
		if in.Coresearch != nil {
			if err := validateCoresearchDraft(r.Context(), tx, user, id, *in.Coresearch); err != nil {
				return err
			}
		}
		if p.ID == uuid.Nil && rev > 0 {
			return pgx.ErrNoRows
		}
		if p.Revision != rev {
			return proposalRevisionConflict{p.Revision}
		}
		content := p.ProposalContent
		if rev == 0 {
			content = in.ProposalContent
		}
		study, err := proposalCard(content)
		if err != nil {
			return err
		}
		if rev == 0 {
			if err := h.validateSuggestion(r, tx, user, id, in.FromSuggestion); err != nil {
				return err
			}
			if _, err := tx.WriteStudyProposal(r.Context(), id, user, 0, content); err != nil {
				return err
			}
		}
		var c studyView
		if err := h.createCardTx(r, tx, user, study, nil, &c); err != nil {
			return err
		}
		if err := tx.RecordProposalLaunch(r.Context(), id, in.OperationID, c.ID, rev, c.Number); err != nil {
			if isUnique(err) {
				return conflict("operation ID already used by another proposal")
			}
			return err
		}
		receipt = proposalReceipt{id, in.OperationID, c.ID, c.Number}
		return tx.Emit(r.Context(), "study_proposal.started", &c.ID, store.User(user), receipt)
	})
	if answerProposal(w, r, err) {
		writeJSON(w, 200, receipt)
	}
}

func (h *H) discardStudyProposal(w http.ResponseWriter, r *http.Request) {
	id, ok := cardID(w, r)
	if !ok {
		return
	}
	rev, err := strconv.ParseInt(r.URL.Query().Get("revision"), 10, 64)
	if err != nil || rev < 1 {
		writeErr(w, 400, "a saved revision is required")
		return
	}
	err = h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) error {
		p, err := lockedProposal(r, tx, id, user)
		if err != nil {
			return err
		}
		if p.ID == uuid.Nil {
			return pgx.ErrNoRows
		}
		if p.CardID != nil {
			return startedProposal{*p.CardID, *p.CardNumber}
		}
		if p.DiscardedAt != nil && p.Revision-1 == rev {
			return nil
		}
		if p.DiscardedAt != nil || p.Revision != rev {
			return proposalRevisionConflict{p.Revision}
		}
		if err := tx.DiscardStudyProposal(r.Context(), id, rev); err != nil {
			return err
		}
		return tx.Emit(r.Context(), "study_proposal.discarded", nil, store.User(user), map[string]any{"proposal_id": id})
	})
	if answerProposal(w, r, err) {
		w.WriteHeader(http.StatusNoContent)
	}
}
