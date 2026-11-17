package flow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/handin"
	"lykeion/server/internal/jev"
	"lykeion/server/internal/records"
	"lykeion/server/internal/report"
	"lykeion/server/internal/review"
	"lykeion/server/internal/service"
	"lykeion/server/internal/sources"
	"lykeion/server/internal/storage"
	"lykeion/server/internal/store"
	"lykeion/server/internal/testutil"
	"lykeion/server/pkg/agent"
	db "lykeion/server/pkg/db/generated"
)

// reportRun queues a report run for c pinned to runtime pin.
func (l *lab) reportRun(t *testing.T, c db.Card, pin uuid.UUID) db.AgentRun {
	t.Helper()
	var r db.AgentRun
	l.in(t, func(tx *store.Tx) (err error) {
		r, err = tx.Q.InsertReportAgentRun(context.Background(), db.InsertReportAgentRunParams{
			WorkspaceID: l.ws, CardID: c.ID, BoardColumn: "validation", Round: 1, Harness: "claude",
			PinnedRuntimeID: &pin, Prompt: "p", Skill: "s"})
		return err
	})
	return r
}

// claims reports whether runtime rt can claim a queued run.
func (l *lab) claims(t *testing.T, rt uuid.UUID) bool {
	t.Helper()
	var ok bool
	l.in(t, func(tx *store.Tx) error {
		_, err := tx.Q.ClaimAgentRun(context.Background(), db.ClaimAgentRunParams{WorkspaceID: l.ws, RuntimeID: &rt})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			t.Fatal(err)
		}
		ok = err == nil
		return nil
	})
	return ok
}

func (l *lab) register(t *testing.T, daemon string, harnesses ...string) map[string]uuid.UUID {
	t.Helper()
	rts, err := l.e.AgentRuns.Register(context.Background(), l.owner, l.ws, daemon, daemon, harnesses, nil, agent.Platform{})
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]uuid.UUID{}
	for _, r := range rts {
		out[r.Harness] = r.ID
	}
	return out
}

func TestReportRunClaimedOnlyByPinnedRuntime(t *testing.T) {
	l := newLab(t)
	c := l.study(t, "validation", true, nil)
	a := l.register(t, "d1", "claude", "codex")
	b := l.register(t, "d2", "claude")
	l.reportRun(t, c, a["claude"])
	if l.claims(t, b["claude"]) {
		t.Fatal("an unpinned runtime claimed the report run")
	}
	if l.claims(t, a["codex"]) {
		t.Fatal("a runtime of the pinned machine with another harness claimed the report run")
	}
	if !l.claims(t, a["claude"]) {
		t.Fatal("the pinned runtime did not claim the report run")
	}
}

func TestReportRunClaimedAfterStudyDone(t *testing.T) {
	l := newLab(t)
	c := l.study(t, "validation", true, nil)
	a := l.register(t, "d1", "claude")
	if _, err := l.env.Owner.Exec(context.Background(), `UPDATE card SET state = 'done' WHERE id = $1`, c.ID); err != nil {
		t.Fatal(err)
	}
	l.reportRun(t, c, a["claude"])
	if !l.claims(t, a["claude"]) {
		t.Fatal("the pinned runtime did not claim a report run for a done study")
	}
}

func TestReportRunCountsAsQueuedForOfflineMachine(t *testing.T) {
	ctx := context.Background()
	l := newLab(t)
	c := l.study(t, "validation", true, nil)
	a := l.register(t, "d1", "claude")
	b := l.register(t, "d2", "claude")
	l.reportRun(t, c, a["claude"])
	// The other machine is online, but only the pinned one can take the run.
	if _, err := l.env.Owner.Exec(ctx, `UPDATE runtime SET status = 'offline' WHERE id = $1`, a["claude"]); err != nil {
		t.Fatal(err)
	}
	var rows []db.OfflineRuntimesWithQueuedRunsRow
	l.in(t, func(tx *store.Tx) (err error) {
		rows, err = tx.Q.OfflineRuntimesWithQueuedRuns(ctx, l.ws)
		return err
	})
	if len(rows) != 1 || rows[0].ID != a["claude"] || rows[0].Queued != 1 {
		t.Fatalf("offline runtimes with queued runs = %+v, want the pinned one with 1 (other %v)", rows, b["claude"])
	}
	if _, err := l.env.Owner.Exec(ctx, `SELECT mark_queue_offline()`); err != nil {
		t.Fatal(err)
	}
	var reason *string
	if err := l.env.Owner.QueryRow(ctx, `SELECT queue_reason FROM agent_run WHERE kind = 'report'`).Scan(&reason); err != nil {
		t.Fatal(err)
	}
	if reason == nil || *reason != "runtime_offline" {
		t.Fatalf("queue_reason = %v, want runtime_offline", reason)
	}
	// Back online, the reason clears.
	if _, err := l.env.Owner.Exec(ctx, `UPDATE runtime SET status = 'online' WHERE id = $1`, a["claude"]); err != nil {
		t.Fatal(err)
	}
	if _, err := l.env.Owner.Exec(ctx, `SELECT mark_queue_offline()`); err != nil {
		t.Fatal(err)
	}
	if err := l.env.Owner.QueryRow(ctx, `SELECT queue_reason FROM agent_run WHERE kind = 'report'`).Scan(&reason); err != nil {
		t.Fatal(err)
	}
	if reason != nil {
		t.Fatalf("queue_reason = %q after the machine came back, want none", *reason)
	}
}

