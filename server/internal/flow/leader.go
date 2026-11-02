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
	ErrStepCap          = errors.New("this Team has used all its steps for this column: finish the column with what you have, or do the remaining work yourself")
	ErrInFlight         = errors.New("a member's run is still in flight: wait until its work lands")
	ErrMovedOn          = errors.New("the study is no longer working this run's column")
	ErrDeclaredDone     = errors.New("this column was already declared done")
	ErrBusy             = errors.New("a member is still working on this column: wait for its result first")
)

// idleNote wakes a leader that stopped without naming a member or finishing,
// and agentIdleNote a study's single agent that stopped without finishing.
const (
	idleNote      = "You ended without naming a member (`lyk team step`) or declaring the column done (`lyk team done`)."
	agentIdleNote = "You ended without declaring the column done (`lyk team done`)."
	// failedNote begins the note that wakes a leader or agent whose run
	// failed, rather than ended, before it named a member or finished.
	failedNote = "Your last run failed"
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

// IsMember says whether a run's role is a Team member's name rather than the
// leader, the auditor or a study's single agent.
func IsMember(role string) bool {
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
// leader names and records the delegation, and returns the member's run. w
// is the leader's run, whose card lock the caller holds. Outside Validation
// one member works a column at a time: while one has not ended, Delegate
// returns ErrBusy with that member's run.
func (e *Engine) Delegate(ctx context.Context, tx *store.Tx, w service.Writer, member, step string) (db.Record, uuid.UUID, error) {
	c, err := openColumn(ctx, tx, w)
	if err != nil {
		return db.Record{}, uuid.Nil, err
	}
	packID, m, err := columnTeam(ctx, tx, c.ID, c.BoardColumn)
	if errors.Is(err, pgx.ErrNoRows) {
		return db.Record{}, uuid.Nil, ErrUndeclaredMember
	}
	if err != nil {
		return db.Record{}, uuid.Nil, err
	}
	if !slices.ContainsFunc(m.Members, func(x pack.Member) bool { return x.Name == member }) {
		return db.Record{}, uuid.Nil, ErrUndeclaredMember
	}
	n, err := tx.Q.CountRoundKindRecords(ctx, db.CountRoundKindRecordsParams{WorkspaceID: tx.WS, CardID: c.ID,
		BoardColumn: c.BoardColumn, Round: c.Round, Kind: "delegation"})
	if err != nil {
		return db.Record{}, uuid.Nil, err
	}
	steps := m.Steps
	if steps == 0 {
		steps = pack.DefaultSteps
	}
	if n >= int64(steps) {
		return db.Record{}, uuid.Nil, ErrStepCap
	}
	if c.BoardColumn != board.Validation {
		working, err := tx.Q.InFlightMemberRun(ctx, db.InFlightMemberRunParams{WorkspaceID: tx.WS, CardID: c.ID, BoardColumn: c.BoardColumn, Round: c.Round})
		if err == nil {
			return db.Record{}, working, ErrBusy
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return db.Record{}, uuid.Nil, err
		}
	}
	a, err := teamAgent(ctx, tx, packID, c.OwnerID, member)
	if err != nil {
		return db.Record{}, uuid.Nil, err
	}
	run, err := e.enqueue(ctx, tx, c, member, m, a, leaderStep+step)
	if err != nil {
		return db.Record{}, uuid.Nil, err
	}
	rec, err := records.WriteAs(ctx, tx, w, "delegation", map[string]any{"member": member, "step": step, "agent_run_id": run.ID})
	return rec, run.ID, err
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
		if IsMember(r.Role) && inFlight(r.Status) {
			return db.Record{}, ErrInFlight
		}
	}
	if cites == nil {
		cites = []string{}
	}
	keys := make([]string, len(cites))
	for i, k := range cites {
		keys[i] = strings.TrimPrefix(strings.TrimPrefix(k, "call:"), "run:")
	}
	found, err := tx.Q.LiveRecordsByKeys(ctx, db.LiveRecordsByKeysParams{WorkspaceID: tx.WS, CardID: c.ID, Keys: keys})
	if err != nil {
		return db.Record{}, err
	}
	kinds := map[string]string{}
	for _, r := range found {
		kinds[r.Key] = r.Kind
	}
	for i, k := range cites {
		kind, ok := kinds[keys[i]]
		if !ok || (strings.HasPrefix(k, "call:") && kind != "call") || (strings.HasPrefix(k, "run:") && kind != "run") {
			return db.Record{}, service.Invalid(fmt.Sprintf("no record of this study has the key %q", k))
		}
	}
	cites = keys
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

// afterRun acts on a lead's settled run. A column finishes on its lead's
// hand-in: a done hand-in already wrote the summary that hands the column to
// its auditor, a could_not_do hand-in holds the study for its owner, and a
// run that failed, or ended without a hand-in, redoes the column once and
// then blocks it. A member's or the auditor's run wakes no one. Validation
// columns, which take no hand-in, go through afterProtocolRun.
func (e *Engine) afterRun(ctx context.Context, t db.AgentRun) error {
	if t.BoardColumn == board.Validation {
		return e.afterProtocolRun(ctx, t)
	}
	if accessInterrupted(t) || !leads(t.Role) || t.Status == "cancelled" {
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
		var h *handInBody
		for _, r := range recs {
			switch r.Kind {
			case "summary":
				return nil
			case "hand_in":
				b := records.Body[handInRecord](r).HandIn
				h = &b
			}
		}
		if t.Status == "completed" && h != nil && h.Status == "could_not_do" {
			return askOwner(ctx, tx, c, ptrOr(h.Reason))
		}
		return runFailed(ctx, tx, c, t)
	})
}

// handInRecord is the part of a hand_in record the engine reads.
type handInRecord struct {
	HandIn handInBody `json:"hand_in"`
}

type handInBody struct {
	Status string  `json:"status"`
	Reason *string `json:"reason"`
}

// askOwner holds a study whose lead could not do its column, as a block
// holds it but with the redo count unchanged, and asks its owner once; the
// owner's comment restarts the column through unblock.
func askOwner(ctx context.Context, tx *store.Tx, c db.Card, reason string) error {
	if err := tx.Q.SaveCardFlow(ctx, db.SaveCardFlowParams{ID: c.ID, WorkspaceID: tx.WS, BoardColumn: c.BoardColumn, State: "blocked",
		Round: c.Round, Redos: c.Redos}); err != nil {
		return err
	}
	if err := tx.Q.UpsertCouldNotDoDecision(ctx, db.UpsertCouldNotDoDecisionParams{WorkspaceID: tx.WS, CardID: c.ID, UserID: c.OwnerID,
		Text: couldNotDoText(c.Number, reason)}); err != nil {
		return err
	}
	if err := tx.Emit(ctx, "inbox.created", &c.ID, store.System, map[string]any{"card_id": c.ID}); err != nil {
		return err
	}
	return tx.Emit(ctx, "card.steered", &c.ID, store.System, map[string]any{"column": c.BoardColumn, "state": "blocked"})
}

// runFailed redoes a column whose lead's run failed, or ended without a
// hand-in, telling the redo what went wrong; a column already redone once is
// blocked instead, and its owner told why.
func runFailed(ctx context.Context, tx *store.Tx, c db.Card, t db.AgentRun) error {
	detail := strings.TrimSpace(t.Error)
	if detail == "" {
		detail = "the run ended without a hand-in"
	}
	next := db.SaveCardFlowParams{ID: c.ID, WorkspaceID: tx.WS, BoardColumn: c.BoardColumn, State: c.State, Round: c.Round, Redos: c.Redos}
	if c.Redos == 0 {
		next.Redos, next.Round = 1, c.Round+1
		next.HandoffNote = "The last attempt at this column failed. Fix this first:\n" + detail
	} else {
		next.State = "blocked"
	}
	if err := tx.Q.SaveCardFlow(ctx, next); err != nil {
		return err
	}
	if next.State == "blocked" {
		if err := tx.Q.InsertInboxItem(ctx, db.InsertInboxItemParams{WorkspaceID: tx.WS, CardID: c.ID, UserID: c.OwnerID, Kind: "blocked",
			Text: runFailedText(c.Number, c.BoardColumn, ptrOr(t.FailureReason), detail)}); err != nil {
			return err
		}
	}
	return tx.Emit(ctx, "card.steered", &c.ID, store.System, map[string]any{"column": next.BoardColumn, "state": next.State})
}

// afterProtocolRun wakes the leader when a member's run settles, with what
// the member wrote. A leader or single-agent run that ends or fails without
// naming a member or declaring the column done is woken once with a note
// saying which; doing so again leaves the study working and escalates it to
// its owner.
func (e *Engine) afterProtocolRun(ctx context.Context, t db.AgentRun) error {
	if accessInterrupted(t) || (leads(t.Role) && t.Status == "cancelled") || (!leads(t.Role) && !IsMember(t.Role)) {
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
		if IsMember(t.Role) {
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
			if (IsMember(r.Role) && inFlight(r.Status)) || (r.Role == t.Role && waitingToStart(r.Status)) {
				return nil
			}
		}
		note, who := idleNote, "The leader stopped twice without naming a member or declaring"
		if t.Role == "agent" {
			note, who = agentIdleNote, "The study's agent stopped twice without declaring"
		}
		if t.Status == "failed" {
			reason := "an error"
			if t.FailureReason != nil && *t.FailureReason != "" {
				reason = *t.FailureReason
			}
			note = fmt.Sprintf("%s (%s) before it finished. Carry on from what the study's records hold.", failedNote, reason)
			who = fmt.Sprintf("The leader's runs failed (%s) twice without naming a member or declaring", reason)
			if t.Role == "agent" {
				who = fmt.Sprintf("The study's agent's runs failed (%s) twice without declaring", reason)
			}
		}
		if !strings.Contains(t.Prompt, idleNote) && !strings.Contains(t.Prompt, agentIdleNote) && !strings.Contains(t.Prompt, failedNote) {
			return e.wake(ctx, tx, c, note)
		}
		return tx.Q.InsertInboxItem(ctx, db.InsertInboxItemParams{WorkspaceID: tx.WS, CardID: c.ID, UserID: c.OwnerID, Kind: "escalated",
			Text: fmt.Sprintf("%s the %s column done. Comment on the study to wake it.", who, title(c.BoardColumn))})
	})
}

