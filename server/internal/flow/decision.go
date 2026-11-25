package flow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/board"
	"lykeion/server/internal/fence"
	"lykeion/server/internal/handin"
	"lykeion/server/internal/jev"
	"lykeion/server/internal/review"
	"lykeion/server/internal/service"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

// holdForDecision raises the decision a study's stop asks its owner.
func holdForDecision(ctx context.Context, tx *store.Tx, c db.Card, kind, cause, text string, now time.Time) error {
	return service.HoldForDecision(ctx, tx, c, kind, cause, text, now)
}

var (
	ErrNotAnswerer    = errors.New("only the study's owner or a workspace owner or admin may answer this decision")
	ErrDecisionClosed = errors.New("this decision is no longer open")
	ErrNotWaiting     = errors.New("the study is no longer waiting on this decision")
)

// OptionError is an answer the decision does not accept.
type OptionError string

func (e OptionError) Error() string { return string(e) }

// Decide answers an open decision with one of its options. An agent's
// question resumes its lead's session with the answer. A stop's option is
// applied to the study: redo restarts the column with guidance as its note,
// close ends the study as a negative result, park leaves it blocked until
// someone redoes it and cancels its column's open runs, and try_again
// releases the runs its machines kept failing. The item is locked and
// rechecked under the study's lock, so of two answers at once, or an answer
// and its expiry, only the first applies; the other answer gets
// ErrDecisionClosed.
func (e *Engine) Decide(ctx context.Context, ws, item, user uuid.UUID, option, guidance string) error {
	return e.decide(ctx, ws, item, user, option, guidance, nil)
}

