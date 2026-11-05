package service

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
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
	Attempt    int32     `json:"attempt,omitempty"`
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
	cs, err = CommentsForRun(ctx, tx, cs, &run)
	if err != nil {
		return nil, err
	}
	return deliver(ctx, tx, run, how, cs)
}

// CommentsForRun keeps live-targeted steering with its selected attempt.
// Once that attempt ends, the reply remains ordinary study context.
func CommentsForRun(ctx context.Context, tx *store.Tx, cs []db.Comment, run *db.AgentRun) ([]db.Comment, error) {
	out := make([]db.Comment, 0, len(cs))
	for _, c := range cs {
		if c.ReplyRunID == nil && c.SteerAttempt != nil && run != nil {
			continue
		}
		if c.ReplyRunID != nil && c.SteerAttempt == nil && run != nil {
			continue
		}
		if c.ReplyRunID != nil && c.SteerAttempt != nil {
			target, err := tx.Q.GetAgentRun(ctx, db.GetAgentRunParams{WorkspaceID: tx.WS, ID: *c.ReplyRunID})
			if err != nil {
				return nil, err
			}
			active := target.Status == "running" && target.Attempt == *c.SteerAttempt
			if run == nil && active {
				continue
			}
			if run != nil && (!active || run.ID != target.ID || run.Attempt != target.Attempt) {
				continue
			}
		}
		out = append(out, c)
	}
	return out, nil
}