// OnComment restarts the column of a study blocked or held for its owner when
// the owner comments. On a Validation column that has started it also wakes
// the leader, or the study's single agent, as a squad leader is woken by a
// comment, and the comment answers any escalation. Like Kick it runs off the
// caller's request, after the comment commits.
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
		if restart, err = unblock(ctx, tx, c, author, body); err != nil || restart || !working(c) || c.BoardColumn != board.Validation {
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
	_, err = e.enqueue(ctx, tx, c, role, m, a, note)
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

func accessInterrupted(r db.AgentRun) bool {
	return r.Status == "cancelled" && ptrOr(r.CancelReason) == "runtime_access_revoked" ||
		r.Status == "failed" && ptrOr(r.FailureReason) == "runtime_access_denied"
}

// Resume restores the latest interrupted work for each role after its owner
// chooses a machine. Later work for that role already supersedes the interruption.
func (e *Engine) Resume(ctx context.Context, tx *store.Tx, c db.Card) error {
	if !working(c) {
		return nil
	}
	runs, err := tx.Q.RoundAgentRuns(ctx, db.RoundAgentRunsParams{WorkspaceID: tx.WS, CardID: c.ID, BoardColumn: c.BoardColumn, Round: c.Round})
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for i := len(runs) - 1; i >= 0; i-- {
		r := runs[i]
		if seen[r.Role] {
			continue
		}
		seen[r.Role] = true
		if !accessInterrupted(r) {
			continue
		}
		var m *pack.Manifest
		var a db.Agent
		if leads(r.Role) {
			_, m, a, err = e.worker(ctx, tx, c)
		} else {
			var packID uuid.UUID
			packID, m, err = columnTeam(ctx, tx, c.ID, c.BoardColumn)
			if err == nil {
				member := r.Role
				if r.Role == "auditor" {
					member = m.Auditor.Name
				}
				a, err = teamAgent(ctx, tx, packID, c.OwnerID, member)
			}
		}
		if err != nil {
			return err
		}
		p := r.Prompt
		if p == "" {
			// Legacy runs may lack a prompt snapshot. Rebuild card context and
			// include any saved assignment without parsing human-facing text.
			p, err = e.runPrompt(ctx, tx, c, r.Role, r.Assignment)
			if err != nil {
				return err
			}
		}
		runAssignment := r.Assignment
		if runAssignment == "" {
			runAssignment = assignment(c, "")
		}
		p += "\nResumed on another machine.\n"
		if _, err := e.enqueueWithInput(ctx, tx, c, r.Role, m, a, p, runAssignment); err != nil {
			return err
		}
	}
	return nil
}