func (e *Engine) decide(ctx context.Context, ws, item, user uuid.UUID, option, guidance string, checked *approvalChecks) error {
	guidance = strings.TrimSpace(guidance)
	restart := false
	var card uuid.UUID
	var cancel []uuid.UUID
	var recheck *db.Card
	var evidence string
	var checkedAt time.Time
	var refreshing, resuming bool
	err := e.Store.InWorkspace(ctx, ws, user, func(tx *store.Tx) error {
		// The workspace, then the membership, as every member write takes them.
		if _, err := tx.Q.LockWorkspaceForMutation(ctx, ws); err != nil {
			return err
		}
		m, err := tx.Q.LockMember(ctx, db.LockMemberParams{WorkspaceID: ws, UserID: user})
		if err != nil {
			return err
		}
		row, err := tx.Q.GetDecision(ctx, db.GetDecisionParams{ID: item, WorkspaceID: ws, Viewer: user, IsAdmin: m.Role == "owner" || m.Role == "admin"})
		if err != nil {
			return err
		}
		if !row.MayAnswer {
			return ErrNotAnswerer
		}
		if row.InboxItem.CardID == nil {
			return ErrNotWaiting
		}
		card = *row.InboxItem.CardID
		// The study before its item, as the engine's stops take them.
		c, err := lockCard(ctx, tx, card)
		if err != nil {
			return err
		}
		it, err := tx.Q.LockDecision(ctx, db.LockDecisionParams{ID: item, WorkspaceID: ws})
		if err != nil {
			return err
		}
		if it.Status != "open" || it.ResolvedAt != nil || it.ArchivedAt != nil {
			return ErrDecisionClosed
		}
		var options []service.Option
		if err := json.Unmarshal(it.Options, &options); err != nil {
			return err
		}
		if !slices.ContainsFunc(options, func(o service.Option) bool { return o.ID == option }) {
			return OptionError("this decision has no option " + option)
		}
		agent := it.Kind == "agent_decision"
		if !agent && option == "redo" && guidance == "" {
			return OptionError("redo needs guidance")
		}
		if c.ID == uuid.Nil || c.Ending != "" || agent && !waitsOnQuestion(c) {
			return ErrNotWaiting
		}
		if !agent {
			held, err := heldBy(ctx, tx, c, it)
			if err != nil {
				return err
			}
			if !held {
				return ErrNotWaiting
			}
		}
		if option == "approve" && ptrOr(it.Cause) == "no_jev" {
			if checked == nil {
				evidence, err = approvalEvidence(ctx, tx, c.ID)
				if err != nil {
					return err
				}
				snapshot, err := loadApprovalSnapshot(ctx, tx, c, item)
				if err != nil {
					return err
				}
				if !snapshot.fresh(evidence, e.now()) {
					refreshing = true
					return refreshApprovalChecks(ctx, tx, c, item)
				}
				recheck, checkedAt = &c, snapshot.CheckedAt
				return nil
			}
			if c.Version != checked.version {
				return ErrNotWaiting
			}
			if err := verifyApprovalEvidence(ctx, tx, c.ID, checked.evidence); err != nil {
				var changed OptionError
				if !errors.As(err, &changed) {
					return err
				}
				refreshing = true
				return refreshApprovalChecks(ctx, tx, c, item)
			}
			if !approvalCheckIsRecent(checked.checkedAt, e.now()) {
				refreshing = true
				return refreshApprovalChecks(ctx, tx, c, item)
			}
			pending, err := hasPendingComposerSince(ctx, tx, c, it.CreatedAt)
			if err != nil {
				return err
			}
			if pending {
				return OptionError("New guidance is waiting to reach the study. Review the updated work before approving.")
			}
			if checked.resumeReview {
				if err := dismissKeylessApproval(ctx, tx, c.ID, item); err != nil {
					return err
				}
				if err := tx.Q.SaveCardFlow(ctx, db.SaveCardFlowParams{ID: c.ID, WorkspaceID: tx.WS,
					BoardColumn: c.BoardColumn, State: board.Working, Round: c.Round, Redos: c.Redos, HandoffNote: c.HandoffNote}); err != nil {
					return err
				}
				resuming = true
				if checked.retryCause != nil {
					if err := scoringUnavailable(ctx, tx, c, checked.retryCause); err != nil {
						return err
					}
				}
				return emitSteered(ctx, tx, c.ID, store.System)
			}
			if checked.legacyText != "" && it.Text != checked.legacyText {
				refreshing = true
				return refreshApprovalChecks(ctx, tx, c, item)
			}
			for _, status := range checked.validation {
				if err := writeServer(ctx, tx, c, "validation_status", statusBody{Hypothesis: status.Hypothesis,
					Status: status.Status, Reasons: status.Reasons, Cites: nonNil(status.Cites), Final: true}); err != nil {
					return err
				}
			}
		}
		decided, err := tx.Q.DecideItem(ctx, db.DecideItemParams{ID: item, WorkspaceID: ws, ChosenOption: option, Guidance: guidance, DecidedBy: user})
		if err != nil {
			return err
		}
		if err := tx.Emit(ctx, "inbox.resolved", &card, store.User(user), map[string]string{"inbox_item_id": item.String()}); err != nil {
			return err
		}
		if agent {
			err = e.resumeAfterDecision(ctx, tx, c, decided)
			if errors.Is(err, ErrPackUnavailable) || errors.Is(err, errNoAgent) {
				// askOwner tells the board itself.
				return e.askOwner(ctx, tx, c, "its question was answered and its lead could not be woken: "+err.Error())
			}
		} else {
			restart, cancel, err = e.applyOption(ctx, tx, c, it, user, option, guidance)
		}
		if err != nil || restart {
			// redo emits card.steered itself.
			return err
		}
		return emitSteered(ctx, tx, card, store.User(user))
	})
	if err != nil {
		return err
	}
	if recheck != nil {
		checked, err := e.checkOwnerApproval(ctx, ws, *recheck, item, evidence, checkedAt)
		if err != nil {
			return err
		}
		if !checked.resumeReview {
			if _, err := e.ownerJev(ctx, recheck.OwnerID); errors.Is(err, errUnreadableJevKey) {
				checked.resumeReview, checked.retryCause = true, err
			}
		}
		return e.decide(ctx, ws, item, user, option, guidance, &checked)
	}
	if resuming {
		if checked.retryCause == nil {
			e.Kick(ws, card)
			return fmt.Errorf("%w: Server checks found issues. The study is going back through review.", ErrNotWaiting)
		}
		return fmt.Errorf("%w: Server review will retry.%s", ErrNotWaiting, scoringUnavailableReason(checked.retryCause))
	}
	if refreshing {
		return fmt.Errorf("%w: Server checks are being refreshed. Lykeion will ask again if the checks allow approval.", ErrNotWaiting)
	}
	if restart {
		e.Kick(ws, card)
	}
	if option == "approve" {
		// A study its owner approved to the end gets its report checks, which
		// mark themselves unavailable without a TypeSafe key.
		e.goReport("check report", ws, card, e.checkReport)
	}
	return e.cancelParked(ctx, ws, user, cancel)
}

// An owner decision may reuse a complete check snapshot for up to one day.
const approvalChecksMaxAge = 24 * time.Hour

type approvalSnapshot struct {
	Decision  uuid.UUID `json:"approval_decision"`
	Revision  string    `json:"evidence_revision"`
	CheckedAt time.Time `json:"checked_at"`
	Complete  bool      `json:"complete"`
	Checks    any       `json:"checks,omitempty"`
}

