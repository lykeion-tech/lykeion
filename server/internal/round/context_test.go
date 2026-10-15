package round

import (
	"context"
	"strings"
	"testing"

	"lykeion/server/internal/board"
)

func TestResearchContextReachesRolePromptsAsBackground(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	setContext := func(text string) {
		t.Helper()
		if _, err := f.env.Owner.Exec(ctx, `UPDATE workspace SET research_context = $1 WHERE id = $2`, text, f.ws); err != nil {
			t.Fatal(err)
		}
	}
	queued := func() string {
		t.Helper()
		card := f.card(t, board.Grounding, 1)
		if err := f.e.Advance(ctx, f.ws, card); err != nil {
			t.Fatal(err)
		}
		ts := f.agentRuns(t, card)
		if len(ts) != 1 {
			t.Fatalf("queued %d runs", len(ts))
		}
		return ts[0].Prompt
	}
	const label = "The lab's research context — background on who asks and why, never evidence:"

	setContext("We study hepatic gluconeogenesis in fasted mice.")
	p := queued()
	if !strings.Contains(p, label+"\nWe study hepatic gluconeogenesis in fasted mice.\n") {
		t.Fatalf("context missing or unlabelled:\n%s", p)
	}

	setContext("We now study zebrafish fin regeneration.")
	if p := queued(); !strings.Contains(p, "zebrafish fin regeneration") || strings.Contains(p, "gluconeogenesis") {
		t.Fatalf("the next run did not take the changed context:\n%s", p)
	}

	setContext("  ")
	if p := queued(); strings.Contains(p, "research context") {
		t.Fatalf("an empty context still has a heading:\n%s", p)
	}
}
