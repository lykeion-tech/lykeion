package autoresearch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/board"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

// createCard opens the run's Backlog card. WIP pull pulls it under the
// WIP limit and gates move it from there; nothing here places it in a stage.
func (s *Service) createCard(ctx context.Context, tx *store.Tx, ap db.Autoresearch, r db.AutoresearchRun) error {
	title, err := s.title(ctx, tx, ap, r)
	if err != nil {
		return err
	}
	question := ap.Description
	if strings.TrimSpace(question) == "" {
		question = ap.Title
	}
	var payload json.RawMessage
	if r.Source == "webhook" {
		payload = r.Payload
	}
	c, err := s.openCard(ctx, tx, ap, r, title, question, payload)
	if errors.Is(err, ErrAlreadyActive) {
		r.Status, r.Reason = "skipped", "already_active"
		return s.setRun(ctx, tx, r)
	}
	if err != nil {
		return err
	}
	r.Status, r.Reason, r.CardID = "running", "", &c.ID
	return s.setRun(ctx, tx, r)
}

// title renders the title template, with {{date}} as the planned day in the
// trigger's time zone, or the acting user's for a manual run.
func (s *Service) title(ctx context.Context, tx *store.Tx, ap db.Autoresearch, r db.AutoresearchRun) (string, error) {
	if strings.TrimSpace(ap.TitleTemplate) == "" {
		return ap.Title, nil
	}
	tz := "UTC"
	if r.TriggerID != nil {
		t, err := tx.Q.GetTrigger(ctx, db.GetTriggerParams{WorkspaceID: tx.WS, ID: *r.TriggerID})
		if err != nil {
			return "", err
		}
		tz = t.Tz
	} else {
		u, err := tx.Q.GetUser(ctx, r.ActingUserID)
		if err != nil {
			return "", err
		}
		tz = u.Timezone
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc = time.UTC
	}
	return strings.ReplaceAll(ap.TitleTemplate, "{{date}}", r.PlannedAt.In(loc).Format("2006-01-02")), nil
}

// openCard opens a Backlog card for an Autoresearch run. The card is owned by
// the user the run acts as, so its agent runs run on that user's runtimes. It is
// refused while an active card this Autoresearch opened has the same title, and
// the Autoresearch's first card is also refused when it duplicates any active
// card, as a person's card would be. Each later card follows the one this
// Autoresearch opened last. A webhook payload is kept as a record in Backlog,
// where no gate reads it as evidence.
func (s *Service) openCard(ctx context.Context, tx *store.Tx, ap db.Autoresearch, run db.AutoresearchRun, title, question string, payload json.RawMessage) (db.Card, error) {
	// Two runs opening cards at once must see each other's card.
	if err := lockWorkspace(ctx, tx); err != nil {
		return db.Card{}, err
	}
	origin := Origin(ap.ID)
	dup, err := tx.Q.ActiveOriginDuplicate(ctx, db.ActiveOriginDuplicateParams{WorkspaceID: tx.WS, Origin: origin, Title: title})
	if err == nil {
		return db.Card{ID: dup.ID, Number: dup.Number}, ErrAlreadyActive
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return db.Card{}, err
	}
	follows := []uuid.UUID{}
	prev, err := tx.Q.LastOriginCard(ctx, db.LastOriginCardParams{WorkspaceID: tx.WS, Origin: origin})
	switch {
	case err == nil:
		follows = append(follows, prev)
	case !errors.Is(err, pgx.ErrNoRows):
		return db.Card{}, err
	default:
		d, err := tx.Q.ActiveDuplicate(ctx, db.ActiveDuplicateParams{WorkspaceID: tx.WS, Title: title, Question: question})
		if err == nil {
			return db.Card{ID: d.ID, Number: d.Number}, ErrAlreadyActive
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return db.Card{}, err
		}
	}
	n, err := tx.Q.NextCardNumber(ctx, tx.WS)
	if err != nil {
		return db.Card{}, err
	}
	c, err := tx.Q.CreateCard(ctx, db.CreateCardParams{WorkspaceID: tx.WS, Number: n, Title: title, Question: question,
		OwnerID: run.ActingUserID, FollowsFrom: follows})
	if err != nil {
		return db.Card{}, err
	}
	if err := tx.Q.SetCardOrigin(ctx, db.SetCardOriginParams{Origin: origin, ID: c.ID, WorkspaceID: tx.WS}); err != nil {
		return db.Card{}, err
	}
	c.Origin = origin
	if err := tx.Emit(ctx, "card.created", &c.ID, store.System, map[string]any{"number": c.Number, "title": c.Title,
		"origin": origin, "autoresearch_run_id": run.ID}); err != nil {
		return db.Card{}, err
	}
	if payload != nil {
		if _, err := tx.Q.InsertRecord(ctx, db.InsertRecordParams{WorkspaceID: tx.WS, CardID: c.ID, BoardColumn: board.Backlog,
			Round: 0, Kind: "trigger_payload", Role: "server", Key: run.ID.String(), Body: payload}); err != nil {
			return db.Card{}, err
		}
	}
	return c, s.notify(ctx, tx, ap, &c.ID, run.ID, "autoresearch_card", fmt.Sprintf("%s opened #%d %s", ap.Title, c.Number, c.Title))
}