func (s approvalSnapshot) fresh(revision string, now time.Time) bool {
	return s.Complete && s.Revision != "" && s.Revision == revision &&
		approvalCheckIsRecent(s.CheckedAt, now)
}

func approvalCheckIsRecent(checkedAt, now time.Time) bool {
	age := now.Sub(checkedAt)
	return !checkedAt.IsZero() && age >= 0 && age < approvalChecksMaxAge
}

func loadApprovalSnapshot(ctx context.Context, tx *store.Tx, c db.Card, decision uuid.UUID) (approvalSnapshot, error) {
	body, err := tx.Q.ApprovalChecksSnapshot(ctx, db.ApprovalChecksSnapshotParams{
		WorkspaceID: tx.WS, CardID: c.ID, BoardColumn: c.BoardColumn, Round: c.Round, Decision: decision.String(),
	})
	var snapshot approvalSnapshot
	if errors.Is(err, pgx.ErrNoRows) {
		return snapshot, nil
	}
	if err != nil {
		return snapshot, err
	}
	if json.Unmarshal(body, &snapshot) != nil || snapshot.Decision != decision {
		return approvalSnapshot{}, nil
	}
	return snapshot, nil
}

func checkedDecisionText(c db.Card, checks string, checkedAt time.Time) string {
	return noJevText(c.Number, c.BoardColumn, fmt.Sprintf("Server checks ran at %s. %s", checkedAt.UTC().Format("2 Jan 2006 15:04:05 UTC"), checks))
}

func refreshApprovalChecks(ctx context.Context, tx *store.Tx, c db.Card, item uuid.UUID) error {
	if err := dismissKeylessApproval(ctx, tx, c.ID, item); err != nil {
		return err
	}
	if err := writeServer(ctx, tx, c, "server_checks", map[string]bool{"approval_refresh": true}); err != nil {
		return err
	}
	if err := tx.Q.SaveCardFlow(ctx, db.SaveCardFlowParams{ID: c.ID, WorkspaceID: tx.WS, BoardColumn: c.BoardColumn,
		State: board.Working, Round: c.Round, Redos: c.Redos, HandoffNote: c.HandoffNote}); err != nil {
		return err
	}
	// The engine's background tick starts review. The request never waits
	// for a sources lookup, and the old approval is already closed.
	return emitSteered(ctx, tx, c.ID, store.System)
}

type approvalChecks struct {
	checkedAt    time.Time
	version      int64
	legacyText   string
	resumeReview bool
	retryCause   error
	evidence     string
	validation   []review.StatusResult
}

// Checks may need the sources service or saved files, so they run outside
// the approval transaction. The decision and card version are checked again
// before applying an approval.
func (e *Engine) checkOwnerApproval(ctx context.Context, ws uuid.UUID, c db.Card, decision uuid.UUID, evidence string, checkedAt time.Time) (approvalChecks, error) {
	out := approvalChecks{checkedAt: checkedAt, version: c.Version, evidence: evidence}
	// Grounding's checks require live providers and are completed entirely
	// in background review. The approval transaction still checks revision.
	if usesReview(c) && c.BoardColumn == board.Grounding {
		return out, nil
	}
	in, ok, err := e.readScoring(ctx, ws, c.ID, true)
	if err != nil {
		return out, err
	}
	if !ok || in.card.Version != c.Version {
		return out, ErrNotWaiting
	}
	in.decision, in.evidence = decision, evidence
	if !usesReview(c) {
		checks, err := e.legacyChecks(ctx, in)
		if err != nil {
			return out, err
		}
		out.legacyText = checkedDecisionText(c, checks.summary(), checkedAt)
		return out, e.persistChecks(ctx, ws, in, checks)
	}
	if c.BoardColumn == board.Validation {
		var replay bool
		var v validating
		err := e.Store.InWorkspace(ctx, ws, uuid.Nil, func(tx *store.Tx) error {
			study, err := studyRecords(ctx, tx, c.ID)
			if err != nil || handin.ModeOf(study) != modeRuns {
				return err
			}
			replay = true
			run, _, err := v.read(ctx, tx, c, study)
			if err == nil && run == nil {
				return ErrNotWaiting
			}
			return err
		})
		if err != nil {
			return out, err
		}
		if replay {
			checked, _, err := e.checkObjections(ctx, in, v)
			if err != nil {
				return out, err
			}
			if err := e.persistValidationChecks(ctx, ws, in, &v, checked); err != nil {
				return out, err
			}
			if !hasInvalidReplay(v) {
				out.validation = v.statuses
				return out, nil
			}
			moved, err := e.applyValidation(ctx, ws, in, v, nil, nil)
			return out, e.rejectCheckedApproval(ws, c.ID, moved, err)
		}
	}
	// Non-Grounding item reviews use local recorded evidence.
	res, _, err := e.deterministicReview(ctx, ws, in)
	if res.Column != "" {
		if err := e.persistChecks(ctx, ws, in, res); err != nil {
			return out, err
		}
	}
	if err != nil {
		out.resumeReview, out.retryCause = true, err
		return out, nil
	}
	if !stopsBeforeApproval(res) {
		return out, nil
	}
	if _, err := e.ownerJev(ctx, c.OwnerID); !errors.Is(err, errNoJev) {
		out.resumeReview, out.retryCause = true, err
		return out, nil
	}
	res.Judgment = "unavailable"
	moved, err := e.applyReview(ctx, ws, in, res, errNoJev)
	return out, e.rejectCheckedApproval(ws, c.ID, moved, err)
}

