package flow

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/board"
	"lykeion/server/internal/pack"
	"lykeion/server/internal/records"
	"lykeion/server/internal/service"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

var (
	ErrUndeclaredMember = errors.New("the Team's manifest declares no such member")
	ErrStepCap          = errors.New("step cap reached: finish with lyk team done or do the step yourself")
	ErrInFlight         = errors.New("a member's run is still in flight: wait until its work lands")
	ErrMovedOn          = errors.New("the study is no longer working this run's column")
	ErrDeclaredDone     = errors.New("this column was already declared done")
)

// idleNote wakes a leader that stopped without naming a member or finishing,
// and agentIdleNote a study's single agent that stopped without finishing.
const (
	idleNote      = "You ended without naming a member (`lyk team step`) or declaring the column done (`lyk team done`)."
	agentIdleNote = "You ended without declaring the column done (`lyk team done`)."
)

const commentNote = "A researcher commented on the study (see the comments above). Read what they said and carry on."

// excerptLen is how much of a record's body or a call's answer a prompt or
// listing shows; the rest is read with lyk records.
const excerptLen = 2000

// Excerpt cuts s to at most n bytes on a character boundary and says whether
// it cut anything.
func Excerpt(s string, n int) (string, bool) {
	if len(s) <= n {
		return s, false
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n], true
}

// working says whether the engine acts on the study: it works a stage column
// and nothing ended, flagged or blocked it.
func working(c db.Card) bool {
	return c.Ending == "" && c.Flag == "" && c.State == board.Working && board.StageIndex(c.BoardColumn) >= 0
}

func current(c db.Card, column string, round int32) bool {
	return working(c) && c.BoardColumn == column && c.Round == round
}

// leads says whether a run's role works a column through to done: a Team's
// leader, or a study's single agent on a column no Team works.
func leads(role string) bool { return role == "leader" || role == "agent" }

// isMember says whether a run's role is a Team member's name rather than the
// leader, the auditor or a study's single agent.
func isMember(role string) bool {
	return role != "" && role != "leader" && role != "auditor" && role != "agent"
}

func waitingToStart(status string) bool { return status == "queued" || status == "dispatched" }

func inFlight(status string) bool {
	return status == "queued" || status == "dispatched" || status == "running"
}

// openColumn reads the study a run's role command acts on and refuses it
// when the study has left the run's column and attempt, or when that column
// was already declared done.
func openColumn(ctx context.Context, tx *store.Tx, w service.Writer) (db.Card, error) {
	c, err := tx.Q.GetCard(ctx, db.GetCardParams{WorkspaceID: tx.WS, ID: w.Card})
	if err != nil {
		return c, err
	}
	if !current(c, w.Column, w.Round) {
		return c, ErrMovedOn
	}
	done, err := tx.Q.CountRoundKindRecords(ctx, db.CountRoundKindRecordsParams{WorkspaceID: tx.WS, CardID: c.ID,
		BoardColumn: c.BoardColumn, Round: c.Round, Kind: "summary"})
	if err == nil && done > 0 {
		err = ErrDeclaredDone
	}
	return c, err
}

// SelfStep records that the leader does a step itself.
func (e *Engine) SelfStep(ctx context.Context, tx *store.Tx, w service.Writer, step string) (db.Record, error) {
	if _, err := openColumn(ctx, tx, w); err != nil {
		return db.Record{}, err
	}
	return records.WriteAs(ctx, tx, w, "self_step", map[string]string{"step": step})
}

// Delegate starts the run of the column Team's member for one step the
// leader names and records the delegation. w is the leader's run, whose card
// lock the caller holds.
func (e *Engine) Delegate(ctx context.Context, tx *store.Tx, w service.Writer, member, step string) (db.Record, error) {
	c, err := openColumn(ctx, tx, w)
	if err != nil {
		return db.Record{}, err
	}
	packID, m, err := columnTeam(ctx, tx, c.ID, c.BoardColumn)
	if errors.Is(err, pgx.ErrNoRows) {
		return db.Record{}, ErrUndeclaredMember
	}
	if err != nil {
		return db.Record{}, err
	}
	if !slices.ContainsFunc(m.Members, func(x pack.Member) bool { return x.Name == member }) {
		return db.Record{}, ErrUndeclaredMember
	}
	n, err := tx.Q.CountRoundKindRecords(ctx, db.CountRoundKindRecordsParams{WorkspaceID: tx.WS, CardID: c.ID,
		BoardColumn: c.BoardColumn, Round: c.Round, Kind: "delegation"})
	if err != nil {
		return db.Record{}, err
	}
	steps := m.Steps
	if steps == 0 {
		steps = pack.DefaultSteps
	}
	if n >= int64(steps) {
		return db.Record{}, ErrStepCap
	}
	a, err := teamAgent(ctx, tx, packID, c.OwnerID, member)
	if err != nil {
		return db.Record{}, err
	}
	run, err := e.enqueue(ctx, tx, c, member, m, a, "Your step, from the leader:\n"+step, false)
	if err != nil {
		return db.Record{}, err
	}
	return records.WriteAs(ctx, tx, w, "delegation", map[string]any{"member": member, "step": step, "agent_run_id": run.ID})
}

