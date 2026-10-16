package round

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"lykeion/server/internal/addon"
	"lykeion/server/internal/board"
	"lykeion/server/internal/ground"
	"lykeion/server/internal/jev"
	"lykeion/server/internal/records"
	"lykeion/server/internal/service"
	"lykeion/server/internal/skills"
	"lykeion/server/internal/storage"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

type Roles struct {
	Phases    [][]string
	Reviewer  string
	Kinds     func() map[string]service.Kind
	Check     func(context.Context, *records.View, *jev.Client) (*board.Gate, error)
	Finalize  func(*records.View, *board.Gate)
	RoleKinds func(role string) []string
}

var StageRoles = map[string]Roles{
	board.Grounding: {Phases: ground.Phases, Reviewer: ground.Reviewer, Kinds: ground.Kinds, Check: ground.Check,
		Finalize: ground.Finalize, RoleKinds: ground.RoleKinds},
}

type Engine struct {
	Store     *store.Store
	AgentRuns *service.AgentRuns
	Blobs     storage.Storage
	JevKey    string // the lab's TypeSafe key; it never leaves the server
	JevURL    string
	Now       func() time.Time
	Inline    bool // run kicks in the caller's goroutine; tests use it to stay deterministic
}

func (e *Engine) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

func (e *Engine) jev(s Settings) *jev.Client {
	if !s.Jev.Enabled || e.JevKey == "" {
		return nil
	}
	url := e.JevURL
	if url == "" {
		url = "https://api.typesafe.ai"
	}
	return &jev.Client{BaseURL: url, Key: e.JevKey, Model: jev.Model}
}

// OnSettled is wired to service.AgentRuns: a finished agent run may let its
// card move. Advance judges the run: a failure, a failed result or a completed
// run with no result escalates the card. Settlements are delivered after
// commit, at least once and inside the settling request; kicking is safe to
// repeat and leaves the request at once. A card-less run moves no card.
func (e *Engine) OnSettled(_ context.Context, t db.AgentRun) {
	if t.CardID == nil {
		return
	}
	e.Kick(t.WorkspaceID, *t.CardID)
}

// Kick advances a card soon, off the caller's request; Tick catches anything
// a crash dropped.
func (e *Engine) Kick(ws, card uuid.UUID) {
	run := func() {
		if err := e.Advance(context.Background(), ws, card); err != nil {
			slog.Error("advance", "card", card, "err", err)
		}
	}
	if e.Inline {
		run()
		return
	}
	go run()
}

// Tick marks queued agent runs whose owner is offline, runs WIP pull for
// workspaces with Backlog cards, and advances every card with nothing in flight.
func (e *Engine) Tick(ctx context.Context) error {
	if err := e.Store.Global().MarkQueueOffline(ctx); err != nil {
		slog.Error("mark queue offline", "err", err)
	}
	cands, err := e.Store.Global().BoardCandidates(ctx)
	if err != nil {
		return err
	}
	backlog := map[uuid.UUID]bool{}
	for _, c := range cands {
		if c.BoardColumn == board.Backlog {
			backlog[c.WorkspaceID] = true
			continue
		}
		// Each card advances on its own goroutine, so one slow gate never holds up
		// the rest; the gating lease keeps a card from being judged twice at once.
		e.Kick(c.WorkspaceID, c.CardID)
	}
	for ws := range backlog {
		if err := e.WIPPull(ctx, ws); err != nil {
			slog.Error("WIP pull", "workspace", ws, "err", err)
		}
	}
	return nil
}

func (e *Engine) settings(ctx context.Context, tx *store.Tx) (Settings, error) {
	w, err := tx.Q.GetWorkspace(ctx, tx.WS)
	return ParseSettings(w.Settings), err
}