func (e *Engine) rejectCheckedApproval(ws, card uuid.UUID, moved bool, err error) error {
	if err != nil {
		return err
	}
	if moved {
		e.Kick(ws, card)
	}
	return OptionError("Server checks found issues. Read the updated study before continuing.")
}

// Card versions guard state changes. This revision also guards evidence and
// comments written since the first locked read, before their wake runs.
func approvalEvidence(ctx context.Context, tx *store.Tx, card uuid.UUID) (string, error) {
	return tx.Q.ApprovalEvidenceRevision(ctx, db.ApprovalEvidenceRevisionParams{WorkspaceID: tx.WS, CardID: card})
}

func verifyApprovalEvidence(ctx context.Context, tx *store.Tx, card uuid.UUID, original string) error {
	current, err := approvalEvidence(ctx, tx, card)
	if err != nil {
		return err
	}
	if current != original {
		return OptionError("Study inputs changed during the server checks. Review the updated work before approving.")
	}
	return nil
}

func (in scoring) checkApprovalEvidence(ctx context.Context, tx *store.Tx) error {
	return verifyApprovalEvidence(ctx, tx, in.card.ID, in.evidence)
}

// dismissKeylessApproval retires only the decision whose evidence failed
// rechecking. Other open decisions keep their own lifecycle.
func dismissKeylessApproval(ctx context.Context, tx *store.Tx, card, id uuid.UUID) error {
	n, err := tx.Q.DismissKeylessApproval(ctx, db.DismissKeylessApprovalParams{
		WorkspaceID: tx.WS, CardID: card, ID: id,
	})
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrDecisionClosed
	}
	return tx.Emit(ctx, "inbox.resolved", &card, store.System, map[string]string{"inbox_item_id": id.String()})
}

// heldBy is whether the study still waits on stop: a study blocked on its
// stop, outside a question or a park, or for runs its machines kept failing,
// a study that still has such runs held. A study that moved on is no longer
// the stop's to park or close.
func heldBy(ctx context.Context, tx *store.Tx, c db.Card, stop db.InboxItem) (bool, error) {
	if ptrOr(stop.Cause) == "machine_failures" {
		runs, err := service.ParkedRuns(ctx, tx, c.ID)
		return len(runs) > 0, err
	}
	return c.State == "blocked" && slices.Contains([]string{"", "low_score"}, c.BlockKind), nil
}

// waitsOnQuestion is whether the study is blocked on its lead's question.
func waitsOnQuestion(c db.Card) bool {
	return c.State == "blocked" && c.BlockKind == "decision"
}