func TestReportTablesAreWorkspaceIsolated(t *testing.T) {
	ctx := context.Background()
	l := newLab(t)
	c := l.study(t, "validation", true, nil)
	l.in(t, func(tx *store.Tx) error {
		if _, err := tx.Q.InsertReportBuild(ctx, db.InsertReportBuildParams{WorkspaceID: l.ws, CardID: c.ID, Status: "queued", ChecksStatus: "pending"}); err != nil {
			return err
		}
		if err := tx.Q.InsertReportConcept(ctx, db.InsertReportConceptParams{WorkspaceID: l.ws, CardID: c.ID, Key: "k", Label: "K", Type: "other"}); err != nil {
			return err
		}
		if err := tx.Q.InsertReportFact(ctx, db.InsertReportFactParams{WorkspaceID: l.ws, CardID: c.ID, Subject: "k", Relation: "is_a", Object: "j", Quote: "q", SourceNode: "n", P: 0.9}); err != nil {
			return err
		}
		if err := tx.Q.InsertReportLink(ctx, db.InsertReportLinkParams{WorkspaceID: l.ws, CardID: c.ID, StartByte: 0, EndByte: 3, NodeID: "n", P: 0.9}); err != nil {
			return err
		}
		return tx.Q.InsertReportCheck(ctx, db.InsertReportCheckParams{WorkspaceID: l.ws, CardID: c.ID, NodeID: "n", CheckID: "figure:o", Question: "q", Choice: "yes", P: 0.9, Status: "pass", Evidence: "e", Basis: "text"})
	})
	count := func(ws uuid.UUID, tbl string) int {
		tx, err := l.env.App.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		if _, err := tx.Exec(ctx, "SELECT set_config('app.workspace_id', $1, true)", ws.String()); err != nil {
			t.Fatal(err)
		}
		var n int
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM "+tbl).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	other := uuid.New()
	for _, tbl := range []string{"report_build", "report_concept", "report_fact", "report_link", "report_check"} {
		if n := count(l.ws, tbl); n != 1 {
			t.Errorf("%s: workspace A sees %d rows, want 1", tbl, n)
		}
		if n := count(other, tbl); n != 0 {
			t.Errorf("%s: workspace B sees %d rows of workspace A", tbl, n)
		}
	}
}

func TestMigration0069RoundTrips(t *testing.T) {
	ctx := context.Background()
	env := testutil.Setup(t)
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join("..", "..", "migrations", name))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	exists := func() (tables int, column bool) {
		if err := env.Owner.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_name IN ('report_build','report_concept','report_fact','report_link','report_check')`).Scan(&tables); err != nil {
			t.Fatal(err)
		}
		if err := env.Owner.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_name='agent_run' AND column_name='pinned_runtime_id')`).Scan(&column); err != nil {
			t.Fatal(err)
		}
		return
	}
	if n, c := exists(); n != 5 || !c {
		t.Fatalf("after up: %d tables, column %v", n, c)
	}
	if _, err := env.Owner.Exec(ctx, read("0069_study_report_graph.down.sql")); err != nil {
		t.Fatalf("down: %v", err)
	}
	if n, c := exists(); n != 0 || c {
		t.Fatalf("after down: %d tables, column %v", n, c)
	}
	if _, err := env.Owner.Exec(ctx, read("0069_study_report_graph.up.sql")); err != nil {
		t.Fatalf("up again: %v", err)
	}
	if n, c := exists(); n != 5 || !c {
		t.Fatalf("after second up: %d tables, column %v", n, c)
	}
}

func TestReportRunNeedsTheShareItWasPinnedUnder(t *testing.T) {
	ctx := context.Background()
	l := newLab(t)
	c := l.study(t, "validation", true, nil)
	guest, err := l.st.Global().UpsertUserByEmail(ctx, "guest@lab.org")
	if err != nil {
		t.Fatal(err)
	}
	l.in(t, func(tx *store.Tx) error {
		return tx.Q.AddMember(ctx, db.AddMemberParams{WorkspaceID: l.ws, UserID: guest.ID, Role: "member"})
	})
	rts, err := l.e.AgentRuns.Register(ctx, guest.ID, l.ws, "gd", "gd", []string{"claude"}, nil, agent.Platform{OS: "linux"})
	if err != nil {
		t.Fatal(err)
	}
	rt := rts[0].ID
	l.reportRun(t, c, rt)
	claim := func() bool {
		ok := false
		l.in(t, func(tx *store.Tx) error {
			_, err := tx.Q.ClaimAgentRun(ctx, db.ClaimAgentRunParams{WorkspaceID: l.ws, RuntimeID: &rt, GuestIsolation: true})
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				t.Fatal(err)
			}
			ok = err == nil
			return nil
		})
		return ok
	}
	if claim() {
		t.Fatal("claimed on a colleague's runtime that was never shared")
	}
	if _, err := l.env.Owner.Exec(ctx, `INSERT INTO runtime_share(workspace_id, runtime_id, user_id) VALUES($1,$2,$3)`, l.ws, rt, l.owner); err != nil {
		t.Fatal(err)
	}
	if _, err := l.env.Owner.Exec(ctx, `DELETE FROM runtime_share`); err != nil {
		t.Fatal(err)
	}
	if claim() {
		t.Fatal("claimed after the share was removed")
	}
	if _, err := l.env.Owner.Exec(ctx, `INSERT INTO runtime_share(workspace_id, runtime_id, user_id) VALUES($1,$2,$3)`, l.ws, rt, l.owner); err != nil {
		t.Fatal(err)
	}
	if !claim() {
		t.Fatal("not claimed while the share stands")
	}
}

