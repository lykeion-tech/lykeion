package flow

import (
	"context"
	"testing"
)

func TestJevReviewRecordsActualLifecycle(t *testing.T) {
	l := newLab(t)
	l.legacy = true
	stub := l.jev(t, 200, levels(nil))
	agent := l.agent(t, "Review agent", "claude", "", "Review.")
	card := l.scoped(t, "experimentation", "experimentation", agent)
	l.e.Kick(l.ws, card.ID)
	l.done(t, card.ID)
	if len(stub.calls()) == 0 {
		t.Fatal("expected an actual Jev request")
	}
	rows, err := l.env.Owner.Query(context.Background(), `SELECT type FROM event WHERE workspace_id=$1 AND card_id=$2 AND type IN ('review.started','review.finished') ORDER BY id`, l.ws, card.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		got = append(got, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "review.started" || got[1] != "review.finished" {
		t.Fatalf("lifecycle: %v", got)
	}
}