// applyOption applies a stop's option to its locked study, for user, or for
// no one when the stop expired. It reports whether the column restarts and
// which runs to cancel once the caller commits.
func (e *Engine) applyOption(ctx context.Context, tx *store.Tx, c db.Card, decision db.InboxItem, user uuid.UUID, option, guidance string) (bool, []uuid.UUID, error) {
	actor := store.System
	if user != uuid.Nil {
		actor = store.User(user)
	}
	switch option {
	case "redo":
		// redo emits card.steered itself.
		restart, err := e.redo(ctx, tx, c, user, guidance)
		if err == nil && !restart {
			err = ErrNotWaiting
		}
		return restart, nil, err
	case "close":
		why := guidance
		if why == "" {
			why = c.Title
		}
		s := board.FromCard(c)
		board.Stop(&s, "closed as a negative result: "+why)
		if err := tx.Q.SaveCardBoard(ctx, s.Params(c.ID, tx.WS)); err != nil {
			return false, nil, err
		}
		return false, nil, service.EndStudyItems(ctx, tx, c.ID, actor)
	case "park":
		s := board.FromCard(c)
		board.Park(&s)
		if err := tx.Q.SaveCardBoard(ctx, s.Params(c.ID, tx.WS)); err != nil {
			return false, nil, err
		}
		// A parked study stops: its column's open runs, held ones
		// included, are cancelled once this commits.
		runs, err := tx.Q.RoundAgentRuns(ctx, db.RoundAgentRunsParams{WorkspaceID: tx.WS, CardID: c.ID, BoardColumn: c.BoardColumn, Round: c.Round})
		if err != nil {
			return false, nil, err
		}
		var cancel []uuid.UUID
		for _, r := range runs {
			if slices.Contains([]string{"queued", "dispatched", "running"}, r.Status) {
				cancel = append(cancel, r.ID)
			}
		}
		return false, cancel, nil
	case "try_again":
		return false, nil, service.ReleaseParkedRuns(ctx, tx, c.ID)
	case "approve":
		// Approval records an owner decision independently of Jev judgment.
		if err := writeServer(ctx, tx, c, "outcome", map[string]any{"verdict": "approved_by_owner", "by": user, "decision_id": decision.ID, "decision_cause": ptrOr(decision.Cause)}); err != nil {
			return false, nil, err
		}
		next := db.SaveCardFlowParams{ID: c.ID, WorkspaceID: tx.WS, BoardColumn: c.BoardColumn, State: board.Working, Round: c.Round, Redos: c.Redos}
		finished := advance(c, &next)
		if err := tx.Q.SaveCardFlow(ctx, next); err != nil {
			return false, nil, err
		}
		if finished {
			return false, nil, e.finishStudy(ctx, tx, c)
		}
		// The next column starts like a redo's, so approve emits card.steered itself.
		return true, nil, emitSteered(ctx, tx, c.ID, actor)
	}
	return false, nil, OptionError("option " + option + " cannot be applied")
}

// emitSteered tells the board where the study now stands.
func emitSteered(ctx context.Context, tx *store.Tx, card uuid.UUID, actor store.Actor) error {
	next, err := tx.Q.GetCard(ctx, db.GetCardParams{WorkspaceID: tx.WS, ID: card})
	if err != nil {
		return err
	}
	return tx.Emit(ctx, "card.steered", &card, actor, map[string]any{"column": next.BoardColumn, "state": next.State, "ending": next.Ending})
}

// cancelParked cancels a parked study's runs after the park commits, as
// steering does, each through the agent-run cancel path so its settlement is
// recorded. user is who the cancellations are recorded for.
func (e *Engine) cancelParked(ctx context.Context, ws, user uuid.UUID, runs []uuid.UUID) error {
	for _, run := range runs {
		if err := e.AgentRuns.Cancel(ctx, ws, user, run); err != nil && !errors.Is(err, service.ErrConflict) {
			return fmt.Errorf("the study was parked; cancelling its run %s failed: %w", run, err)
		}
	}
	return nil
}

func decisionAnswerNote(item db.InboxItem, withContext bool) (string, error) {
	title, body, _ := strings.Cut(item.Text, "\n")
	var note string
	if len(item.QuestionSet) > 0 {
		var set handin.QuestionSetIn
		var answers []QuestionAnswer
		if err := json.Unmarshal(item.QuestionSet, &set); err != nil {
			return "", err
		}
		if err := json.Unmarshal(item.Answers, &answers); err != nil {
			return "", err
		}
		note = "The researcher answered your question \"" + title + "\":" + questionSetAnswerText(set, answers, withContext)
	} else {
		var options []service.Option
		if err := json.Unmarshal(item.Options, &options); err != nil {
			return "", err
		}
		label := item.ChosenOption
		var consequence string
		if i := slices.IndexFunc(options, func(o service.Option) bool { return o.ID == item.ChosenOption }); i >= 0 {
			label = options[i].Label
			consequence = options[i].Consequence
		}
		note = "The researcher answered your question \"" + title + "\": " + label
		if withContext && consequence != "" {
			note += " — " + consequence
		}
	}
	if item.Guidance != "" {
		note += " — " + item.Guidance
	}
	if body = strings.TrimSpace(body); withContext && body != "" {
		note += "\nQuestion context: " + body
	}
	return note, nil
}