func TestReportRunSurvivesItsRuntimeBeingDeleted(t *testing.T) {
	ctx := context.Background()
	l := newLab(t)
	c := l.study(t, "validation", true, nil)
	a := l.register(t, "d1", "claude")
	r := l.reportRun(t, c, a["claude"])
	l.in(t, func(tx *store.Tx) error {
		n, err := tx.Q.DeleteRuntime(ctx, db.DeleteRuntimeParams{ID: a["claude"], WorkspaceID: l.ws, OwnerID: l.owner})
		if err == nil && n != 1 {
			t.Errorf("deleted %d runtimes, want 1", n)
		}
		return err
	})
	var pin *uuid.UUID
	if err := l.env.Owner.QueryRow(ctx, `SELECT pinned_runtime_id FROM agent_run WHERE id = $1`, r.ID).Scan(&pin); err != nil {
		t.Fatal(err)
	}
	if pin != nil {
		t.Fatalf("pin = %v, want NULL", pin)
	}
}

func TestReportBuildsToResume(t *testing.T) {
	ctx := context.Background()
	l := newLab(t)
	a := l.register(t, "d1", "claude")
	mk := func(status, checks string, withRun bool, age, runAge string) uuid.UUID {
		c := l.study(t, "validation", true, nil)
		var run *uuid.UUID
		if withRun {
			r := l.reportRun(t, c, a["claude"])
			run = &r.ID
			if _, err := l.env.Owner.Exec(ctx, `UPDATE agent_run SET status='completed', completed_at = now() - $2::interval WHERE id=$1`, r.ID, runAge); err != nil {
				t.Fatal(err)
			}
		}
		l.in(t, func(tx *store.Tx) error {
			_, err := tx.Q.InsertReportBuild(ctx, db.InsertReportBuildParams{WorkspaceID: l.ws, CardID: c.ID, AgentRunID: run, Status: status, ChecksStatus: checks})
			return err
		})
		if _, err := l.env.Owner.Exec(ctx, `UPDATE report_build SET created_at = now() - $2::interval WHERE card_id=$1`, c.ID, age); err != nil {
			t.Fatal(err)
		}
		return c.ID
	}
	settledOld := mk("judging", "done", true, "10 minutes", "5 minutes")
	settledJustNow := mk("judging", "done", true, "10 minutes", "10 seconds") // a long run that just settled: still being judged
	noRun := mk("judging", "done", false, "10 minutes", "")
	pendingOld := mk("done", "pending", false, "10 minutes", "")
	pendingNew := mk("done", "pending", false, "10 seconds", "")
	finished := mk("done", "done", false, "10 minutes", "")
	got := map[uuid.UUID]bool{}
	rows, err := l.st.Global().ReportBuildsToResume(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		got[r.CardID] = r.Judging
	}
	for name, want := range map[string]struct {
		id  uuid.UUID
		in  bool
		jdg bool
	}{
		"settled long ago": {settledOld, true, true}, "settled just now": {settledJustNow, false, false},
		"run row gone": {noRun, true, true}, "checks pending": {pendingOld, true, false},
		"checks pending, new": {pendingNew, false, false}, "finished": {finished, false, false},
	} {
		j, in := got[want.id]
		if in != want.in || (in && j != want.jdg) {
			t.Errorf("%s: resumable=%v judging=%v, want %v %v", name, in, j, want.in, want.jdg)
		}
	}
}

// finishedStudy runs a Team Validation study to done. Its leader run ended
// on daemon d1's runtime for its harness before the objector settled.
func (l *lab) finishedStudy(t *testing.T) (db.Card, db.AgentRun) {
	t.Helper()
	ctx := context.Background()
	l.jev(t, 200, levels(nil))
	l.online(t, "d1", "claude", "codex")
	yes := true
	c := modeAStudy(t, l, &yes)
	l.assess(t, c.ID)
	lead := l.latestRun(t, c.ID, "leader")
	var rt uuid.UUID
	if err := l.env.Owner.QueryRow(ctx, "SELECT id FROM runtime WHERE harness = $1", lead.Harness).Scan(&rt); err != nil {
		t.Fatal(err)
	}
	if _, err := l.env.Owner.Exec(ctx, "UPDATE agent_run SET status = 'completed', runtime_id = $2 WHERE id = $1", lead.ID, rt); err != nil {
		t.Fatal(err)
	}
	lead.RuntimeID = &rt
	l.object(t, c.ID)
	if got := l.card(t, c.ID); got.State != "done" {
		t.Fatalf("state %s", got.State)
	}
	return c, lead
}

func (l *lab) reportBuild(t *testing.T, card uuid.UUID) (db.ReportBuild, bool) {
	t.Helper()
	var b db.ReportBuild
	var found bool
	l.in(t, func(tx *store.Tx) error {
		var err error
		b, err = tx.Q.GetReportBuild(context.Background(), db.GetReportBuildParams{WorkspaceID: l.ws, CardID: card})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		found = err == nil
		return err
	})
	return b, found
}

func TestFinishQueuesReportRunOnFinalRunsRuntime(t *testing.T) {
	l := newLab(t)
	c, lead := l.finishedStudy(t)
	if n := l.count(t, "SELECT count(*) FROM agent_run WHERE card_id = $1 AND kind = 'report'", c.ID); n != 1 {
		t.Fatalf("%d report runs, want 1", n)
	}
	r := l.latestRun(t, c.ID, "reporter")
	if r.Kind != "report" || r.Status != "queued" || r.PinnedRuntimeID == nil || *r.PinnedRuntimeID != *lead.RuntimeID || r.Harness != lead.Harness ||
		r.BoardColumn != lead.BoardColumn || r.Round != lead.Round || (r.AgentID == nil) != (lead.AgentID == nil) || (r.AgentID != nil && *r.AgentID != *lead.AgentID) {
		t.Fatalf("report run %+v\nfinal run %+v", r, lead)
	}
	if !strings.Contains(r.Prompt, supportedText) || r.Skill == "" {
		t.Fatalf("report run prompt %q skill %q", r.Prompt, r.Skill)
	}
	b, ok := l.reportBuild(t, c.ID)
	// The finish kicked the server's checks once it committed.
	if !ok || b.Status != "queued" || b.ChecksStatus != "done" || b.AgentRunID == nil || *b.AgentRunID != r.ID {
		t.Fatalf("report build %+v (found %v)", b, ok)
	}
}

