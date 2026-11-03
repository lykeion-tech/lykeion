package flow

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
)

// wantStopDecision checks the one open item a study's stop raised: it offers
// redo, close and park, defaults to park, and applies the default in a week.
// Every stop's scenario test calls it.
func (l *lab) wantStopDecision(t *testing.T, card uuid.UUID) {
	t.Helper()
	rows, err := l.env.Owner.Query(context.Background(),
		"SELECT options, default_option, expires_at FROM inbox_item WHERE card_id = $1 AND resolved_at IS NULL AND kind IN ('blocked', 'needs_decision')", card)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var raw []byte
		var def string
		var expires *time.Time
		if err := rows.Scan(&raw, &def, &expires); err != nil {
			t.Fatal(err)
		}
		n++
		var opts []struct{ ID, Label, Consequence string }
		if err := json.Unmarshal(raw, &opts); err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, o := range opts {
			if o.Label == "" || o.Consequence == "" {
				t.Errorf("option %q lacks its words", o.ID)
			}
			ids = append(ids, o.ID)
		}
		if !slices.Equal(ids, []string{"redo", "close", "park"}) || def != "park" {
			t.Errorf("options %v default %q", ids, def)
		}
		if expires == nil || expires.Sub(l.e.now().Add(7*24*time.Hour)).Abs() > time.Minute {
			t.Errorf("expires_at %v", expires)
		}
	}
	if n != 1 {
		t.Fatalf("%d open stop items, want 1", n)
	}
}