// resumeAfterDecision wakes the lead that asked item, once it is answered or
// expired: the study works again in the same round, so the lead's next run
// has the asking run's continuity scope and resumes its session, with the
// outcome as its note.
func (e *Engine) resumeAfterDecision(ctx context.Context, tx *store.Tx, c db.Card, item db.InboxItem) error {
	title, _, _ := strings.Cut(item.Text, "\n")
	note := "Your question \"" + title + "\" expired without an answer. Continue only with work that does not need that answer. Expiry does not authorize a choice the researcher reserved."
	if item.Status == "decided" {
		var err error
		note, err = decisionAnswerNote(item, false)
		if err != nil {
			return err
		}
		if item.DecidedBy != nil && item.DecidedAt != nil {
			u, err := tx.Q.GetUser(ctx, *item.DecidedBy)
			if err != nil {
				return err
			}
			who := u.Name
			if who == "" {
				who = u.Email
			}
			note += " — answered by " + who + " at " + item.DecidedAt.UTC().Format(time.RFC3339)
		}
	}
	// Who resumes is read before anything is written, so a lead that cannot
	// be woken leaves the study as it was.
	role, m, a, err := e.worker(ctx, tx, c)
	if err != nil {
		return err
	}
	if err := tx.Q.SaveCardFlow(ctx, db.SaveCardFlowParams{ID: c.ID, WorkspaceID: tx.WS, BoardColumn: c.BoardColumn, State: "working",
		BlockKind: "", Round: c.Round, Redos: c.Redos, HandoffNote: c.HandoffNote}); err != nil {
		return err
	}
	c.State, c.BlockKind = "working", ""
	_, err = e.enqueue(ctx, tx, c, role, m, a, note)
	return err
}

