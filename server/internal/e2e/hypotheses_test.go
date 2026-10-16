package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"

	"lykeion/server/internal/auth"
	"lykeion/server/internal/hyp"
	"lykeion/server/internal/middleware"
	db "lykeion/server/pkg/db/generated"
)

// pastRecord writes a record ahead of a test through the sources server, as a
// run of role in column and round would. The run is running, with its token,
// only while the sources server records the call; then it is finished and
// moved to Grounding round 0, so no column's view of its current round counts
// it, skips a role for it, or checks its scope or mode. Call it before the
// lab's daemon and engine run.
func (l *boardLab) pastRecord(t *testing.T, card uuid.UUID, role, column string, round int32, kind, key string, body map[string]any, text string) {
	t.Helper()
	ctx := context.Background()
	run, token := uuid.New(), "ltk_"+uuid.NewString()
	if _, err := l.env.Owner.Exec(ctx, `INSERT INTO agent_run (id, workspace_id, card_id, responsible_user_id, board_column, round, role, harness, prompt, status, attempt)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'claude', 'p', 'running', 1)`, run, l.ws, card, l.owner, column, round, role); err != nil {
		t.Fatal(err)
	}
	if _, err := l.env.Owner.Exec(ctx, `INSERT INTO agent_run_token (token_hash, workspace_id, agent_run_id, card_id, role, attempt, expires_at)
		VALUES ($1, $2, $3, $4, $5, 1, now() + interval '1 hour')`, auth.Hash(token), l.ws, run, card, role); err != nil {
		t.Fatal(err)
	}
	r := middleware.Role{AgentRunID: run, WorkspaceID: l.ws, CardID: card, Role: role, Column: column, Round: round}
	if err := l.hosted.Record(ctx, r, token, kind, key, body, text); err != nil {
		t.Fatal(err)
	}
	if _, err := l.env.Owner.Exec(ctx, `UPDATE agent_run SET status = 'completed', completed_at = now(), board_column = 'grounding', round = 0 WHERE id = $1`, run); err != nil {
		t.Fatal(err)
	}
}

// noveltySearch records the search the fake novelty scout cites, as the
// sources server would. It found nothing, so the scout records unknown.
func (l *boardLab) noveltySearch(t *testing.T, card uuid.UUID) {
	l.pastRecord(t, card, hyp.NoveltyScout, "hypotheses", 2, "search", "",
		map[string]any{"database": "openalex", "query": "X raises Y prior work", "results": []string{}}, "")
}

func (l *boardLab) count(t *testing.T, q string, args ...any) int {
	t.Helper()
	var n int
	if err := l.env.Owner.QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// gates lists the card's gates in order as column:verdict:route.
func (l *boardLab) gates(t *testing.T, card uuid.UUID) string {
	t.Helper()
	rows, err := l.env.Owner.Query(context.Background(), `SELECT board_column || ':' || (body->>'verdict') || ':' || coalesce(body->'route'->>'column', '')
		FROM record WHERE card_id = $1 AND kind = 'gate' ORDER BY created_at`, card)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		must(t, rows.Scan(&s))
		out = append(out, s)
	}
	must(t, rows.Err())
	return strings.Join(out, " ")
}