// Finish stores the summary with which the leader, or a study's single
// agent, declares its column done. Every cited key must name a live record
// of the study, and no member's run may still be in flight.
func (e *Engine) Finish(ctx context.Context, tx *store.Tx, w service.Writer, summary string, cites []string) (db.Record, error) {
	c, err := openColumn(ctx, tx, w)
	if err != nil {
		return db.Record{}, err
	}
	runs, err := tx.Q.RoundAgentRuns(ctx, db.RoundAgentRunsParams{WorkspaceID: tx.WS, CardID: c.ID, BoardColumn: c.BoardColumn, Round: c.Round})
	if err != nil {
		return db.Record{}, err
	}
	for _, r := range runs {
		if isMember(r.Role) && inFlight(r.Status) {
			return db.Record{}, ErrInFlight
		}
	}
	if cites == nil {
		cites = []string{}
	}
	found, err := tx.Q.LiveRecordKeys(ctx, db.LiveRecordKeysParams{WorkspaceID: tx.WS, CardID: c.ID, Keys: cites})
	if err != nil {
		return db.Record{}, err
	}
	for _, k := range cites {
		if !slices.Contains(found, k) {
			return db.Record{}, service.Invalid(fmt.Sprintf("no record of this study has the key %q", k))
		}
	}
	return records.WriteAs(ctx, tx, w, "summary", map[string]any{"text": summary, "cites": cites})
}

// DropWakes cancels the leader (or single agent) runs still waiting to start on the column and
// attempt a study declared done: work that landed during the leader's last
// turn queued them, and the column takes no more steps. column and round are
// the declaring run's, since scoring may already have moved the study.
func (e *Engine) DropWakes(ctx context.Context, ws, card uuid.UUID, column string, round int32) error {
	var owner uuid.UUID
	var waiting []uuid.UUID
	err := e.Store.InWorkspace(ctx, ws, uuid.Nil, func(tx *store.Tx) error {
		c, err := tx.Q.GetCard(ctx, db.GetCardParams{WorkspaceID: ws, ID: card})
		if err != nil {
			return err
		}
		owner = c.OwnerID
		runs, err := tx.Q.RoundAgentRuns(ctx, db.RoundAgentRunsParams{WorkspaceID: ws, CardID: card, BoardColumn: column, Round: round})
		for _, r := range runs {
			if leads(r.Role) && waitingToStart(r.Status) {
				waiting = append(waiting, r.ID)
			}
		}
		return err
	})
	if err != nil {
		return err
	}
	for _, id := range waiting {
		if err := e.AgentRuns.Cancel(ctx, ws, owner, id); err != nil && !errors.Is(err, service.ErrConflict) {
			return err
		}
	}
	return nil
}

// lockCard takes the locks Advance takes, waiting for the card's, and reads
// the card; a deleted card reads as not working.
func lockCard(ctx context.Context, tx *store.Tx, id uuid.UUID) (db.Card, error) {
	if _, err := tx.Q.LockWorkspaceExecution(ctx, tx.WS); err != nil {
		return db.Card{}, err
	}
	if err := tx.Q.WaitLockCard(ctx, id); err != nil {
		return db.Card{}, err
	}
	c, err := tx.Q.LockCard(ctx, db.LockCardParams{ID: id, WorkspaceID: tx.WS})
	if errors.Is(err, pgx.ErrNoRows) {
		return db.Card{}, nil
	}
	return c, err
}

// afterRun wakes the leader when a member's run settles, with what the
// member wrote. A leader or single-agent run that ends without naming a
// member or declaring the column done is woken once with its idle note;
// ending that way again leaves the study working and escalates it to its
// owner.
func (e *Engine) afterRun(ctx context.Context, t db.AgentRun) error {
	if (leads(t.Role) && t.Status == "cancelled") || (!leads(t.Role) && !isMember(t.Role)) {
		return nil
	}
	return e.Store.InWorkspace(ctx, t.WorkspaceID, uuid.Nil, func(tx *store.Tx) error {
		c, err := lockCard(ctx, tx, *t.CardID)
		if err != nil || !current(c, t.BoardColumn, t.Round) {
			return err
		}
		recs, err := tx.Q.AgentRunRecords(ctx, db.AgentRunRecordsParams{WorkspaceID: tx.WS, AgentRunID: &t.ID})
		if err != nil {
			return err
		}
		if isMember(t.Role) {
			return e.wake(ctx, tx, c, memberReport(t, recs))
		}
		for _, r := range recs {
			if r.Kind == "delegation" || r.Kind == "summary" {
				return nil
			}
		}
		// A member still at work, or a run of this role already waiting,
		// wakes it in its own time.
		runs, err := tx.Q.RoundAgentRuns(ctx, db.RoundAgentRunsParams{WorkspaceID: tx.WS, CardID: c.ID, BoardColumn: c.BoardColumn, Round: c.Round})
		if err != nil {
			return err
		}
		for _, r := range runs {
			if (isMember(r.Role) && inFlight(r.Status)) || (r.Role == t.Role && waitingToStart(r.Status)) {
				return nil
			}
		}
		note, who := idleNote, "The leader stopped twice without naming a member or declaring"
		if t.Role == "agent" {
			note, who = agentIdleNote, "The study's agent stopped twice without declaring"
		}
		if !strings.Contains(t.Prompt, note) {
			return e.wake(ctx, tx, c, note)
		}
		return tx.Q.InsertInboxItem(ctx, db.InsertInboxItemParams{WorkspaceID: tx.WS, CardID: c.ID, UserID: c.OwnerID, Kind: "escalated",
			Text: fmt.Sprintf("%s the %s column done. Comment on the study to wake it.", who, title(c.BoardColumn))})
	})
}