func TestFinishQueuesNoReportRunForWorkflowOrStopped(t *testing.T) {
	ctx := context.Background()
	t.Run("workflow", func(t *testing.T) {
		l := newLab(t)
		l.legacy = true
		l.jev(t, 200, levels(nil))
		l.online(t, "d1", "claude")
		a := l.agent(t, "Curie", "claude", "", "You are Curie.")
		c := l.scoped(t, "grounding", "grounding", a)
		if _, err := l.env.Owner.Exec(ctx, "UPDATE card SET workflow = 'w', workflow_release = 'v0.1.0' WHERE id = $1", c.ID); err != nil {
			t.Fatal(err)
		}
		if err := l.declare(t, c.ID); err != nil {
			t.Fatal(err)
		}
		// The agent's run ended on a runtime, so only the Workflow rule keeps the report away.
		if _, err := l.env.Owner.Exec(ctx, "UPDATE agent_run SET status = 'completed', runtime_id = (SELECT id FROM runtime LIMIT 1) WHERE card_id = $1 AND role = 'agent'", c.ID); err != nil {
			t.Fatal(err)
		}
		if err := l.e.Audit(ctx, l.ws, c.ID); err != nil {
			t.Fatal(err)
		}
		if got := l.card(t, c.ID); got.State != "done" {
			t.Fatalf("state %s", got.State)
		}
		if _, ok := l.reportBuild(t, c.ID); ok {
			t.Fatal("a Workflow study has a report build")
		}
		if n := l.count(t, "SELECT count(*) FROM agent_run WHERE card_id = $1 AND kind = 'report'", c.ID); n != 0 {
			t.Fatalf("%d report runs", n)
		}
	})
	t.Run("stopped", func(t *testing.T) {
		l := newLab(t)
		c, item := l.heldStudy(t)
		if err := l.e.Decide(ctx, l.ws, item, l.owner, "close", "no data"); err != nil {
			t.Fatal(err)
		}
		if got := l.card(t, c.ID); got.Ending != "stopped" {
			t.Fatalf("ending %q", got.Ending)
		}
		if _, ok := l.reportBuild(t, c.ID); ok {
			t.Fatal("a stopped study has a report build")
		}
		if n := l.count(t, "SELECT count(*) FROM agent_run WHERE card_id = $1 AND kind = 'report'", c.ID); n != 0 {
			t.Fatalf("%d report runs", n)
		}
	})
	t.Run("no final run", func(t *testing.T) {
		l := newLab(t)
		l.legacy = true
		l.jev(t, 200, levels(nil))
		a := l.agent(t, "Curie", "claude", "", "You are Curie.")
		c := l.scoped(t, "grounding", "grounding", a)
		l.done(t, c.ID)
		if got := l.card(t, c.ID); got.State != "done" {
			t.Fatalf("state %s", got.State)
		}
		b, ok := l.reportBuild(t, c.ID)
		if !ok || b.Status != "skipped" || b.ChecksStatus != "done" || b.AgentRunID != nil {
			t.Fatalf("report build %+v (found %v)", b, ok)
		}
		if n := l.count(t, "SELECT count(*) FROM agent_run WHERE card_id = $1 AND kind = 'report'", c.ID); n != 0 {
			t.Fatalf("%d report runs", n)
		}
	})
}

func TestReportRunInStudyTimeline(t *testing.T) {
	ctx := context.Background()
	l := newLab(t)
	c := l.study(t, "validation", true, nil)
	a := l.register(t, "d1", "claude")
	r := l.reportRun(t, c, a["claude"])
	l.in(t, func(tx *store.Tx) error {
		runs, err := tx.Q.StudyRuns(ctx, db.StudyRunsParams{WorkspaceID: l.ws, CardID: c.ID})
		if err != nil {
			return err
		}
		if !slices.ContainsFunc(runs, func(x db.StudyRunsRow) bool { return x.ID == r.ID }) {
			t.Errorf("StudyRuns leaves out the report run: %+v", runs)
		}
		page, err := tx.Q.CardAgentRuns(ctx, db.CardAgentRunsParams{WorkspaceID: l.ws, CardID: &c.ID})
		if err != nil {
			return err
		}
		if !slices.ContainsFunc(page, func(x db.CardAgentRunsRow) bool { return x.ID == r.ID }) {
			t.Errorf("the study page's runs leave out the report run")
		}
		return nil
	})
}