// WIPPull starts Backlog cards from the top (lowest position) while the
// workspace is under its work-in-progress limit.
func (e *Engine) WIPPull(ctx context.Context, ws uuid.UUID) error {
	var started []uuid.UUID
	err := e.Store.InWorkspace(ctx, ws, uuid.Nil, func(tx *store.Tx) error {
		if _, err := tx.Q.GetWorkspace(ctx, ws); err != nil {
			return err
		}
		// Serialize WIP admission before touching cards, independently of events.
		if _, err := tx.Q.LockWorkspaceExecution(ctx, ws); err != nil {
			return err
		}
		s, err := e.settings(ctx, tx)
		if err != nil {
			return err
		}
		active, err := tx.Q.ActiveCardCount(ctx, ws)
		if err != nil {
			return err
		}
		backlog, err := tx.Q.BacklogCards(ctx, ws)
		if err != nil {
			return err
		}
		for _, c := range backlog {
			if active >= s.WIPLimit {
				break
			}
			st := board.FromCard(c)
			if items := board.CheckBudget(&st, e.now()); len(items) > 0 {
				if err := e.save(ctx, tx, c, st, items, "card.out_of_budget", nil); err != nil {
					return err
				}
				continue
			}
			board.Start(&st)
			if err := tx.Q.SaveCardBoard(ctx, st.Params(c.ID, ws)); err != nil {
				return err
			}
			if err := tx.Emit(ctx, "card.started", &c.ID, store.System, map[string]any{"column": st.Column, "round": st.Round}); err != nil {
				return err
			}
			started = append(started, c.ID)
			active++
		}
		return nil
	})
	for _, id := range started {
		e.Kick(ws, id)
	}
	return err
}

// rolesFrom returns the phases to run: all the stage roles, or, after a failed
// gate, the role to redo followed by every later phase.
func rolesFrom(t Roles, rerun string) [][]string {
	for i, p := range t.Phases {
		if i > 0 && slices.Contains(p, rerun) {
			return append([][]string{{rerun}}, t.Phases[i+1:]...)
		}
	}
	return t.Phases
}

func terminal(s string) bool { return s == "completed" || s == "failed" || s == "cancelled" }

// outcome says why a settled role run cannot count, or "" when it left a
// result: its role's own records or nothing_found. A failed or cancelled run,
// a failed result, and a completed run that recorded nothing all escalate.
func outcome(roles Roles, t db.AgentRun, recs []db.RoundAgentRunRecordsRow) string {
	if t.Status != "completed" {
		status := t.Status
		if t.FailureReason != nil {
			status += " (" + *t.FailureReason + ")"
		}
		return fmt.Sprintf("the %s run %s: %s", t.Role, status, t.Error)
	}
	own := append(roles.RoleKinds(t.Role), "nothing_found")
	result := false
	for _, r := range recs {
		if r.AgentRunID != t.ID {
			continue
		}
		if r.Kind == "failed" {
			var b struct{ Reason string }
			json.Unmarshal(r.Body, &b)
			return fmt.Sprintf("the %s role recorded that it failed: %s", t.Role, b.Reason)
		}
		result = result || slices.Contains(own, r.Kind)
	}
	if !result {
		return fmt.Sprintf("the %s run completed without recording a result (agent_error)", t.Role)
	}
	return ""
}

// gateLease is how long one Advance keeps other passes, on any server node,
// off a card while it judges the card outside a transaction. It outlasts a
// gate's Jev questions (60 s each) and a server step's 5-minute budget; a
// call that outlives it costs at most a duplicate call, because the write
// phase's re-check still lets only one result through.
const gateLease = 10 * time.Minute

// pending is what the read phase hands the call phase: the card as it was
// read and the records the gate reads, all in memory.
type pending struct {
	c        db.Card
	roles    Roles
	set      Settings
	v        *records.View
	digest   string // every live record of the column, as read
	reviewed bool   // the reviewer has run: finish its draft
}

