// Package autoresearch runs a lab's saved rules: when a schedule, a webhook or a
// person fires one, it opens a study or runs one agent run with narrow
// powers. Every firing is a run, recorded once per idempotency key, acting as
// a named member of the workspace.
package autoresearch

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/flow"
	"lykeion/server/internal/service"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

type Service struct {
	Store     *store.Store
	AgentRuns *service.AgentRuns
	Engine    *flow.Engine // starts the studies a run opens; nil leaves them unstarted
	Key       []byte       // opens webhook signing secrets; nil when LYK_SECRET_KEY is unset
	Now       func() time.Time
}

var ErrAlreadyActive = errors.New("already_active: an active card from this Autoresearch has this title")

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Origin is what a card an Autoresearch opened records as its source.
func Origin(id uuid.UUID) string { return "autoresearch:" + id.String() }

// Admission is one firing of an Autoresearch, before it becomes a run.
type Admission struct {
	Autoresearch uuid.UUID
	Trigger      *uuid.UUID // nil for a manual run
	Source       string     // schedule | webhook | manual
	Actor        uuid.UUID  // the member the run acts as
	PlannedAt    time.Time
	Key          string          // the run's idempotency key
	Payload      json.RawMessage // the webhook envelope; nil for other sources
	Status       string          // "" (queued), or skipped for a firing that will not run
	Reason       string
}

func runEvent(r db.AutoresearchRun) map[string]any {
	return map[string]any{"run_id": r.ID, "autoresearch_id": r.AutoresearchID, "status": r.Status, "reason": r.Reason,
		"source": r.Source, "card_id": r.CardID, "agent_run_id": r.AgentRunID}
}

// lockWorkspace orders the execution row mutex before the event advisory lock,
// matching daemon claims before either path touches a child row.
func lockWorkspace(ctx context.Context, tx *store.Tx) error {
	if _, err := tx.Q.LockWorkspaceExecution(ctx, tx.WS); err != nil {
		return err
	}
	return tx.Q.LockWorkspaceEvents(ctx, tx.WS)
}

// Admit records a run once per key, in the caller's transaction. A second
// admission with the same key returns the first run and false.
func (s *Service) Admit(ctx context.Context, tx *store.Tx, a Admission) (db.AutoresearchRun, bool, error) {
	if err := lockWorkspace(ctx, tx); err != nil {
		return db.AutoresearchRun{}, false, err
	}
	status := a.Status
	if status == "" {
		status = "queued"
	}
	payload := a.Payload
	if payload == nil {
		payload = json.RawMessage(`{}`)
	}
	r, err := tx.Q.InsertRun(ctx, db.InsertRunParams{WorkspaceID: tx.WS, AutoresearchID: a.Autoresearch, TriggerID: a.Trigger,
		Source: a.Source, ActingUserID: a.Actor, PlannedAt: a.PlannedAt, Status: status, Reason: a.Reason,
		Payload: payload, IdempotencyKey: a.Key})
	if errors.Is(err, pgx.ErrNoRows) {
		r, err = tx.Q.RunByKey(ctx, db.RunByKeyParams{WorkspaceID: tx.WS, IdempotencyKey: a.Key})
		return r, false, err
	}
	if err != nil {
		return r, false, err
	}
	return r, true, tx.Emit(ctx, "autoresearch_run."+r.Status, nil, store.System, runEvent(r))
}

// setRun saves a run's status, reason, card and agent run, and emits the change.
func (s *Service) setRun(ctx context.Context, tx *store.Tx, r db.AutoresearchRun) error {
	if err := tx.Q.SetRun(ctx, db.SetRunParams{Status: r.Status, Reason: r.Reason, CardID: r.CardID, AgentRunID: r.AgentRunID,
		ID: r.ID, WorkspaceID: tx.WS}); err != nil {
		return err
	}
	return tx.Emit(ctx, "autoresearch_run."+r.Status, nil, store.System, runEvent(r))
}

// Dispatch carries out a queued run in its own transaction: it opens the card
// or queues the agent run, or ends the run with a reason. A run that is no longer
// queued, or already has its card or agent run, is left alone, so a repeated or
// concurrent dispatch acts once.
func (s *Service) Dispatch(ctx context.Context, ws, id uuid.UUID) error {
	err := s.dispatch(ctx, ws, id)
	if !errors.Is(err, ErrStartFailed) {
		return err
	}
	// The failed attempt left nothing behind; record why, so a retry does not
	// fail the same way again.
	return s.Store.InWorkspace(ctx, ws, uuid.Nil, func(tx *store.Tx) error {
		if err := lockWorkspace(ctx, tx); err != nil {
			return err
		}
		r, err := tx.Q.LockRun(ctx, db.LockRunParams{WorkspaceID: ws, ID: id})
		if err != nil || r.Status != "queued" {
			return err
		}
		r.Status, r.Reason = "failed", "assignee_unavailable"
		return s.setRun(ctx, tx, r)
	})
}