func TestReportRunDoesNotAdvanceStudy(t *testing.T) {
	ctx := context.Background()
	l := newLab(t)
	c, _ := l.finishedStudy(t)
	ref, claimed := l.claim(t, c.ID, "report")
	run := claimed.AgentRun
	// The report run's token opens no study route, and it gets no tools.
	rows, err := l.env.Owner.Query(ctx, "SELECT scopes FROM agent_run_token WHERE agent_run_id = $1", run.ID)
	if err != nil {
		t.Fatal(err)
	}
	var tokens [][]string
	for rows.Next() {
		var s []string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		tokens = append(tokens, s)
	}
	rows.Close()
	if len(tokens) != 1 || !slices.Equal(tokens[0], []string{service.ScopeHandIn}) || claimed.CallToken != "" {
		t.Fatalf("report run tokens %v, call token %q", tokens, claimed.CallToken)
	}
	if len(claimed.Connectors) != 0 || len(claimed.WorkspaceConnectors) != 0 || len(claimed.MCP) != 0 || len(claimed.Attachments) != 0 || len(claimed.Skills) != 0 {
		t.Fatalf("report run got tools: %+v", claimed)
	}

	recordsBefore := l.count(t, "SELECT count(*) FROM record WHERE card_id = $1", c.ID)
	itemsBefore := l.count(t, "SELECT count(*) FROM inbox_item WHERE card_id = $1", c.ID)
	runsBefore := l.count(t, "SELECT count(*) FROM agent_run WHERE card_id = $1", c.ID)
	hs := handin.HandIns{Runs: l.e.AgentRuns, SentBack: l.e.SentBack}
	if probs, err := hs.Submit(ctx, l.owner, ref, run.ID, json.RawMessage(`{"status":"done","reason":null,"concepts":[],"facts":[],"links":[{"phrase":"","occurrence":0,"node":""}]}`)); err != nil || len(probs) == 0 {
		t.Fatalf("a misshapen report hand-in: %v %v", probs, err)
	}
	raw := json.RawMessage(`{"status":"done","reason":null,"concepts":[{"key":"auc","label":"AUC","type":"measure"}],"facts":[],"links":[]}`)
	if probs, err := hs.Submit(ctx, l.owner, ref, run.ID, raw); err != nil || len(probs) != 0 {
		t.Fatalf("the report hand-in: %v %v", probs, err)
	}
	if probs, err := hs.Submit(ctx, l.owner, ref, run.ID, raw); err != nil || len(probs) != 0 {
		t.Fatalf("the report hand-in again: %v %v", probs, err)
	}
	if n := l.count(t, "SELECT count(*) FROM record WHERE card_id = $1", c.ID); n != recordsBefore+1 {
		t.Fatalf("%d records after the hand-in, want %d", n, recordsBefore+1)
	}
	var stored json.RawMessage
	if err := l.env.Owner.QueryRow(ctx, "SELECT body->'hand_in' FROM record WHERE agent_run_id = $1 AND kind = 'hand_in'", run.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if h, probs := handin.ParseReport(stored); len(probs) != 0 || len(h.Concepts) != 1 {
		t.Fatalf("stored hand-in %s: %v", stored, probs)
	}

	l.settle(t, run, "completed", "", "")
	if got := l.card(t, c.ID); got.State != "done" {
		t.Fatalf("state %s after the report run", got.State)
	}
	if n := l.count(t, "SELECT count(*) FROM inbox_item WHERE card_id = $1", c.ID); n != itemsBefore {
		t.Fatalf("%d Inbox items after the report run, want %d", n, itemsBefore)
	}
	if n := l.count(t, "SELECT count(*) FROM agent_run WHERE card_id = $1", c.ID); n != runsBefore {
		t.Fatalf("%d runs after the report run, want %d", n, runsBefore)
	}
}

// A report run that can't be queued leaves the study finished, its reports
// raised, and its build skipped.
func TestFinishSurvivesAReportRunThatCannotBeQueued(t *testing.T) {
	ctx := context.Background()
	l := newLab(t)
	if _, err := l.env.Owner.Exec(ctx, `CREATE FUNCTION refuse_report() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'no report runs'; END $$;
		CREATE TRIGGER refuse_report BEFORE INSERT ON agent_run FOR EACH ROW WHEN (NEW.kind = 'report') EXECUTE FUNCTION refuse_report();`); err != nil {
		t.Fatal(err)
	}
	c, _ := l.finishedStudy(t)
	if items := l.inbox(t, c.ID, "study_report"); len(items) != 1 {
		t.Fatalf("study_report items: %v", items)
	}
	b, ok := l.reportBuild(t, c.ID)
	if !ok || b.Status != "skipped" || b.AgentRunID != nil {
		t.Fatalf("report build %+v (found %v)", b, ok)
	}
	if n := l.count(t, "SELECT count(*) FROM agent_run WHERE card_id = $1 AND kind = 'report'", c.ID); n != 0 {
		t.Fatalf("%d report runs", n)
	}
}

// studyReport builds c's report as it stands.
func (l *lab) studyReport(t *testing.T, c db.Card) report.Report {
	t.Helper()
	var in report.Input
	l.in(t, func(tx *store.Tx) (err error) {
		in, err = report.Load(context.Background(), tx, l.card(t, c.ID))
		return err
	})
	return report.Build(in)
}

// settleReport records hand as the report run's hand-in, completes the run
// and delivers its settlement.
func (l *lab) settleReport(t *testing.T, run db.AgentRun, status string, hand any) {
	t.Helper()
	ctx := context.Background()
	raw, err := json.Marshal(hand)
	if err != nil {
		t.Fatal(err)
	}
	l.in(t, func(tx *store.Tx) error {
		_, err := records.WriteAs(ctx, tx, service.Writer{Card: *run.CardID, AgentRun: run.ID, Column: run.BoardColumn, Role: run.Role, Round: run.Round},
			"hand_in", map[string]any{"agent_run_id": run.ID, "attempt": run.Attempt, "hand_in": json.RawMessage(raw)})
		return err
	})
	if err := l.env.Owner.QueryRow(ctx, "UPDATE agent_run SET status = $2, completed_at = now() WHERE id = $1 RETURNING status",
		run.ID, status).Scan(&run.Status); err != nil {
		t.Fatal(err)
	}
	if err := l.e.OnSettlement(ctx, service.Settlement{ID: run.ID, AgentRun: run}); err != nil {
		t.Fatal(err)
	}
}

// reportRows counts the report's concepts, facts and links.
func (l *lab) reportRows(t *testing.T, card uuid.UUID) [3]int {
	return [3]int{l.count(t, "SELECT count(*) FROM report_concept WHERE card_id = $1", card),
		l.count(t, "SELECT count(*) FROM report_fact WHERE card_id = $1", card),
		l.count(t, "SELECT count(*) FROM report_link WHERE card_id = $1", card)}
}

// judgedStudy finishes a study with a Grounding paper and settles its report
// run with two facts about its question, one Jev rejects, and links from the
// hand-in's first word to the paper and, never kept, to the question.
func (l *lab) judgedStudy(t *testing.T) db.Card {
	t.Helper()
	c, lead := l.finishedStudy(t)
	// A Grounding paper, the only kind of node besides datasets a link may
	// name, in a study whose scope now starts at Grounding.
	if _, err := l.env.Owner.Exec(context.Background(), "UPDATE card SET scope_from = 'grounding' WHERE id = $1", c.ID); err != nil {
		t.Fatal(err)
	}
	l.in(t, func(tx *store.Tx) error {
		_, err := records.WriteAs(context.Background(), tx, service.Writer{Card: c.ID, AgentRun: lead.ID, Column: "grounding", Role: "leader", Round: 1},
			"paper", review.PaperBody{ID: "arxiv:2101.00001", Text: "A paper."})
		return err
	})
	rep := l.studyReport(t, c)
	if rep.HandIn == nil || rep.Question == "" {
		t.Fatalf("no hand-in or question in %+v", rep)
	}
	paper := ""
	for _, n := range rep.Nodes {
		if n.Kind == "paper" {
			paper = n.ID
		}
	}
	if paper == "" {
		t.Fatalf("no paper node in %+v", rep.Nodes)
	}
	phrase := strings.Fields(rep.HandIn.Text)[0]
	l.jev(t, 200, levels(nil)).byState = func(state string) float64 {
		if strings.Contains(state, "Q (other) associated with R (other)") {
			return 0.3
		}
		return 0.9
	}
	l.settleReport(t, l.latestRun(t, c.ID, "reporter"), "completed", handin.ReportHandIn{Status: "done",
		Concepts: []handin.ReportConcept{{Key: "q", Label: "Q", Type: "other"}, {Key: "r", Label: "R", Type: "other"}},
		Facts: []handin.ReportFact{{Subject: "q", Relation: "mentioned_in", Object: "question", Quote: rep.Question, SourceNode: "question"},
			{Subject: "q", Relation: "associated_with", Object: "r", Quote: rep.Question, SourceNode: "question"}},
		Links: []handin.ReportLink{{Phrase: phrase, Occurrence: 1, Node: paper}, {Phrase: phrase, Occurrence: 1, Node: "question"}}})
	return c
}

func TestJudgeReportStoresAcceptedRows(t *testing.T) {
	l := newLab(t)
	c := l.judgedStudy(t)
	b, _ := l.reportBuild(t, c.ID)
	if b.Status != "done" || b.FinishedAt == nil {
		t.Fatalf("build %+v", b)
	}
	if got := l.reportRows(t, c.ID); got != [3]int{1, 1, 1} {
		t.Fatalf("concepts, facts, links = %v, want 1 1 1", got)
	}
	var key, relation string
	var p float64
	if err := l.env.Owner.QueryRow(context.Background(), `SELECT c.key, f.relation, f.p FROM report_concept c JOIN report_fact f ON f.card_id = c.card_id
		WHERE c.card_id = $1`, c.ID).Scan(&key, &relation, &p); err != nil {
		t.Fatal(err)
	}
	if key != "q" || relation != "mentioned_in" || p != 0.9 {
		t.Fatalf("kept %s %s %v", key, relation, p)
	}
	var node string
	if err := l.env.Owner.QueryRow(context.Background(), "SELECT node_id FROM report_link WHERE card_id = $1", c.ID).Scan(&node); err != nil {
		t.Fatal(err)
	}
	if node == "question" {
		t.Fatalf("kept the link to the question, not the paper")
	}
}

func TestJudgeReportIsIdempotent(t *testing.T) {
	ctx := context.Background()
	l := newLab(t)
	c := l.judgedStudy(t)
	first := l.reportRows(t, c.ID)
	if _, err := l.env.Owner.Exec(ctx, "UPDATE report_build SET status = 'judging' WHERE card_id = $1", c.ID); err != nil {
		t.Fatal(err)
	}
	if err := l.e.judgeReport(ctx, l.ws, c.ID); err != nil {
		t.Fatal(err)
	}
	if again := l.reportRows(t, c.ID); again != first || first == [3]int{} {
		t.Fatalf("rows %v after judging again, %v before", again, first)
	}
	if b, _ := l.reportBuild(t, c.ID); b.Status != "done" {
		t.Fatalf("status %s", b.Status)
	}
}

func TestJudgeReportFailedRunStoresNothing(t *testing.T) {
	l := newLab(t)
	c, _ := l.finishedStudy(t)
	before, _ := l.reportBuild(t, c.ID)
	reason := "the evidence is empty"
	l.settleReport(t, l.latestRun(t, c.ID, "reporter"), "completed", handin.ReportHandIn{Status: "could_not_do", Reason: &reason})
	b, _ := l.reportBuild(t, c.ID)
	if b.Status != "failed" || b.ChecksStatus != before.ChecksStatus {
		t.Fatalf("build %+v, checks before %s", b, before.ChecksStatus)
	}
	if got := l.reportRows(t, c.ID); got != [3]int{} {
		t.Fatalf("rows %v", got)
	}
}

func TestCheckReportWithoutJev(t *testing.T) {
	ctx := context.Background()
	l := newLab(t)
	c, _ := l.finishedStudy(t)
	if b, _ := l.reportBuild(t, c.ID); b.ChecksStatus != "done" {
		t.Fatalf("checks %s after the finish, want done", b.ChecksStatus)
	}
	if _, err := l.env.Owner.Exec(ctx, "UPDATE report_build SET checks_status = 'pending' WHERE card_id = $1", c.ID); err != nil {
		t.Fatal(err)
	}
	l.e.Jev = nil
	if err := l.e.checkReport(ctx, l.ws, c.ID); err != nil {
		t.Fatal(err)
	}
	if b, _ := l.reportBuild(t, c.ID); b.ChecksStatus != "unavailable" {
		t.Fatalf("checks %s", b.ChecksStatus)
	}
	if n := l.count(t, "SELECT count(*) FROM report_check WHERE card_id = $1", c.ID); n != 0 {
		t.Fatalf("%d checks", n)
	}
}

func TestResumeCancelsReportRunWhoseRuntimeIsGone(t *testing.T) {
	ctx := context.Background()
	l := newLab(t)
	c, _ := l.finishedStudy(t)
	r := l.latestRun(t, c.ID, "reporter")
	if err := l.e.resumeReports(ctx); err != nil {
		t.Fatal(err)
	}
	if got := l.latestRun(t, c.ID, "reporter"); got.Status != "queued" {
		t.Fatalf("a pinned queued run was %s", got.Status)
	}
	if _, err := l.env.Owner.Exec(ctx, "UPDATE agent_run SET pinned_runtime_id = NULL WHERE id = $1", r.ID); err != nil {
		t.Fatal(err)
	}
	if err := l.e.resumeReports(ctx); err != nil {
		t.Fatal(err)
	}
	if got := l.latestRun(t, c.ID, "reporter"); got.Status != "cancelled" {
		t.Fatalf("run %s, want cancelled", got.Status)
	}
	if b, _ := l.reportBuild(t, c.ID); b.Status != "failed" {
		t.Fatalf("build %s, want failed", b.Status)
	}
}

func TestReportBuildsToResumeQueued(t *testing.T) {
	ctx := context.Background()
	l := newLab(t)
	a := l.register(t, "d1", "claude")
	mk := func(runSQL string) uuid.UUID {
		c := l.study(t, "validation", true, nil)
		r := l.reportRun(t, c, a["claude"])
		if runSQL != "" {
			if _, err := l.env.Owner.Exec(ctx, "UPDATE agent_run SET "+runSQL+" WHERE id = $1", r.ID); err != nil {
				t.Fatal(err)
			}
		}
		l.in(t, func(tx *store.Tx) error {
			_, err := tx.Q.InsertReportBuild(ctx, db.InsertReportBuildParams{WorkspaceID: l.ws, CardID: c.ID, AgentRunID: &r.ID, Status: "queued", ChecksStatus: "done"})
			return err
		})
		return c.ID
	}
	waiting := mk("")
	unpinned := mk("pinned_runtime_id = NULL")
	settledOld := mk("status = 'completed', completed_at = now() - interval '5 minutes'")
	settledNew := mk("status = 'completed', completed_at = now()")
	rows, err := l.st.Global().ReportBuildsToResume(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := map[uuid.UUID]bool{}
	for _, r := range rows {
		got[r.CardID] = r.Judging
	}
	for name, want := range map[string]struct {
		id uuid.UUID
		in bool
	}{"waiting": {waiting, false}, "unpinned": {unpinned, true}, "settled long ago": {settledOld, true}, "settled just now": {settledNew, false}} {
		if j, in := got[want.id]; in != want.in || in && !j {
			t.Errorf("%s: resumable=%v judging=%v, want %v", name, in, j, want.in)
		}
	}
}

func TestReportFiguresJudgedFromImageOrScript(t *testing.T) {
	ctx := context.Background()
	l := newLab(t)
	stub := l.jev(t, 200, levels(nil))
	l.e.Blobs = storage.Local{Dir: t.TempDir()}
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewGray(image.Rect(0, 0, 3000, 200))); err != nil {
		t.Fatal(err)
	}
	if err := l.e.Blobs.Put(ctx, "plot", bytes.NewReader(buf.Bytes()), int64(buf.Len())); err != nil {
		t.Fatal(err)
	}
	maker, other := uuid.New(), uuid.New()
	rec := func(key, kind, blob string, run uuid.UUID, body map[string]any) db.Record {
		b, _ := json.Marshal(body)
		return db.Record{Key: key, Kind: kind, Body: b, BlobKey: blob, AgentRunID: &run}
	}
	in := report.Input{Records: []db.Record{
		rec("o1", "output", "plot", maker, map[string]any{"name": "plot.png", "content_type": "image/png", "size_bytes": buf.Len()}),
		rec("o2", "output", "", maker, map[string]any{"name": "out/fig.pdf", "content_type": "application/pdf"}),
		rec("o3", "output", "", other, map[string]any{"name": "none.pdf", "content_type": "application/pdf"}),
		rec("r1", "run", "", maker, map[string]any{"tool": "run_script", "path": "plot.py", "script": "import pylab; pylab.savefig('fig.pdf')"}),
		rec("r2", "run", "", maker, map[string]any{"tool": "run_code", "code": "print('unrelated')"}),
		// A script of none.pdf's run that never names it is not its producing script.
		rec("r3", "run", "", other, map[string]any{"tool": "run_code", "code": "print('other')"}),
	}}
	rep := report.Report{Nodes: []report.Node{
		{ID: "out:plot.png", Kind: "output", ContentType: "image/png"},
		{ID: "out:out/fig.pdf", Kind: "output", ContentType: "application/pdf"},
		{ID: "out:none.pdf", Kind: "output", ContentType: "application/pdf"},
		{ID: "rec:f1", Kind: "finding", Quote: "IL6 rises"},
	}, Edges: []report.Edge{{From: "out:plot.png", To: "rec:f1", Kind: "supports"}, {From: "out:out/fig.pdf", To: "rec:f1", Kind: "supports"},
		{From: "out:none.pdf", To: "rec:f1", Kind: "supports"}}}
	ev, err := l.e.reportEvidence(ctx, in, rep)
	if err != nil {
		t.Fatal(err)
	}
	checks, err := report.CheckFindings(ctx, retryAsker{&reviewPass{e: l.e, ws: l.ws, jev: l.e.Jev, retry: l.e.retry(), renewed: l.e.now()}}, rep, ev)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]report.FindingCheck{}
	for _, c := range checks {
		got[c.CheckID] = c
	}
	if len(checks) != 2 || got["figure:out:plot.png"].Basis != "image" || got["figure:out:out/fig.pdf"].Basis != "script" {
		t.Fatalf("checks %+v", checks)
	}
	if s := got["figure:out:out/fig.pdf"].Evidence; !strings.Contains(s, "savefig('fig.pdf')") || strings.Contains(s, "unrelated") {
		t.Fatalf("script evidence %q", s)
	}
	var images int
	for _, req := range stub.calls() {
		for ref, a := range req.Assets {
			images++
			if !strings.Contains(req.State, ref) || a.Type != "image" || a.Format != "png" || a.Data_base64 == "" {
				t.Fatalf("asset %s %+v in state %q", ref, a, req.State)
			}
		}
	}
	if images != 1 {
		t.Fatalf("%d images sent, want 1", images)
	}
}