// sweepDecisions expires the decisions due by now, reminds once the day
// before a decision expires, and archives history untouched for 90 days. An
// expiring decision is locked and rechecked under its study's lock, as
// Decide takes them, so an answer and its expiry close it once between them.
func (e *Engine) sweepDecisions(ctx context.Context, now time.Time) error {
	var errs []error
	due, err := e.Store.Global().DueDecisions(ctx, now)
	if err != nil {
		return err
	}
	for _, d := range due {
		if err := e.expire(ctx, d, now); err != nil {
			errs = append(errs, fmt.Errorf("expire decision %s: %w", d.ID, err))
		}
	}
	remind, err := e.Store.Global().DueReminders(ctx, now)
	if err != nil {
		return errors.Join(append(errs, err)...)
	}
	for _, r := range remind {
		err := e.Store.InWorkspace(ctx, r.WorkspaceID, uuid.Nil, func(tx *store.Tx) error {
			card, err := tx.Q.MarkReminded(ctx, db.MarkRemindedParams{At: now, ID: r.ID, WorkspaceID: r.WorkspaceID})
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			if err != nil {
				return err
			}
			return tx.Emit(ctx, "decision.reminder", card, store.System, map[string]string{"inbox_item_id": r.ID.String()})
		})
		if err != nil {
			errs = append(errs, fmt.Errorf("remind decision %s: %w", r.ID, err))
		}
	}
	manual, err := e.Store.Global().DueManualReminders(ctx, now)
	if err != nil {
		errs = append(errs, err)
	} else {
		for _, r := range manual {
			if _, err := e.Store.Global().RemindManualDecision(ctx, db.RemindManualDecisionParams{WorkspaceID: r.WorkspaceID, ID: r.ID, At: now}); err != nil {
				errs = append(errs, fmt.Errorf("remind manual decision %s: %w", r.ID, err))
			}
		}
	}
	if err := e.Store.Global().ArchiveStale(ctx, now); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// expire closes one due decision: a stop applies its default option as the
// system, and an agent's question wakes its lead without an answer, or, when
// the lead cannot be woken, holds the study for its owner.
func (e *Engine) expire(ctx context.Context, d db.DueDecisionsRow, now time.Time) error {
	return e.Store.InWorkspace(ctx, d.WorkspaceID, uuid.Nil, func(tx *store.Tx) error {
		// The workspace, then the study's execution lock and the study, then
		// its item, as Decide takes them.
		if _, err := tx.Q.LockWorkspaceForMutation(ctx, d.WorkspaceID); err != nil {
			return err
		}
		c, err := lockCard(ctx, tx, d.CardID)
		if err != nil {
			return err
		}
		it, err := tx.Q.LockDecision(ctx, db.LockDecisionParams{ID: d.ID, WorkspaceID: d.WorkspaceID})
		if err != nil {
			return err
		}
		if it.Kind == "manual_decision" || it.Status != "open" || it.ResolvedAt != nil || it.ExpiresAt == nil || it.ExpiresAt.After(now) {
			return nil
		}
		expired, err := tx.Q.ExpireDecision(ctx, db.ExpireDecisionParams{ID: d.ID, WorkspaceID: d.WorkspaceID})
		if err != nil {
			return err
		}
		if err := tx.Emit(ctx, "inbox.resolved", it.CardID, store.System, map[string]string{"inbox_item_id": d.ID.String()}); err != nil {
			return err
		}
		// A study already stopped, or no longer waiting on the question,
		// only has its item closed.
		if c.ID == uuid.Nil || c.Ending != "" {
			return nil
		}
		switch {
		case it.Kind == "agent_decision":
			if !waitsOnQuestion(c) {
				return nil
			}
			err = e.resumeAfterDecision(ctx, tx, c, expired)
			if errors.Is(err, ErrPackUnavailable) || errors.Is(err, errNoAgent) {
				// askOwner tells the board itself.
				return e.askOwner(ctx, tx, c, "its question expired and its lead could not be woken: "+err.Error())
			}
		case it.DefaultOption != "":
			held, herr := heldBy(ctx, tx, c, it)
			if herr != nil || !held {
				return herr
			}
			var cancel []uuid.UUID
			if _, cancel, err = e.applyOption(ctx, tx, c, it, uuid.Nil, it.DefaultOption, ""); err != nil {
				return err
			}
			// No one chose the default, so its runs are cancelled here, as
			// the system, rather than after commit as a person.
			err = e.cancelInTx(ctx, tx, cancel)
		default:
			return nil
		}
		if err != nil {
			return err
		}
		return emitSteered(ctx, tx, c.ID, store.System)
	})
}

// cancelInTx cancels runs inside the caller's transaction as the system,
// each recording its settlement; one that already ended is left alone.
func (e *Engine) cancelInTx(ctx context.Context, tx *store.Tx, runs []uuid.UUID) error {
	for _, id := range runs {
		r, err := tx.Q.LockAgentRun(ctx, db.LockAgentRunParams{WorkspaceID: tx.WS, ID: id})
		if err != nil {
			return err
		}
		if err := e.AgentRuns.CancelInTx(ctx, tx, uuid.Nil, r); err != nil && !errors.Is(err, service.ErrConflict) {
			return err
		}
	}
	return nil
}

// errDecisionOpen is the failed hand-in of a lead that asks while its study
// already waits on one of its questions.
const errDecisionOpen = "one decision is already open on this study"

// errDecideTwice is the failed hand-in of a lead whose question Jev sent
// back once already in this round.
const errDecideTwice = "Jev judged two questions in a row as the agent's own to decide"

// decideYourself introduces the note that sends a question back to its lead.
const decideYourself = "Decide this yourself: "

// fitQuestion is what Jev is asked of an agent's question before it reaches
// the researcher.
const fitQuestion = "Does this question belong to the kind it is tagged with, and is it one only the researcher can answer?"

// Jev's verdicts on an agent's question; unavailable is no Jev, or a Jev
// that failed, and leaves the server's checks alone.
const (
	fitUnavailable = ""
	fitFits        = "fits"
	fitDecide      = "decide_yourself"
)

var fitOptions = map[string]string{
	fitFits:   "It is of its tagged kind and only the researcher can settle it. A choice explicitly reserved for the researcher in the study brief fits scope, even if it concerns the method.",
	fitDecide: "It is not of its tagged kind, or it is a choice of method, tools, analysis, evidence or a retry that the brief left to the agent. Do not assume the researcher reserved a choice based only on what the agent says.",
}

// fitCheck asks Jev whether an agent's question is the researcher's to
// answer. It runs with no transaction open.
func (e *Engine) fitCheck(ctx context.Context, j *jev.Client, card uuid.UUID, brief string, d handin.DecisionIn) string {
	questions := []handin.DecisionIn{d}
	if d.QuestionSet != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Minute)
		defer cancel()
		questions = make([]handin.DecisionIn, 0, len(d.QuestionSet.Questions))
		for _, q := range d.QuestionSet.Questions {
			one := d
			one.QuestionSet = &handin.QuestionSetIn{Questions: []handin.QuestionIn{q}}
			questions = append(questions, one)
		}
	}
	result := fitFits
	for _, question := range questions {
		if ctx.Err() != nil {
			return fitUnavailable
		}
		state := fence.Untrusted("Researcher's study brief", brief) + fence.Untrusted("Agent's proposed decision", decisionState(question))
		a, err := j.Choice(ctx, state, fitQuestion, fitOptions)
		if err != nil {
			slog.Warn("jev fit check failed; raising the decision on the server's checks", "card", card, "err", err)
			result = fitUnavailable
			continue
		}
		if a.Choice == fitDecide {
			return fitDecide
		}
	}
	return result
}

