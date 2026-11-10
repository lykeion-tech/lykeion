package flow

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

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
		if err := tx.Q.InsertReportConcept(ctx, db.InsertReportConceptParams{WorkspaceID: l.ws, CardID: c.ID, Key: "k", Label: "K", Type: "t"}); err != nil {
			return err
		}
		if err := tx.Q.InsertReportFact(ctx, db.InsertReportFactParams{WorkspaceID: l.ws, CardID: c.ID, Subject: "k", Relation: "is_a", Object: "j", Quote: "q", SourceNode: "n", P: 0.9}); err != nil {
			return err
		}
		if err := tx.Q.InsertReportLink(ctx, db.InsertReportLinkParams{WorkspaceID: l.ws, CardID: c.ID, StartByte: 0, EndByte: 3, NodeID: "n", P: 0.9}); err != nil {
			return err
		}
		return tx.Q.InsertReportCheck(ctx, db.InsertReportCheckParams{WorkspaceID: l.ws, CardID: c.ID, NodeID: "n", CheckID: "figure:o", Question: "q", Choice: "yes", P: 0.9, Status: "pass", Evidence: "e"})
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