// Advance moves one card one step, in up to three phases, so that no
// transaction is open while anything outside the database is called.
//
// The read phase locks the card and decides. Most steps finish there:
// escalating, flagging the budget, queueing a phase. When the gate is due it
// loads the records into memory, takes the card's gating lease and commits.
// The call phase runs the gate, which may ask Jev and read blobs, with no
// transaction open: settlements and record writes on the card never wait on
// it. The write phase locks the card again and applies the gate only if the
// card and its records are as the read phase left them; otherwise it drops
// the result and kicks the card, so the next Advance judges it afresh.
func (e *Engine) Advance(ctx context.Context, ws, id uuid.UUID) error {
	var p *pending
	err := e.Store.InWorkspace(ctx, ws, uuid.Nil, func(tx *store.Tx) (err error) {
		p, err = e.read(ctx, tx, id)
		return err
	})
	if err != nil || p == nil {
		return err
	}
	var kick bool
	g, err := e.call(ctx, p)
	if err == nil {
		err = e.Store.InWorkspace(ctx, ws, uuid.Nil, func(tx *store.Tx) (err error) {
			kick, err = e.write(ctx, tx, p, g)
			return err
		})
	}
	if err != nil {
		e.release(ctx, ws, id)
		return err
	}
	if kick {
		e.Kick(ws, id)
	}
	return nil
}

// read is the read phase. It returns what the call phase needs when the gate
// is due, or nil when it finished the step itself or there is nothing to do.
func (e *Engine) read(ctx context.Context, tx *store.Tx, id uuid.UUID) (*pending, error) {
	if _, err := tx.Q.LockWorkspaceExecution(ctx, tx.WS); err != nil {
		return nil, err
	}
	ok, err := tx.Q.TryLockCard(ctx, id)
	if err != nil || !ok {
		return nil, err
	}
	c, err := tx.Q.LockCard(ctx, db.LockCardParams{ID: id, WorkspaceID: tx.WS})
	if err != nil {
		return nil, err
	}
	if c.Ending != "" || c.Flag != "" {
		return nil, nil
	}
	if c.GatingUntil != nil && c.GatingUntil.After(e.now()) {
		return nil, nil // another pass is judging the card; its write phase moves it on
	}
	roles, ok := StageRoles[c.BoardColumn]
	if !ok {
		return nil, nil
	}
	s := board.FromCard(c)
	if items := board.CheckBudget(&s, e.now()); len(items) > 0 {
		reason := "budget"
		if err := tx.Q.SetCardQueueReason(ctx, db.SetCardQueueReasonParams{QueueReason: &reason, WorkspaceID: tx.WS, CardID: id}); err != nil {
			return nil, err
		}
		return nil, e.save(ctx, tx, c, s, items, "card.out_of_budget", nil)
	}
	set, err := e.settings(ctx, tx)
	if err != nil {
		return nil, err
	}
	runs, err := tx.Q.RoundAgentRuns(ctx, db.RoundAgentRunsParams{WorkspaceID: tx.WS, CardID: id, BoardColumn: c.BoardColumn, Round: c.Round})
	if err != nil {
		return nil, err
	}
	done := map[string]db.AgentRun{}
	inFlight := false
	for _, t := range runs {
		if !terminal(t.Status) {
			inFlight = true
			continue
		}
		done[t.Role] = t
	}
	recs, err := tx.Q.RoundAgentRunRecords(ctx, db.RoundAgentRunRecordsParams{WorkspaceID: tx.WS, CardID: id, BoardColumn: c.BoardColumn, Round: c.Round})
	if err != nil {
		return nil, err
	}
	for _, t := range runs {
		if !terminal(t.Status) {
			continue
		}
		if why := outcome(roles, t, recs); why != "" {
			items := board.EscalateAgentRun(&s, t.Role, why)
			return nil, e.save(ctx, tx, c, s, items, "card.escalated", nil)
		}
	}
	if inFlight {
		return nil, nil // a later settlement brings us back after checking failures
	}
	for _, phase := range rolesFrom(roles, c.Rerun) {
		var missing []string
		for _, role := range phase {
			if _, ok := done[role]; !ok {
				missing = append(missing, role)
			}
		}
		if len(missing) > 0 {
			return nil, e.queue(ctx, tx, c, s, roles, set, missing, "")
		}
	}
	_, reviewed := done[roles.Reviewer]
	return e.pend(ctx, tx, c, roles, set, reviewed)
}

