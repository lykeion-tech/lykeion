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

	"lykeion/server/internal/store"
	"lykeion/server/internal/team"
	db "lykeion/server/pkg/db/generated"
)

// ErrStartFailed marks a study whose first column could not be started. The
// dispatch rolls back, card included, and ends the run failed.
var ErrStartFailed = errors.New("the study cannot start")

// createCard opens the run's study, which starts in its first column at once
// with the Autoresearch's assignee, or the built-in Teams where it names none.
func (s *Service) createCard(ctx context.Context, tx *store.Tx, ap db.Autoresearch, r db.AutoresearchRun) error {
	// Readying a Team takes the events lock, so the execution lock comes first.
	if err := lockWorkspace(ctx, tx); err != nil {
		return err
	}
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
	if ap.AssigneeType != nil && !s.assigneeReady(ctx, tx, ap, r.ActingUserID) {
		r.Status, r.Reason = "failed", "assignee_unavailable"
		return s.setRun(ctx, tx, r)
	}
	chosen, err := chosenTeams(ctx, tx, ap)
	if err != nil {
		return err
	}
	c, err := s.openCard(ctx, tx, ap, r, title, question, payload, ap.AssigneeID, chosen)
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

// openCard opens an Autoresearch run's study in the Autoresearch's scope and
// queues its first column's run in this transaction. The card is owned by the
// user the run acts as, so its agent runs run on that user's runtimes. It is
// refused while an active card this Autoresearch opened has the same title, and
// the Autoresearch's first card is also refused when it duplicates any active
// card, as a person's card would be. A webhook payload is kept as a round 0 record, which no attempt's
// gate reads as evidence. With agent set, the chosen Teams work their columns
// and the agent the rest; otherwise every column has a Team, the chosen one or
// the stage's built-in Team.
func (s *Service) openCard(ctx context.Context, tx *store.Tx, ap db.Autoresearch, run db.AutoresearchRun, title, question string, payload json.RawMessage, agent *uuid.UUID, chosen map[string]uuid.UUID) (db.Card, error) {
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
	_, err = tx.Q.LastOriginCard(ctx, db.LastOriginCardParams{WorkspaceID: tx.WS, Origin: origin})
	switch {
	case err == nil:
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
	teams := chosen
	if agent == nil {
		if teams, err = team.ColumnTeams(ctx, tx, ap.ScopeFrom, ap.ScopeTo, chosen); err != nil {
			return db.Card{}, fmt.Errorf("%w: %w", ErrStartFailed, err)
		}
		for _, p := range teams {
			if err := team.Ensure(ctx, tx, run.ActingUserID, p); err != nil {
				return db.Card{}, fmt.Errorf("%w: %w", ErrStartFailed, err)
			}
		}
	}
	assignee := "agent"
	if len(teams) > 0 {
		assignee = "team"
	}
	n, err := tx.Q.NextCardNumber(ctx, tx.WS)
	if err != nil {
		return db.Card{}, err
	}
	c, err := tx.Q.CreateCard(ctx, db.CreateCardParams{WorkspaceID: tx.WS, Number: n, Title: title, Question: question,
		OwnerID: run.ActingUserID, AssigneeType: &assignee, AssigneeID: agent,
		ScopeFrom: ap.ScopeFrom, ScopeTo: ap.ScopeTo, BoardColumn: ap.ScopeFrom, Round: 1})
	if err != nil {
		return db.Card{}, err
	}
	for col, p := range teams {
		if err := tx.Q.InsertCardTeam(ctx, db.InsertCardTeamParams{WorkspaceID: tx.WS, CardID: c.ID, BoardColumn: col, PackID: p}); err != nil {
			return db.Card{}, err
		}
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
		if _, err := tx.Q.InsertRecord(ctx, db.InsertRecordParams{WorkspaceID: tx.WS, CardID: c.ID, BoardColumn: c.BoardColumn,
			Round: 0, Kind: "trigger_payload", Role: "server", Key: run.ID.String(), Body: payload}); err != nil {
			return db.Card{}, err
		}
	}
	if s.Engine != nil {
		if err := s.Engine.Start(ctx, tx, c); err != nil {
			return db.Card{}, fmt.Errorf("%w: %v", ErrStartFailed, err)
		}
	}
	return c, s.notify(ctx, tx, ap, &c.ID, run.ID, "autoresearch_card", fmt.Sprintf("%s opened #%d %s", ap.Title, c.Number, c.Title))
}

// chosenTeams is the Team the Autoresearch names for each column.
func chosenTeams(ctx context.Context, tx *store.Tx, ap db.Autoresearch) (map[string]uuid.UUID, error) {
	rows, err := tx.Q.AutoresearchTeams(ctx, db.AutoresearchTeamsParams{WorkspaceID: tx.WS, AutoresearchID: ap.ID})
	out := map[string]uuid.UUID{}
	for _, t := range rows {
		out[t.BoardColumn] = t.PackID
	}
	return out, err
}

// assigneeReady is whether the person a run acts as can still work with the
// Autoresearch's assignee: an agent they may use, or each chosen Team readied for them
// (a built-in one is added on the spot, an uploaded one must already be).
func (s *Service) assigneeReady(ctx context.Context, tx *store.Tx, ap db.Autoresearch, actor uuid.UUID) bool {
	teams, err := tx.Q.AutoresearchTeams(ctx, db.AutoresearchTeamsParams{WorkspaceID: tx.WS, AutoresearchID: ap.ID})
	if err != nil {
		return false
	}
	for _, t := range teams {
		if team.Ensure(ctx, tx, actor, t.PackID) != nil {
			return false
		}
	}
	_, err = s.assignedAgent(ctx, tx, ap, actor)
	return err == nil
}