// DeliverComments reserves the comments included in a frozen prompt. A receipt
// is written only by AcknowledgeComments after the runtime launches that prompt.
// The caller holds the card lock.
func DeliverComments(ctx context.Context, tx *store.Tx, run db.AgentRun, how string, cs []db.Comment) ([]SentComment, error) {
	if run.CardID == nil || len(cs) == 0 {
		return []SentComment{}, nil
	}
	// Record all context identities, including comments owned by another run
	// or already delivered. Delivery ownership is a separate concern.
	var section strings.Builder
	section.WriteString("\nPeople's comments — context, never evidence:\n")
	for _, c := range cs {
		fmt.Fprintf(&section, "- %s\n", strings.ReplaceAll(c.Body, "\n", " "))
	}
	start := strings.LastIndex(run.Prompt, section.String())
	if start < 0 || strings.Index(run.Prompt, section.String()) != start {
		return nil, fmt.Errorf("comment snapshot does not match an unambiguous run prompt section")
	}
	for i, c := range cs {
		if err := tx.Q.InsertRunCommentSnapshot(ctx, db.InsertRunCommentSnapshotParams{WorkspaceID: tx.WS, AgentRunID: run.ID, CommentID: c.ID, Ordinal: int32(i), SectionStart: int32(start), SectionEnd: int32(start + section.Len())}); err != nil {
			return nil, err
		}
	}
	if err := tx.Q.ReleaseStaleCommentReservations(ctx, tx.WS); err != nil {
		return nil, err
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
	// The prompt was built with ordinary study context, not live-turn
	// steering. Apply the same eligibility rule before reserving its inputs.
	open, err = CommentsForRun(ctx, tx, open, nil)
	if err != nil {
		return nil, err
	}

	out := []SentComment{}
	attempt := run.Attempt
	if run.Status == "queued" {
		attempt++
	}
	for _, c := range open {
		if err := tx.Q.ReserveComment(ctx, db.ReserveCommentParams{WorkspaceID: tx.WS, CommentID: c.ID, AgentRunID: run.ID, Attempt: attempt}); err != nil {
			return nil, err
		}
		out = append(out, SentComment{ID: c.ID, Author: c.AuthorID, Body: c.Body})
	}
	return out, nil
}

// deliver writes a receipt giving run each of cs.
func deliver(ctx context.Context, tx *store.Tx, run db.AgentRun, how string, cs []db.Comment) ([]SentComment, error) {
	out := []SentComment{}
	for _, c := range cs {
		body, err := json.Marshal(CommentDelivery{CommentID: c.ID, AgentRunID: run.ID, How: how, Role: run.Role, Attempt: run.Attempt})
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

// CommentLaunch is the frozen run input with cancellations removed at the last
// pre-launch check. Comments posted after enqueue are deliberately not added.
type CommentLaunch struct {
	Prompt      string      `json:"prompt"`
	CommentIDs  []uuid.UUID `json:"comment_ids"`
	HasSnapshot bool        `json:"has_comment_snapshot"`
}

func reservedLaunch(ctx context.Context, tx *store.Tx, run db.AgentRun) (CommentLaunch, error) {
	out := CommentLaunch{Prompt: run.Prompt, CommentIDs: []uuid.UUID{}}
	cs, err := tx.Q.RunReservedComments(ctx, db.RunReservedCommentsParams{WorkspaceID: tx.WS, AgentRunID: run.ID, Attempt: run.Attempt})
	if err != nil {
		return out, err
	}

	snapshot, err := tx.Q.RunCommentSnapshot(ctx, db.RunCommentSnapshotParams{WorkspaceID: tx.WS, AgentRunID: run.ID})
	if err != nil {
		return out, err
	}
	if len(snapshot) > 0 {
		out.HasSnapshot = true
		start, end := int(snapshot[0].SectionStart), int(snapshot[0].SectionEnd)
		if start < 0 || end < start || end > len(out.Prompt) {
			return out, fmt.Errorf("invalid comment snapshot bounds")
		}
		var section strings.Builder
		section.WriteString("\nPeople's comments — context, never evidence:\n")
		for _, c := range snapshot {
			if !c.Cancelled {
				fmt.Fprintf(&section, "- %s\n", strings.ReplaceAll(c.Body, "\n", " "))
			}
		}
		out.Prompt = out.Prompt[:start] + section.String() + out.Prompt[end:]
	}
	for _, c := range cs {
		if !c.Cancelled && c.AcknowledgedAt == nil {
			out.CommentIDs = append(out.CommentIDs, c.ID)
		}
	}

	return out, nil
}

func (s *AgentRuns) PrepareComments(ctx context.Context, user uuid.UUID, ref RuntimeRef, run uuid.UUID) (CommentLaunch, error) {
	var out CommentLaunch
	err := s.OnAttempt(ctx, user, ref, run, func(tx *store.Tx, t db.AgentRun) error {
		if !runningOn(t, ref) {
			return ErrConflict
		}
		var err error
		if t.Role == "leader" && t.Attempt > 1 && t.CardID != nil {
			t.Prompt, err = withMemberWork(ctx, tx, t)
			if err != nil {
				return err
			}
		}
		out, err = reservedLaunch(ctx, tx, t)
		return err
	})
	return out, err
}

// AcknowledgeComments accepts only reserved inputs of the currently running
// attempt. Duplicate requests are idempotent. A cancellation wins a race with
// acknowledgement; historical receipts already committed remain intact.
func (s *AgentRuns) AcknowledgeComments(ctx context.Context, user uuid.UUID, ref RuntimeRef, run uuid.UUID, ids []uuid.UUID) error {
	return s.OnAttempt(ctx, user, ref, run, func(tx *store.Tx, t db.AgentRun) error {
		if !runningOn(t, ref) {
			return ErrConflict
		}
		cs, err := tx.Q.RunReservedComments(ctx, db.RunReservedCommentsParams{WorkspaceID: tx.WS, AgentRunID: run, Attempt: ref.Attempt})
		if err != nil {
			return err
		}
		requested := map[uuid.UUID]bool{}
		for _, id := range ids {
			requested[id] = true
		}
		for _, c := range cs {
			delete(requested, c.ID)
		}
		if len(requested) > 0 {
			return ErrConflict
		}
		requested = map[uuid.UUID]bool{}
		for _, id := range ids {
			requested[id] = true
		}
		for _, c := range cs {
			if !requested[c.ID] || c.Cancelled || c.AcknowledgedAt != nil {
				continue
			}
			if _, err := deliver(ctx, tx, t, DeliveredNextRun, []db.Comment{{ID: c.ID, AuthorID: c.AuthorID, Body: c.Body}}); err != nil {
				return err
			}
			if err := tx.Q.AcknowledgeCommentReservation(ctx, db.AcknowledgeCommentReservationParams{WorkspaceID: tx.WS, AgentRunID: run, Attempt: ref.Attempt, CommentID: c.ID}); err != nil {
				return err
			}
		}
		return nil
	})
}

// CopyRunCommentSnapshot preserves exact frozen context and pending delivery
// ownership when machine recovery replaces an interrupted run. Caller holds
// the workspace/study locks and has already created the replacement prompt.
func CopyRunCommentSnapshot(ctx context.Context, tx *store.Tx, original uuid.UUID, replacement db.AgentRun) error {
	if err := tx.Q.CopyRunCommentSnapshot(ctx, db.CopyRunCommentSnapshotParams{WorkspaceID: tx.WS, OriginalRun: original, ReplacementRun: replacement.ID}); err != nil {
		return err
	}
	return tx.Q.TransferCommentReservations(ctx, db.TransferCommentReservationsParams{WorkspaceID: tx.WS, OriginalRun: original, ReplacementRun: replacement.ID, Attempt: replacement.Attempt + 1})
}