// raiseAgentDecision puts a lead's question to the study's owner: the study
// is blocked on it in the same round, so the lead resumes its session once
// it is answered. A study waits on one question at a time, so a second ask
// fails the hand-in. When Jev judged the question not the researcher's, the
// lead runs again in the same round and decides it itself; a second such
// question in the round fails the hand-in.
func (e *Engine) raiseAgentDecision(ctx context.Context, tx *store.Tx, c db.Card, run db.AgentRun, d handin.DecisionIn, fit string, now time.Time) error {
	open, err := tx.Q.HasOpenAgentDecision(ctx, db.HasOpenAgentDecisionParams{WorkspaceID: tx.WS, CardID: c.ID})
	if err != nil {
		return err
	}
	if open {
		run.Error = errDecisionOpen
		return e.runFailed(ctx, tx, c, run)
	}
	if fit == fitDecide {
		runs, err := tx.Q.RoundAgentRuns(ctx, db.RoundAgentRunsParams{WorkspaceID: tx.WS, CardID: c.ID, BoardColumn: c.BoardColumn, Round: c.Round})
		if err != nil {
			return err
		}
		// The runs a question was sent back by carry the note as their assignment.
		if slices.ContainsFunc(runs, func(r db.AgentRun) bool { return leads(r.Role) && strings.HasPrefix(r.Assignment, decideYourself) }) {
			run.Error = errDecideTwice
			return e.runFailed(ctx, tx, c, run)
		}
		role, m, ag, err := e.worker(ctx, tx, c)
		if err != nil {
			return err
		}
		_, err = e.enqueue(ctx, tx, c, role, m, ag, decideYourself+d.Title)
		return err
	}
	options := make([]service.Option, len(d.Options))
	for i, o := range d.Options {
		options[i] = service.Option{ID: o.ID, Label: o.Label, Consequence: o.Consequence}
	}
	raw, err := json.Marshal(options)
	if err != nil {
		return err
	}
	var questions json.RawMessage
	if d.QuestionSet != nil {
		questions, err = json.Marshal(d.QuestionSet)
		if err != nil {
			return err
		}
	}
	expires := now.Add(service.DecisionWindow)
	if d.ExpiresAt != nil {
		expires = *d.ExpiresAt
	}
	if err := tx.Q.InsertAgentDecision(ctx, db.InsertAgentDecisionParams{WorkspaceID: tx.WS, CardID: c.ID, UserID: c.OwnerID,
		Text: d.Title + "\n\n" + d.Body, Options: raw, QuestionSet: questions, ExpiresAt: &expires, RaisedRunID: run.ID, FromMember: ptrOr(d.FromMember), DecisionKind: d.Kind}); err != nil {
		return err
	}
	if err := tx.Q.SaveCardFlow(ctx, db.SaveCardFlowParams{ID: c.ID, WorkspaceID: tx.WS, BoardColumn: c.BoardColumn, State: "blocked",
		BlockKind: "decision", Round: c.Round, Redos: c.Redos, HandoffNote: c.HandoffNote}); err != nil {
		return err
	}
	if err := tx.Emit(ctx, "inbox.created", &c.ID, store.System, map[string]any{"card_id": c.ID}); err != nil {
		return err
	}
	return tx.Emit(ctx, "card.steered", &c.ID, store.System, map[string]any{"column": c.BoardColumn, "state": "blocked"})
}

// decisionState is an agent's question as Jev reads it.
func decisionState(d handin.DecisionIn) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Kind: %s\nTitle: %s\n\n%s\n", d.Kind, d.Title, d.Body)
	if d.QuestionSet != nil {
		for _, q := range d.QuestionSet.Questions {
			fmt.Fprintf(&b, "\nQuestion: %s\n", q.Prompt)
			if q.HelpText != nil {
				fmt.Fprintf(&b, "%s\n", *q.HelpText)
			}
			for _, o := range q.Options {
				fmt.Fprintf(&b, "- %s: %s\n", o.Label, o.Consequence)
			}
		}
	} else {
		b.WriteString("\nOptions:\n")
		for _, o := range d.Options {
			fmt.Fprintf(&b, "- %s: %s\n", o.Label, o.Consequence)
		}
	}
	return b.String()
}