// OnComment wakes the leader of a column that has started, or the study's
// single agent, when someone comments on the study, as a squad leader is
// woken by a comment, and the comment answers any escalation. The owner's
// comment on a study blocked by low scores restarts its column instead. Like
// Kick it runs off the caller's request, after the comment commits.
func (e *Engine) OnComment(ws, card, author uuid.UUID, body string) {
	run := func() {
		if err := e.wakeOnComment(context.Background(), ws, card, author, body); err != nil {
			slog.Error("wake on comment", "card", card, "err", err)
		}
	}
	if e.Inline {
		run()
		return
	}
	go run()
}

func (e *Engine) wakeOnComment(ctx context.Context, ws, card, author uuid.UUID, body string) error {
	restart := false
	err := e.Store.InWorkspace(ctx, ws, uuid.Nil, func(tx *store.Tx) error {
		c, err := lockCard(ctx, tx, card)
		if err != nil {
			return err
		}
		if restart, err = unblock(ctx, tx, c, author, body); err != nil || restart || !working(c) {
			return err
		}
		runs, err := tx.Q.RoundAgentRuns(ctx, db.RoundAgentRunsParams{WorkspaceID: ws, CardID: c.ID, BoardColumn: c.BoardColumn, Round: c.Round})
		if err != nil || len(runs) == 0 {
			return err // Advance starts the column, and the first prompt carries the comment
		}
		if err := e.wake(ctx, tx, c, commentNote); err != nil {
			return err
		}
		return resolveInbox(ctx, tx, c.ID, "escalated")
	})
	if err == nil && restart {
		e.Kick(ws, card)
	}
	return err
}

// wake queues the next run of the column's leader, or of the study's single
// agent where no Team works it, which resumes its session, with note after
// the study. Like a squad leader's trigger it is dropped while that role
// already has a run waiting to start; a running one does not drop it, so work
// landing during its last turn is never lost. A column already declared done
// wakes no one.
func (e *Engine) wake(ctx context.Context, tx *store.Tx, c db.Card, note string) error {
	done, err := tx.Q.CountRoundKindRecords(ctx, db.CountRoundKindRecordsParams{WorkspaceID: tx.WS, CardID: c.ID,
		BoardColumn: c.BoardColumn, Round: c.Round, Kind: "summary"})
	if err != nil || done > 0 {
		return err
	}
	role, m, a, err := e.worker(ctx, tx, c)
	if errors.Is(err, errNoAgent) {
		return nil
	}
	if err != nil {
		return err
	}
	runs, err := tx.Q.RoundAgentRuns(ctx, db.RoundAgentRunsParams{WorkspaceID: tx.WS, CardID: c.ID, BoardColumn: c.BoardColumn, Round: c.Round})
	if err != nil {
		return err
	}
	for _, r := range runs {
		if r.Role == role && waitingToStart(r.Status) {
			return nil
		}
	}
	_, err = e.enqueue(ctx, tx, c, role, m, a, note, false)
	return err
}

// memberReport tells the leader what a member's settled run left: its
// outcome and the records it wrote.
func memberReport(t db.AgentRun, recs []db.Record) string {
	var b strings.Builder
	switch t.Status {
	case "completed":
		fmt.Fprintf(&b, "%s finished the step you named.\n", t.Role)
	case "cancelled":
		fmt.Fprintf(&b, "%s's run was cancelled before it finished the step.\n", t.Role)
	default:
		fmt.Fprintf(&b, "%s's run failed: %s\n", t.Role, strings.TrimSpace(t.Error+" "+ptrOr(t.FailureReason)))
	}
	for _, r := range recs {
		if r.Kind == "failed" {
			fmt.Fprintf(&b, "It could not do the step: %s\n", records.Body[struct{ Reason string }](r).Reason)
		}
	}
	if len(recs) == 0 {
		b.WriteString("It wrote no records.\n")
	} else {
		b.WriteString("What it wrote:\n")
		for _, r := range recs {
			body, cut := Excerpt(string(r.Body), excerptLen)
			if cut {
				body += " …"
			}
			fmt.Fprintf(&b, "- %s (%s): %s\n", r.Key, r.Kind, body)
		}
	}
	b.WriteString("Read the study's records with `lyk records`, then name the next member or declare the column done.\n")
	return b.String()
}

func ptrOr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
