package flow

import (
	"context"
	"slices"
	"testing"

	"github.com/google/uuid"

	"lykeion/server/internal/store"
)

func (l *lab) reportRecipients(t *testing.T, id uuid.UUID) []uuid.UUID {
	t.Helper()
	rows, err := l.env.Owner.Query(context.Background(), "SELECT user_id FROM inbox_item WHERE card_id = $1 AND kind = 'study_report'", id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var u uuid.UUID
		if err := rows.Scan(&u); err != nil {
			t.Fatal(err)
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestFinishRaisesAReportPerParticipant(t *testing.T) {
	l := newLab(t)
	l.legacy = true
	l.jev(t, 200, levels(nil))
	a := l.agent(t, "Curie", "claude", "", "You are Curie.")
	c := l.scoped(t, "grounding", "grounding", a)
	cm, dec, bystander := l.colleague(t, "c@lab.org"), l.colleague(t, "d@lab.org"), l.colleague(t, "e@lab.org")
	if _, err := l.env.Owner.Exec(context.Background(), "INSERT INTO comment (workspace_id, card_id, author_id, body) VALUES ($1, $2, $3, 'hi')", l.ws, c.ID, cm); err != nil {
		t.Fatal(err)
	}
	if _, err := l.env.Owner.Exec(context.Background(), "INSERT INTO inbox_item (workspace_id, card_id, user_id, kind, text, decided_by) VALUES ($1, $2, $3, 'no_tool', 'x', $4)", l.ws, c.ID, l.owner, dec); err != nil {
		t.Fatal(err)
	}
	l.done(t, c.ID)
	if got := l.card(t, c.ID); got.State != "done" {
		t.Fatalf("state %s", got.State)
	}
	got := l.reportRecipients(t, c.ID)
	if len(got) != 3 || slices.Contains(got, bystander) || !slices.Contains(got, l.owner) || !slices.Contains(got, cm) || !slices.Contains(got, dec) {
		t.Fatalf("report recipients %v", got)
	}
	if items := l.inbox(t, c.ID, "done"); len(items) != 0 {
		t.Fatalf("done items: %v", items)
	}
}

func TestFinishWorkflowKeepsDone(t *testing.T) {
	l := newLab(t)
	l.legacy = true
	l.jev(t, 200, levels(nil))
	a := l.agent(t, "Curie", "claude", "", "You are Curie.")
	c := l.scoped(t, "grounding", "grounding", a)
	if _, err := l.env.Owner.Exec(context.Background(), "UPDATE card SET workflow = 'w', workflow_release = 'v0.1.0' WHERE id = $1", c.ID); err != nil {
		t.Fatal(err)
	}
	l.done(t, c.ID)
	if items := l.inbox(t, c.ID, "done"); len(items) != 1 {
		t.Fatalf("done items: %v", items)
	}
	if got := l.reportRecipients(t, c.ID); len(got) != 0 {
		t.Fatalf("report recipients %v", got)
	}
}

func TestStoppedStudyRaisesNoReport(t *testing.T) {
	l := newLab(t)
	c, item := l.heldStudy(t)
	if err := l.e.Decide(context.Background(), l.ws, item, l.owner, "close", "no data"); err != nil {
		t.Fatal(err)
	}
	if got := l.card(t, c.ID); got.State != "stopped" {
		t.Fatalf("state %s", got.State)
	}
	if got := l.reportRecipients(t, c.ID); len(got) != 0 {
		t.Fatalf("report recipients %v", got)
	}
}

func TestFinishLeavesOutPrivateManualDecisions(t *testing.T) {
	l := newLab(t)
	a := l.agent(t, "Curie", "claude", "", "You are Curie.")
	c := l.scoped(t, "grounding", "grounding", a)
	noter := l.colleague(t, "n@lab.org")
	if _, err := l.env.Owner.Exec(context.Background(), `INSERT INTO inbox_item (workspace_id, card_id, user_id, kind, title, text, status, resolved_at, decided_at, decided_by)
		VALUES ($1, $2, $3, 'manual_decision', 'Note', 'private note', 'decided', now(), now(), $3)`, l.ws, c.ID, noter); err != nil {
		t.Fatal(err)
	}
	// The noter finishing the study is the one transaction that can see the note.
	if err := l.st.InWorkspace(context.Background(), l.ws, noter, func(tx *store.Tx) error {
		return l.e.finishStudy(context.Background(), tx, l.card(t, c.ID))
	}); err != nil {
		t.Fatal(err)
	}
	if got := l.reportRecipients(t, c.ID); slices.Contains(got, noter) || len(got) != 1 {
		t.Fatalf("report recipients %v", got)
	}
}