func TestReportConceptTypeIsOneOfTheHandInsTypes(t *testing.T) {
	ctx := context.Background()
	l := newLab(t)
	c := l.study(t, "validation", true, nil)
	insert := func(typ string) error {
		_, err := l.env.Owner.Exec(ctx, `INSERT INTO report_concept(workspace_id,card_id,key,label,type) VALUES($1,$2,$3,'L',$3)`, l.ws, c.ID, typ)
		return err
	}
	for _, typ := range handin.ReportConceptTypes {
		if err := insert(typ); err != nil {
			t.Fatalf("%s: %v", typ, err)
		}
	}
	if err := insert("chemical"); err == nil {
		t.Fatal("a concept of type chemical was stored")
	}
}

// flakySources is stubSources whose lookup of down fails until it has been
// tried fails times, and always fails for broken.
type flakySources struct {
	stubSources
	down, broken string
	fails, tries int
}

func (s *flakySources) Paper(ctx context.Context, id string) (sources.Hit, error) {
	if id == s.broken {
		return sources.Hit{}, errors.New("sources down")
	}
	if id == s.down {
		if s.tries++; s.tries <= s.fails {
			return sources.Hit{}, errors.New("sources busy")
		}
	}
	return s.stubSources.Paper(ctx, id)
}

func TestReportNoveltyJudgedOnlyAgainstEachPapersOwnAbstract(t *testing.T) {
	ctx := context.Background()
	l := newLab(t)
	l.jev(t, 200, levels(nil))
	src := &flakySources{stubSources: stubSources{missing: map[string]bool{paperB: true}}, down: paperA, fails: 1, broken: paperB2}
	l.e.Sources = src
	l.e.Retry = func() *jev.Retry {
		return &jev.Retry{Waits: []time.Duration{time.Millisecond}, Max: time.Second, Sleep: func(context.Context, time.Duration) error { return nil }}
	}
	rec := func(key, kind string, body map[string]any) db.Record {
		b, _ := json.Marshal(body)
		return db.Record{Key: key, Kind: kind, Body: b}
	}
	in := report.Input{Records: []db.Record{
		// The call each paper was fetched in also answered with another paper's abstract.
		rec("c1", "call", map[string]any{"tool": "search_literature"}),
		rec("p1", "paper", map[string]any{"id": paperA, "text": "A", "cites": []string{"c1"}}),
		rec("p2", "paper", map[string]any{"id": paperB, "text": "B", "cites": []string{"c1"}}),
		rec("p3", "paper", map[string]any{"id": paperB2, "text": "B2", "cites": []string{"c1"}}),
	}}
	rep := report.Report{Nodes: []report.Node{
		{ID: "rec:p1", Kind: "paper", Label: paperA},
		{ID: "rec:p2", Kind: "paper", Label: paperB},
		{ID: "rec:p3", Kind: "paper", Label: paperB2},
		{ID: "rec:f1", Kind: "finding", Quote: "IL6 rises"},
	}}
	ev, err := l.e.reportEvidence(ctx, in, rep)
	if err != nil {
		t.Fatal(err)
	}
	if got := ev.Texts["paper:rec:p1"]; got != "Abstract of "+paperA {
		t.Fatalf("paper evidence %q, want only its own abstract", got)
	}
	for _, id := range []string{"paper:rec:p2", "paper:rec:p3"} {
		if s, ok := ev.Texts[id]; ok {
			t.Fatalf("%s has evidence %q though its abstract was not found", id, s)
		}
	}
	checks, err := report.CheckFindings(ctx, retryAsker{&reviewPass{e: l.e, ws: l.ws, jev: l.e.Jev, retry: l.e.retry(), renewed: l.e.now()}}, rep, ev)
	if err != nil {
		t.Fatal(err)
	}
	if len(checks) != 1 || checks[0].CheckID != "novelty:rec:p1" || checks[0].Evidence != "Abstract of "+paperA {
		t.Fatalf("checks %+v, want one novelty check on rec:p1's abstract", checks)
	}
}

func TestReportSkillNamesWhatCantBeLinked(t *testing.T) {
	for _, s := range []string{"must carry no Markdown marks", "never cross formatting",
		"DOIs, URLs, domains, emails and a Markdown link's text can't be linked"} {
		if !strings.Contains(reportSkill(), s) {
			t.Errorf("the report skill's link rule does not say %q", s)
		}
	}
}