func TestHypothesesPassAndTheCardMovesToExperimentation(t *testing.T) {
	t.Setenv("LYK_TEST_HYPOTHESES", "1")
	capture := t.TempDir()
	t.Setenv("LYK_TEST_HYP_OUTPUT", capture)
	l := newBoardLab(t)
	card := l.startCard(t)
	l.noveltySearch(t, card)
	defer l.run(t)()
	l.waitCard(t, card, "Experimentation", func(c db.Card) bool { return c.BoardColumn == "experimentation" })

	// These are stdout from the shipped CLI talking to the real role API.
	for file, pattern := range map[string]string{
		"candidate": `^H[0-9]+\n$`,
		"ranking":   `^1\. H[0-9]+ -?[0-9]+\.[0-9]{4}\n2\. H[0-9]+ -?[0-9]+\.[0-9]{4}\n$`,
	} {
		output, err := os.ReadFile(filepath.Join(capture, file))
		if err != nil {
			t.Fatal(err)
		}
		if !regexp.MustCompile(pattern).Match(output) {
			t.Errorf("%s CLI output: %q", file, output)
		}
	}
	candidate, err := os.ReadFile(filepath.Join(capture, "candidate"))
	must(t, err)
	if n := l.count(t, `SELECT count(*) FROM record WHERE card_id = $1 AND kind = 'candidate' AND key = $2`, card, strings.TrimSpace(string(candidate))); n != 1 {
		t.Fatalf("CLI returned a key absent from candidate records: %q", candidate)
	}
	var raw []byte
	must(t, l.env.Owner.QueryRow(context.Background(), `SELECT body FROM record WHERE card_id = $1 AND kind = 'ranking' AND superseded_at IS NULL`, card).Scan(&raw))
	var ranking struct {
		Order  []string           `json:"order"`
		Scores map[string]float64 `json:"scores"`
	}
	must(t, json.Unmarshal(raw, &ranking))
	var expected strings.Builder
	for i, key := range ranking.Order {
		fmt.Fprintf(&expected, "%d. %s %.4f\n", i+1, key, ranking.Scores[key])
	}
	output, err := os.ReadFile(filepath.Join(capture, "ranking"))
	must(t, err)
	if string(output) != expected.String() {
		t.Fatalf("CLI ranking %q, want server record %q", output, expected.String())
	}
	if g := l.gates(t, card); g != "grounding:pass: hypotheses:pass:" {
		t.Fatalf("gates %s", g)
	}
	live := `SELECT count(*) FROM record WHERE card_id = $1 AND board_column = 'hypotheses' AND superseded_at IS NULL AND `
	for what, q := range map[string]string{
		"three seeds":                     live + `kind = 'seed'`,
		"one duplicate merged":            live + `kind = 'dedup' AND jsonb_array_length(body->'survivors') = 2 AND body->'merged' <> '{}'::jsonb`,
		"Jev judged the pair":             live + `kind = 'pairwise' AND body->>'judge' = 'jev'`,
		"a server-fitted ranking":         live + `kind = 'ranking' AND jsonb_array_length(body->'order') = 2`,
		"the unit came from Grounding":    live + `kind = 'matrix' AND body->'measurement'->>'unit' = 'biological_replicates' AND body->'measurement'->>'accession' = 'GSE1'`,
		"novelty is unknown, never novel": live + `kind = 'novelty' AND body->>'status' = 'unknown'`,
	} {
		want := 1
		switch what {
		case "three seeds":
			want = 3
		case "novelty is unknown, never novel":
			want = 2
		}
		if n := l.count(t, q, card); n != want {
			t.Errorf("%s: %d, want %d", what, n, want)
		}
	}
	if n := l.count(t, `SELECT count(*) FROM agent_run WHERE card_id = $1 AND board_column = 'hypotheses' AND role = 'hypotheses-reviewer'`, card); n != 1 {
		t.Fatalf("%d reviewer runs", n)
	}
	// Doers resume one session per card and role; the reviewer starts fresh each round.
	for what, q := range map[string]string{
		"the doers' scopes name the card and role": `SELECT count(*) FROM agent_run WHERE card_id = $1 AND board_column = 'hypotheses'
			AND role <> 'hypotheses-reviewer' AND continuity_scope <> 'card:' || card_id || '/role:' || role`,
		"the reviewer's scope names its round": `SELECT count(*) FROM agent_run WHERE card_id = $1 AND role = 'hypotheses-reviewer'
			AND continuity_scope <> 'card:' || card_id || '/role:hypotheses-reviewer/round:' || round`,
		"every agent run recorded its mode": `SELECT count(*) FROM agent_run WHERE card_id = $1 AND board_column = 'hypotheses'
			AND profile_mode <> 'workspace_continuity'`,
	} {
		if n := l.count(t, q, card); n != 0 {
			t.Errorf("%s: %d agent runs do not", what, n)
		}
	}
}

func TestMissingPremiseReturnsTheCardToGrounding(t *testing.T) {
	t.Setenv("LYK_TEST_HYPOTHESES", "1")
	marker := filepath.Join(t.TempDir(), "missing-premise")
	must(t, os.WriteFile(marker, nil, 0o644))
	t.Setenv("LYK_TEST_MISSING_PREMISE", marker)
	l := newBoardLab(t)
	card := l.startCard(t)
	l.noveltySearch(t, card)
	defer l.run(t)()
	l.waitCard(t, card, "Experimentation", func(c db.Card) bool { return c.BoardColumn == "experimentation" })

	if g := l.gates(t, card); g != "grounding:pass: hypotheses:fail:grounding grounding:pass: hypotheses:pass:" {
		t.Fatalf("gates %s", g)
	}
	var note string
	must(t, l.env.Owner.QueryRow(context.Background(), `SELECT handoff_note FROM agent_run WHERE card_id = $1 AND board_column = 'grounding'
		AND role = 'literature-scout' ORDER BY created_at DESC LIMIT 1`, card).Scan(&note))
	if !strings.Contains(note, "missing premise: the dose at which X saturates Y") {
		t.Fatalf("the redone scout was not told the gap: %q", note)
	}
	// The first visit's candidates were kept until the stage roles redid their work.
	if n := l.count(t, `SELECT count(*) FROM record WHERE card_id = $1 AND board_column = 'hypotheses' AND kind = 'candidate'
		AND superseded_at IS NOT NULL`, card); n != 2 {
		t.Fatalf("%d superseded candidates", n)
	}
	if n := l.count(t, `SELECT count(*) FROM agent_run WHERE card_id = $1 AND board_column = 'hypotheses' AND role = 'decomposer'`, card); n != 1 {
		t.Fatalf("the decomposer ran %d times; it must not run on the first visit", n)
	}
	// Each generator ran on both visits in one scope, so its second visit could
	// resume the first.
	if n := l.count(t, `SELECT count(*) FROM (SELECT role FROM agent_run WHERE card_id = $1 AND role LIKE 'generator-%'
		GROUP BY role HAVING count(*) >= 2 AND count(DISTINCT continuity_scope) = 1) g`, card); n != 3 {
		t.Fatalf("%d generators kept one scope across both visits, want 3", n)
	}
}