// pend ends the read phase when the gate is due: it loads the records the
// gate reads and takes the card's gating lease.
func (e *Engine) pend(ctx context.Context, tx *store.Tx, c db.Card, roles Roles, set Settings, reviewed bool) (*pending, error) {
	v, err := records.Load(ctx, tx, e.Blobs, c.ID, c.BoardColumn)
	if err != nil {
		return nil, err
	}
	until := e.now().Add(gateLease)
	if err := tx.Q.SetGatingLease(ctx, db.SetGatingLeaseParams{GatingUntil: &until, ID: c.ID, WorkspaceID: tx.WS}); err != nil {
		return nil, err
	}
	return &pending{c: c, roles: roles, set: set, v: v, digest: v.Digest(), reviewed: reviewed}, nil
}

// call is the call phase: the gate, on the in-memory records, with no
// transaction open. Without a reviewer it is the stage roles' check; after one, the
// reviewer's draft finalised, or judged afresh if the records it reviewed
// have changed.
func (e *Engine) call(ctx context.Context, p *pending) (*board.Gate, error) {
	j := e.jev(p.set)
	if !p.reviewed {
		g, err := p.roles.Check(ctx, p.v, j)
		if err != nil {
			return nil, err
		}
		g.Round = p.c.Round
		return g, nil
	}
	g, err := e.draft(p.v, p.c.Round)
	if err != nil {
		return nil, err
	}
	if g == nil || g.InputDigest != p.v.Digest(ground.ReviewKinds...) {
		// the records changed under the reviewer: judge them afresh
		if g, err = p.roles.Check(ctx, p.v, j); err != nil {
			return nil, err
		}
		g.Round = p.c.Round
	}
	if g.Verdict == "" {
		p.roles.Finalize(p.v, g)
	}
	return g, nil
}

// write is the write phase. It clears the lease and applies the gate when
// nothing it was judged on has changed: a decided gate finishes, a draft
// queues the reviewer. A stale result is dropped and the card kicked.
func (e *Engine) write(ctx context.Context, tx *store.Tx, p *pending, g *board.Gate) (kick bool, err error) {
	if _, err := tx.Q.LockWorkspaceExecution(ctx, tx.WS); err != nil {
		return false, err
	}
	if err := tx.Q.WaitLockCard(ctx, p.c.ID); err != nil {
		return false, err
	}
	c, err := tx.Q.LockCard(ctx, db.LockCardParams{ID: p.c.ID, WorkspaceID: tx.WS})
	if err != nil {
		return false, err
	}
	if err := tx.Q.SetGatingLease(ctx, db.SetGatingLeaseParams{ID: c.ID, WorkspaceID: tx.WS}); err != nil {
		return false, err
	}
	if changed(p.c, c) {
		return true, nil
	}
	v, err := records.Load(ctx, tx, e.Blobs, c.ID, c.BoardColumn)
	if err != nil {
		return false, err
	}
	s := board.FromCard(c)
	if budget := s; v.Digest() != p.digest || len(board.CheckBudget(&budget, e.now())) > 0 {
		return true, nil
	}
	if g.Verdict != "" {
		return true, e.finish(ctx, tx, c, s, p.set, g)
	}
	if err := e.writeGate(ctx, tx, c, "gate_draft", g); err != nil {
		return false, err
	}
	s.State = board.InReview
	if err := tx.Q.SaveCardBoard(ctx, s.Params(c.ID, tx.WS)); err != nil {
		return false, err
	}
	return false, e.queue(ctx, tx, c, s, p.roles, p.set, []string{p.roles.Reviewer}, "The draft gate is the gate_draft record; read it with `lyk records`.")
}

// changed reports whether the card moved on since the read phase in a way
// that makes a gate judged then stale. spent_usd and version are left out: a
// settlement on the card changes them and nothing the gate judged.
func changed(was, now db.Card) bool {
	return was.Round != now.Round || was.BoardColumn != now.BoardColumn || was.State != now.State ||
		was.Flag != now.Flag || was.Ending != now.Ending
}

