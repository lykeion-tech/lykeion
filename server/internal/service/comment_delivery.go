package service

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/google/uuid"

	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

// How a comment reached a study's work: into a running turn, into the
// leader's member_result while it waited on a member, or in the prompt of the
// next run.
const (
	DeliveredRunningTurn  = "running_turn"
	DeliveredMemberResult = "member_result"
	DeliveredNextRun      = "next_run"
)

// CommentDelivery is a comment_delivery record's body: the receipt saying
// which run a comment went to, and how.
type CommentDelivery struct {
	CommentID  uuid.UUID `json:"comment_id"`
	AgentRunID uuid.UUID `json:"agent_run_id"`
	How        string    `json:"how"`
	Role       string    `json:"role"`
}

// SentComment is a comment as it is handed to a run.
type SentComment struct {
	ID     uuid.UUID `json:"id"`
	Author uuid.UUID `json:"author"`
	Body   string    `json:"body"`
}

// ClaimNewComments gives run, which is working, every comment on its study
// posted since its attempt started that no run has been given, writing one
// receipt each. The caller holds the card lock, so a comment is claimed once.
func ClaimNewComments(ctx context.Context, tx *store.Tx, run db.AgentRun, how string) ([]SentComment, error) {
	if run.CardID == nil || run.Status != "running" || run.StartedAt == nil {
		return []SentComment{}, nil
	}
	cs, err := tx.Q.UndeliveredComments(ctx, db.UndeliveredCommentsParams{WorkspaceID: tx.WS, CardID: *run.CardID, Since: *run.StartedAt})
	if err != nil {
		return nil, err
	}
	return deliver(ctx, tx, run, how, cs)
}

// DeliverComments writes a receipt giving run each of cs that has none yet,
// and returns those. The caller holds the card lock.
func DeliverComments(ctx context.Context, tx *store.Tx, run db.AgentRun, how string, cs []db.Comment) ([]SentComment, error) {
	if run.CardID == nil || len(cs) == 0 {
		return []SentComment{}, nil
	}
	open, err := tx.Q.UndeliveredComments(ctx, db.UndeliveredCommentsParams{WorkspaceID: tx.WS, CardID: *run.CardID, Since: time.Time{}})
	if err != nil {
		return nil, err
	}
	included := map[uuid.UUID]bool{}
	for _, c := range cs {
		included[c.ID] = true
	}
	open = slices.DeleteFunc(open, func(c db.Comment) bool { return !included[c.ID] })
	return deliver(ctx, tx, run, how, open)
}

// deliver writes a receipt giving run each of cs.
func deliver(ctx context.Context, tx *store.Tx, run db.AgentRun, how string, cs []db.Comment) ([]SentComment, error) {
	out := []SentComment{}
	for _, c := range cs {
		body, err := json.Marshal(CommentDelivery{CommentID: c.ID, AgentRunID: run.ID, How: how, Role: run.Role})
		if err != nil {
			return nil, err
		}
		if _, err := tx.Q.InsertRecord(ctx, db.InsertRecordParams{WorkspaceID: tx.WS, CardID: *run.CardID, BoardColumn: run.BoardColumn,
			Round: run.Round, Kind: "comment_delivery", Role: run.Role, Body: body, AgentRunID: &run.ID}); err != nil {
			return nil, err
		}
		out = append(out, SentComment{ID: c.ID, Author: c.AuthorID, Body: c.Body})
	}
	if len(out) > 0 {
		if err := tx.Emit(ctx, "comment.delivered", run.CardID, store.System, map[string]string{"agent_run_id": run.ID.String(), "how": how}); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// TakeComments claims, for the daemon running run's current attempt, the
// comments posted on its study since the attempt started.
func (s *AgentRuns) TakeComments(ctx context.Context, user uuid.UUID, ref RuntimeRef, run uuid.UUID) ([]SentComment, error) {
	out := []SentComment{}
	err := s.onAgentRun(ctx, user, ref, func(tx *store.Tx) error {
		t, err := tx.Q.GetAgentRun(ctx, db.GetAgentRunParams{ID: run, WorkspaceID: ref.WorkspaceID})
		if err != nil {
			return err
		}
		if t.Attempt != ref.Attempt {
			return ErrConflict
		}
		if t.RuntimeID == nil || *t.RuntimeID != ref.RuntimeID {
			return ErrNotYours
		}
		if t.CardID == nil || t.Status != "running" {
			return nil
		}
		if _, err := tx.Q.LockCard(ctx, db.LockCardParams{ID: *t.CardID, WorkspaceID: ref.WorkspaceID}); err != nil {
			return err
		}
		out, err = ClaimNewComments(ctx, tx, t, DeliveredRunningTurn)
		return err
	})
	return out, err
}
