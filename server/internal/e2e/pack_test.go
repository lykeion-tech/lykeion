//go:build unix

package e2e

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/google/uuid"

	"lykeion/server/internal/apiclient"
	"lykeion/server/internal/auth"
	"lykeion/server/internal/board"
	db "lykeion/server/pkg/db/generated"
)

// demoPack is a shipped pack with an author and a reviewer in every column.
// Each column's check passes once that column's author has written a notes
// record; a missing one sends the column back to itself.
func demoPack(t *testing.T) fstest.MapFS {
	t.Helper()
	fsys := fstest.MapFS{}
	cols, failures := map[string]any{}, map[string]string{}
	for _, c := range board.Stages {
		author, reviewer := "demo-"+c+"-author", "demo-"+c+"-reviewer"
		cols[c] = map[string]any{"phases": [][]string{{author}}, "reviewer": reviewer,
			"kinds": map[string]any{"notes": map[string]any{"roles": []string{author}}}}
		failures["missing_"+c+"_note"] = c
		fsys["demo/skills/"+c+"/"+author+"/SKILL.md"] = &fstest.MapFile{Data: []byte("Write the " + c + " note.")}
		fsys["demo/skills/"+c+"/"+reviewer+"/SKILL.md"] = &fstest.MapFile{Data: []byte("Review the " + c + " note.")}
		fsys["demo/checks/"+c+".star"] = &fstest.MapFile{Data: []byte(`def check(records):
    if [r for r in records if r["kind"] == "notes" and r["column"] == "` + c + `"]:
        return [{"id": "note", "ok": True}]
    return [{"id": "note", "ok": False, "failure": "missing_` + c + `_note"}]
`)}
	}
	m, err := json.Marshal(map[string]any{"name": "demo", "version": "1", "description": "Demo field",
		"start_kinds": []string{"question"}, "columns": cols, "failures": failures})
	must(t, err)
	fsys["demo/pack.json"] = &fstest.MapFile{Data: m}
	return fsys
}

func TestDemoPackAcrossBoard(t *testing.T) {
	l := newBoardLab(t)
	l.h.Shipped = demoPack(t)
	ctx := context.Background()
	api := &apiclient.Client{Server: l.url, Token: l.token, Workspace: l.ws.String()}
	must(t, api.Do(ctx, "PUT", "/api/me/pack", map[string]string{"name": "demo"}, nil))
	newCard := func(q string) uuid.UUID {
		var c struct{ ID uuid.UUID }
		must(t, api.Do(ctx, "POST", "/api/cards", map[string]any{"question": q, "start_kind": "question"}, &c))
		return c.ID
	}
	released := func(c db.Card) bool { return c.BoardColumn == "done" && c.Ending == "released" }
	passed := func(id uuid.UUID) []string {
		rows, err := l.env.Owner.Query(ctx, `SELECT DISTINCT board_column FROM record WHERE card_id = $1 AND kind = 'gate' AND body->>'verdict' = 'pass' ORDER BY 1`, id)
		must(t, err)
		defer rows.Close()
		var cols []string
		for rows.Next() {
			var c string
			must(t, rows.Scan(&c))
			cols = append(cols, c)
		}
		must(t, rows.Err())
		return cols
	}
	defer l.run(t)()

	card := newCard("Does X raise Y?")
	c := l.waitCard(t, card, "released", released)
	if c.PackID == nil || c.Reopens != 0 || c.Flag != "" {
		t.Fatalf("released card %+v", c)
	}
	if got := strings.Join(passed(card), ","); got != "experimentation,grounding,hypotheses,reporting,validation" {
		t.Fatalf("columns with a passing gate: %s", got)
	}

	// An Autoresearch run requested by the card's owner reopens it.
	run, token := uuid.New(), "ltk_"+uuid.NewString()
	if _, err := l.env.Owner.Exec(ctx, `INSERT INTO agent_run (id, workspace_id, kind, requester_id, responsible_user_id, role, board_column, round, harness, prompt, status, attempt)
		VALUES ($1, $2, 'autoresearch', $3, $3, 'autoresearch', '', 0, 'claude', 'p', 'running', 1)`, run, l.ws, l.owner); err != nil {
		t.Fatal(err)
	}
	if _, err := l.env.Owner.Exec(ctx, `INSERT INTO agent_run_token (token_hash, workspace_id, agent_run_id, role, attempt, scopes, expires_at)
		VALUES ($1, $2, $3, 'autoresearch', 1, '{card:reopen}', now() + interval '1 hour')`, auth.Hash(token), l.ws, run); err != nil {
		t.Fatal(err)
	}
	var reopened db.Card
	must(t, (&apiclient.Client{Server: l.url, Token: token}).Do(ctx, "POST", "/api/cards/"+card.String()+"/reopen",
		map[string]string{"reason": "a new study contradicts it", "evidence": "pmid:1"}, &reopened))
	if reopened.BoardColumn != "grounding" || reopened.Reopens != 1 || reopened.Ending != "" {
		t.Fatalf("reopened %+v", reopened)
	}
	if _, err := l.env.Owner.Exec(ctx, `UPDATE agent_run SET status = 'completed', completed_at = now() WHERE id = $1`, run); err != nil {
		t.Fatal(err)
	}
	if c := l.waitCard(t, card, "released again", released); c.Reopens != 1 {
		t.Fatalf("released again %+v", c)
	}

	// Validation's author writes nothing, so its check fails until the cap.
	t.Setenv("LYK_TEST_SKIP_NOTE", "validation")
	second := newCard("Does Z lower W?")
	c = l.waitCard(t, second, "Blocked", func(c db.Card) bool { return c.Flag == "escalated" })
	if c.BlockKind != "cap" || c.RouteBacks != 4 || c.BoardColumn != "validation" {
		t.Fatalf("capped card %+v", c)
	}
	if got := strings.Join(passed(second), ","); got != "experimentation,grounding,hypotheses" {
		t.Fatalf("columns with a passing gate: %s", got)
	}
}