// release clears a card's gating lease after a failed call or write, so the
// next tick retries without waiting the lease out.
func (e *Engine) release(ctx context.Context, ws, id uuid.UUID) {
	err := e.Store.InWorkspace(ctx, ws, uuid.Nil, func(tx *store.Tx) error {
		if _, err := tx.Q.LockWorkspaceExecution(ctx, ws); err != nil {
			return err
		}
		return tx.Q.SetGatingLease(ctx, db.SetGatingLeaseParams{ID: id, WorkspaceID: ws})
	})
	if err != nil {
		slog.Error("release gating lease", "card", id, "err", err)
	}
}

// ContinuityScope names the CLI session a role run continues on its owner's
// daemon. A doer continues its session from round to round; a judge gets a
// fresh session each round, so its verdict never remembers the last one.
func ContinuityScope(card uuid.UUID, role string, round int32, judge bool) string {
	s := "card:" + card.String() + "/role:" + role
	if judge {
		s += fmt.Sprintf("/round:%d", round)
	}
	return s
}

// queue supersedes the roles' earlier records and enqueues one agent run per role.
// A role whose skill is missing gets no agent run: nothing of the phase is queued
// and the card escalates. The workspace's add-on reaches every role but the
// reviewer; one that cannot be assembled queues nothing either. The card's
// handoff note goes to each redone role: the rerun role, or every role when
// all the stage roles redo their work.
func (e *Engine) queue(ctx context.Context, tx *store.Tx, c db.Card, s board.State, sr Roles, set Settings, roles []string, extra string) error {
	skill := map[string]string{}
	for _, role := range roles {
		if skill[role] = skills.Role(c.BoardColumn, role); skill[role] == "" {
			items := board.EscalateAgentRun(&s, c.Rerun, fmt.Sprintf("no skill for the %s role in %s: nothing was queued", role, c.BoardColumn))
			return e.save(ctx, tx, c, s, items, "card.escalated", nil)
		}
	}
	var lab addon.Addon
	if slices.ContainsFunc(roles, func(r string) bool { return r != sr.Reviewer }) {
		var err error
		lab, err = addon.Load(ctx, tx.Q, tx.WS, c.BoardColumn)
		if errors.Is(err, addon.ErrInvalid) {
			// The fault is the add-on's, not a role's: keep the card's rerun so
			// resuming retries exactly these roles.
			items := board.EscalateAgentRun(&s, c.Rerun, fmt.Sprintf(
				"the %s add-on cannot be assembled (%v): nothing was queued; the workspace owner can fix it with `lyk addon set %s`",
				c.BoardColumn, err, c.BoardColumn))
			return e.save(ctx, tx, c, s, items, "card.escalated", nil)
		}
		if err != nil {
			return err
		}
	}
	comments, err := tx.Q.ListComments(ctx, db.ListCommentsParams{WorkspaceID: tx.WS, CardID: c.ID})
	if err != nil {
		return err
	}
	// Read at queue time, so a change to the research context reaches the next run.
	ws, err := tx.Q.GetWorkspace(ctx, tx.WS)
	if err != nil {
		return err
	}
	for _, role := range roles {
		if err := tx.Q.SupersedeRoleKinds(ctx, db.SupersedeRoleKindsParams{WorkspaceID: tx.WS, CardID: c.ID,
			BoardColumn: c.BoardColumn, Role: role, Kinds: slices.Concat(sr.RoleKinds(role), ResultKinds)}); err != nil {
			return err
		}
		note := ""
		if c.Rerun == "" || c.Rerun == role {
			note = c.HandoffNote
		}
		given := lab
		if role == sr.Reviewer {
			given = addon.Addon{}
		}
		x := extra
		payload, err := triggerPayload(ctx, tx, c, role)
		if err != nil {
			return err
		}
		if payload != "" {
			x = strings.TrimSpace(extra + "\n" + payload)
		}
		t, err := e.AgentRuns.Enqueue(ctx, tx, c.OwnerID, service.NewAgentRun{Card: c.ID, Column: c.BoardColumn, Round: c.Round,
			Role: role, Harness: set.HarnessFor(role), Prompt: prompt(c, role, comments, ws.ResearchContext, note, x), Skill: addon.Compose(skill[role], given.SkillMD)})
		if err != nil {
			return err
		}
		if err := tx.Q.SetAgentRunContinuity(ctx, db.SetAgentRunContinuityParams{ContinuityScope: ContinuityScope(c.ID, role, c.Round, role == sr.Reviewer),
			RunPrivate: payload != "", ID: t.ID, WorkspaceID: tx.WS}); err != nil {
			return err
		}
		if h := given.Hash(); h != "" {
			if err := tx.Q.SetAgentRunAddon(ctx, db.SetAgentRunAddonParams{AddonHash: h, AddonMcp: addon.Canonical(given.MCP), ID: t.ID, WorkspaceID: tx.WS}); err != nil {
				return err
			}
		}
		if note != "" {
			if err := tx.Q.SetAgentRunHandoffNote(ctx, db.SetAgentRunHandoffNoteParams{HandoffNote: note, ID: t.ID, WorkspaceID: tx.WS}); err != nil {
				return err
			}
		}
	}
	return tx.Emit(ctx, "phase.queued", &c.ID, store.System, map[string]any{"column": c.BoardColumn, "round": c.Round, "roles": roles})
}

