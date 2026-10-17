package round

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	"lykeion/server/internal/board"
	"lykeion/server/internal/gate"
	"lykeion/server/internal/ground"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

func TestReviewHarness(t *testing.T) {
	for _, tc := range []struct {
		authors, online []string
		fallback        string
		harness, indep  string
	}{
		{[]string{"claude"}, []string{"claude", "codex"}, "claude", "codex", "different_family"},
		{[]string{"claude"}, []string{"claude"}, "claude", "claude", "same_family"},
		{[]string{"claude"}, []string{"cursor"}, "antigravity", "antigravity", "same_family"},
		{[]string{"claude", "codex"}, []string{"codex", "antigravity"}, "claude", "antigravity", "different_family"},
		{[]string{"cursor"}, []string{"codex"}, "claude", "claude", "same_family"},
	} {
		h, ind := ReviewHarness(tc.authors, tc.online, tc.fallback)
		if h != tc.harness || ind != tc.indep {
			t.Errorf("%v on %v: got %s %s, want %s %s", tc.authors, tc.online, h, ind, tc.harness, tc.indep)
		}
	}
}

// The reviewer is queued on the owner's online harness of another family,
// and the decided gate and the reviewer's choice records say so.
func TestReviewerRunsOnAnotherFamilyAndTheGateSaysSo(t *testing.T) {
	f := setup(t)
	ctx := t.Context()
	card := f.card(t, board.Grounding, 1)
	author := uuid.New()
	if _, err := f.env.Owner.Exec(ctx, `INSERT INTO agent_run (id, workspace_id, card_id, responsible_user_id, board_column, round, role, harness, prompt, status)
		VALUES ($1, $2, $3, $4, 'grounding', 1, 'scoper', 'claude', 'p', 'completed')`, author, f.ws, card, f.owner); err != nil {
		t.Fatal(err)
	}
	f.result(t, author, "nothing_found", "none")
	for _, h := range []string{"claude", "codex"} {
		if _, err := f.env.Owner.Exec(ctx, `INSERT INTO runtime (workspace_id, owner_id, daemon_id, harness) VALUES ($1, $2, 'd', $3)`, f.ws, f.owner, h); err != nil {
			t.Fatal(err)
		}
	}
	roles := StageRoles[board.Grounding]
	set := ParseSettings(nil)
	f.inWS(t, func(tx *store.Tx) error {
		c, err := tx.Q.LockCard(ctx, db.LockCardParams{ID: card, WorkspaceID: f.ws})
		if err != nil {
			return err
		}
		return f.e.queue(ctx, tx, c, board.FromCard(c), roles, set, []string{roles.Reviewer}, "")
	})
	var rev db.AgentRun
	for _, r := range f.agentRuns(t, card) {
		if r.Role == ground.Reviewer {
			rev = r
		}
	}
	if rev.Harness != "codex" {
		t.Fatalf("the reviewer was queued on %q, want codex", rev.Harness)
	}
	if _, err := f.env.Owner.Exec(ctx, `UPDATE agent_run SET status = 'completed', model = 'gpt-test' WHERE id = $1`, rev.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.env.Owner.Exec(ctx, `INSERT INTO record (workspace_id, card_id, board_column, round, kind, role, agent_run_id, body)
		VALUES ($1, $2, 'grounding', 1, 'review_item', $3, $4, '{"check":"gap","result":"pass","note":"fine"}')`, f.ws, card, ground.Reviewer, rev.ID); err != nil {
		t.Fatal(err)
	}
	choice, _ := json.Marshal(gate.Choice{Question: "gap", Judge: "reviewer"})
	jevChoice, _ := json.Marshal(gate.Choice{Question: "other", Judge: "jev"})
	for key, body := range map[string][]byte{"gap": choice, "other": jevChoice} {
		if _, err := f.env.Owner.Exec(ctx, `INSERT INTO record (workspace_id, card_id, board_column, round, kind, role, key, body)
			VALUES ($1, $2, 'grounding', 1, 'choice', 'server', $3, $4)`, f.ws, card, key, body); err != nil {
			t.Fatal(err)
		}
	}
	f.inWS(t, func(tx *store.Tx) error {
		c, err := tx.Q.LockCard(ctx, db.LockCardParams{ID: card, WorkspaceID: f.ws})
		if err != nil {
			return err
		}
		g := board.Gate{Column: board.Grounding, Round: 1, Verdict: board.VPass, Checks: []board.Check{
			{ID: "gap", Judge: board.Reviewer, Result: board.Pass, Note: "reviewer: fine"},
			{ID: "code", Judge: board.Code, Result: board.Pass, Note: "ok"}}}
		return f.e.finish(ctx, tx, c, board.FromCard(c), roles, set, &g)
	})
	g := f.gateBody(t, card, 1)
	if !strings.HasSuffix(g.Checks[0].Note, "independence: different_family") || strings.Contains(g.Checks[1].Note, "independence") {
		t.Fatalf("check notes: %+v", g.Checks)
	}
	rows, err := f.env.Owner.Query(ctx, `SELECT key, body FROM record WHERE card_id = $1 AND kind = 'choice' AND superseded_at IS NULL`, card)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]gate.Choice{}
	for rows.Next() {
		var key string
		var body []byte
		rows.Scan(&key, &body)
		var c gate.Choice
		json.Unmarshal(body, &c)
		got[key] = c
	}
	if c := got["gap"]; len(got) != 2 || c.Independence != "different_family" || c.JudgeHarness != "codex" || c.JudgeModel != "gpt-test" {
		t.Fatalf("choices: %+v", got)
	}
	if c := got["other"]; c.Independence != "" || c.JudgeHarness != "" {
		t.Fatalf("a Jev choice was annotated: %+v", c)
	}
}