func (s *Service) dispatch(ctx context.Context, ws, id uuid.UUID) error {
	return s.Store.InWorkspace(ctx, ws, uuid.Nil, func(tx *store.Tx) error {
		if err := lockWorkspace(ctx, tx); err != nil {
			return err
		}
		r, err := tx.Q.LockRun(ctx, db.LockRunParams{WorkspaceID: ws, ID: id})
		if err != nil {
			return err
		}
		if r.Status != "queued" || r.CardID != nil || r.AgentRunID != nil {
			return nil
		}
		ap, err := tx.Q.GetAutoresearch(ctx, db.GetAutoresearchParams{WorkspaceID: ws, ID: r.AutoresearchID})
		if err != nil {
			return err
		}
		end := func(status, reason string) error {
			r.Status, r.Reason = status, reason
			return s.setRun(ctx, tx, r)
		}
		if ap.Status != "active" {
			return end("skipped", "paused")
		}
		// A run acts for a person; someone who has left the workspace cannot be
		// acted for, and their runtimes no longer serve it.
		_, err = tx.Q.GetMember(ctx, db.GetMemberParams{WorkspaceID: ws, UserID: r.ActingUserID})
		if errors.Is(err, pgx.ErrNoRows) {
			return end("failed", "actor_revoked")
		}
		if err != nil {
			return err
		}
		if ap.ExecutionMode == "run_only" {
			return s.runOnly(ctx, tx, ap, r)
		}
		return s.createCard(ctx, tx, ap, r)
	})
}

// RunNow is "Run now": a manual run that acts as the person who asked.
func (s *Service) RunNow(ctx context.Context, ws, autoresearch, actor uuid.UUID) (db.AutoresearchRun, error) {
	var r db.AutoresearchRun
	err := s.Store.InWorkspace(ctx, ws, actor, func(tx *store.Tx) error {
		var err error
		r, _, err = s.Admit(ctx, tx, Admission{Autoresearch: autoresearch, Source: "manual", Actor: actor,
			PlannedAt: s.now(), Key: "manual:" + uuid.NewString()})
		return err
	})
	if err != nil {
		return r, err
	}
	if err := s.Dispatch(ctx, ws, r.ID); err != nil {
		return r, err
	}
	err = s.Store.InWorkspace(ctx, ws, actor, func(tx *store.Tx) error {
		r, err = tx.Q.GetRun(ctx, db.GetRunParams{WorkspaceID: ws, ID: r.ID})
		return err
	})
	return r, err
}

// FailStudyDraft records a failed background dispatch and tells the requester.
func (s *Service) FailStudyDraft(ctx context.Context, ws, id uuid.UUID) error {
	return s.Store.InWorkspace(ctx, ws, uuid.Nil, func(tx *store.Tx) error {
		if err := lockWorkspace(ctx, tx); err != nil {
			return err
		}
		r, err := tx.Q.LockRun(ctx, db.LockRunParams{WorkspaceID: ws, ID: id})
		if err != nil || r.Status != "queued" {
			return err
		}
		draft, err := tx.LockStudyDraft(ctx, r.AutoresearchID)
		if err != nil || draft == nil {
			return err
		}
		ap, err := tx.Q.GetAutoresearch(ctx, db.GetAutoresearchParams{WorkspaceID: ws, ID: r.AutoresearchID})
		if err != nil {
			return err
		}
		r.Status, r.Reason = "failed", "dispatch_failed"
		if err := s.setRun(ctx, tx, r); err != nil {
			return err
		}
		return s.notify(ctx, tx, ap, nil, r.ID, "autoresearch_report", "The agent could not start creating the study. Please try again.")
	})
}

// notify gives the Autoresearch's owner and subscribers, those still members, an
// Inbox item about a run.
func (s *Service) notify(ctx context.Context, tx *store.Tx, ap db.Autoresearch, card *uuid.UUID, run uuid.UUID, kind, text string) error {
	users, err := tx.Q.AutoresearchRecipients(ctx, db.AutoresearchRecipientsParams{WorkspaceID: tx.WS, AutoresearchID: ap.ID})
	if err != nil {
		return err
	}
	for _, u := range users {
		if err := tx.Q.InsertAutoresearchInboxItem(ctx, db.InsertAutoresearchInboxItemParams{WorkspaceID: tx.WS, CardID: card,
			UserID: u, Kind: kind, Text: text, AutoresearchRunID: run}); err != nil {
			return err
		}
	}
	return nil
}