func (e *Engine) writeGate(ctx context.Context, tx *store.Tx, c db.Card, kind string, g *board.Gate) error {
	b, err := json.Marshal(g)
	if err != nil {
		return err
	}
	_, err = tx.Q.InsertRecord(ctx, db.InsertRecordParams{WorkspaceID: tx.WS, CardID: c.ID, BoardColumn: c.BoardColumn,
		Round: c.Round, Kind: kind, Role: "server", Key: strconv.Itoa(int(c.Round)), Body: b})
	return err
}

func (e *Engine) draft(v *records.View, round int32) (*board.Gate, error) {
	r, ok := v.ByKey("gate_draft", strconv.Itoa(int(round)))
	if !ok {
		return nil, nil
	}
	g := records.Body[board.Gate](r)
	return &g, nil
}

// finish records the gate, with the add-on hash, mode and policy digest of every
// agent run it judged, and applies the lifecycle.
func (e *Engine) finish(ctx context.Context, tx *store.Tx, c db.Card, s board.State, set Settings, g *board.Gate) error {
	judged, err := tx.Q.JudgedAgentRuns(ctx, db.JudgedAgentRunsParams{WorkspaceID: tx.WS, CardID: c.ID, BoardColumn: c.BoardColumn, Round: c.Round})
	if err != nil {
		return err
	}
	g.Addons = nil
	for _, t := range judged {
		g.Addons = append(g.Addons, board.AgentRunAddon{AgentRun: t.ID.String(), Role: t.Role, Hash: t.AddonHash,
			ProfileMode: t.ProfileMode, PolicyDigest: t.PolicyDigest})
	}
	if err := e.writeGate(ctx, tx, c, "gate", g); err != nil {
		return err
	}
	items := board.Apply(&s, g, set.Limits)
	return e.save(ctx, tx, c, s, items, "gate.decided", map[string]any{"column": g.Column, "round": g.Round, "verdict": g.Verdict, "reason": g.Reason})
}

func (e *Engine) save(ctx context.Context, tx *store.Tx, c db.Card, s board.State, items []board.Item, event string, payload map[string]any) error {
	if err := tx.Q.SaveCardBoard(ctx, s.Params(c.ID, tx.WS)); err != nil {
		return err
	}
	for _, it := range items {
		if err := tx.Q.InsertInboxItem(ctx, db.InsertInboxItemParams{WorkspaceID: tx.WS, CardID: c.ID, UserID: c.OwnerID, Kind: it.Kind, Text: it.Text}); err != nil {
			return err
		}
	}
	if payload == nil {
		payload = map[string]any{}
	}
	payload["to_column"], payload["flag"], payload["ending"] = s.Column, s.Flag, s.Ending
	return tx.Emit(ctx, event, &c.ID, store.System, payload)
}
