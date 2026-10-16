package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	_, payload, ok := strings.Cut(note, "missing premise: ")
	var missing hyp.MissingPremise
	if !ok || json.Unmarshal([]byte(payload), &missing) != nil || missing.Gap != "the dose at which X saturates Y" || missing.Step != "literature-scout" {
		t.Fatalf("the redone scout was not told the structured gap and target: %q", note)
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

func TestMetadataRepairReachesAcceptedMatrix(t *testing.T) {
	capture := t.TempDir()
	t.Setenv("LYK_TEST_HYP_OUTPUT", capture)
	l := metadataLab(t, "repair")
	card := l.metadataCard(t)
	defer l.run(t)()
	l.waitCard(t, card, "accepted metadata matrix", func(c db.Card) bool { return c.BoardColumn == "experimentation" || c.Flag == "escalated" })
	if c := l.card(t, card); c.BoardColumn != "experimentation" {
		t.Fatalf("metadata path failed: %+v", c)
	}
	if got := l.gates(t, card); got != "grounding:fail:grounding grounding:pass: hypotheses:pass:" {
		t.Fatalf("gates %s", got)
	}
	for _, file := range []string{"metadata-evidence", "candidate", "ranking"} {
		out, err := os.ReadFile(filepath.Join(capture, file))
		must(t, err)
		if len(out) == 0 {
			t.Fatalf("empty CLI output for %s", file)
		}
	}

	for file, kind := range map[string]string{"metadata-evidence": "evidence", "candidate": "candidate"} {
		out, err := os.ReadFile(filepath.Join(capture, file))
		must(t, err)
		pattern := `^E[0-9]+\n$`
		if kind == "candidate" {
			pattern = `^H[0-9]+\n$`
		}
		if !regexp.MustCompile(pattern).Match(out) {
			t.Fatalf("%s CLI stdout %q", kind, out)
		}
		if n := l.count(t, `SELECT count(*) FROM record WHERE card_id=$1 AND kind=$2 AND key=$3 AND superseded_at IS NULL`, card, kind, strings.TrimSpace(string(out))); n != 1 {
			t.Fatalf("%s stdout key not persisted", kind)
		}
	}
	// Matrix records have no key: successful CLI stdout is empty by contract.
	matrixOut, err := os.ReadFile(filepath.Join(capture, "matrix"))
	must(t, err)
	if len(matrixOut) != 0 {
		t.Fatalf("unexpected matrix stdout %q", matrixOut)
	}
	var ranking struct {
		Order  []string           `json:"order"`
		Scores map[string]float64 `json:"scores"`
	}
	var rankingRaw []byte
	must(t, l.env.Owner.QueryRow(context.Background(), `SELECT body FROM record WHERE card_id=$1 AND kind='ranking' AND superseded_at IS NULL`, card).Scan(&rankingRaw))
	must(t, json.Unmarshal(rankingRaw, &ranking))
	var expected strings.Builder
	for i, key := range ranking.Order {
		fmt.Fprintf(&expected, "%d. %s %.4f\n", i+1, key, ranking.Scores[key])
	}
	ranked, err := os.ReadFile(filepath.Join(capture, "ranking"))
	must(t, err)
	if string(ranked) != expected.String() || len(ranking.Order) != 2 {
		t.Fatalf("ranking stdout %q, persisted %q", ranked, expected.String())
	}
	for kind, want := range map[string]bool{"open_dataset": false, "fetch": true} {
		var key string
		must(t, l.env.Owner.QueryRow(context.Background(), `SELECT blob_key FROM record WHERE card_id=$1 AND kind=$2 AND key=$3 ORDER BY created_at DESC LIMIT 1`, card, kind, map[string]string{"open_dataset": "GSE1", "fetch": "geo:GSE1"}[kind]).Scan(&key))
		body, err := l.hosted.Blobs.Get(context.Background(), key)
		must(t, err)
		text, err := io.ReadAll(body)
		must(t, err)
		must(t, body.Close())
		if strings.Contains(string(text), "Each mouse was collected at the same time after treatment") != want {
			t.Fatalf("%s collection premise: %q", kind, text)
		}
	}
	metadataKey, err := os.ReadFile(filepath.Join(capture, "metadata-evidence"))
	must(t, err)
	if n := l.count(t, `SELECT count(*) FROM record WHERE card_id=$1 AND kind='decomposition' AND superseded_at IS NULL AND EXISTS (SELECT 1 FROM jsonb_array_elements(body->'assumptions') a WHERE a->'claims' ? $2)`, card, strings.TrimSpace(string(metadataKey))); n != 2 {
		t.Fatalf("decompositions citing metadata E: %d", n)
	}
	if n := l.count(t, `SELECT count(*) FROM record r, jsonb_array_elements(r.body->'checks') ch WHERE card_id=$1 AND kind='gate' AND board_column='hypotheses' AND ch->>'id' LIKE 'disagree.%' AND ch->>'result'='pass'`, card); n != 3 {
		t.Fatalf("pairwise non-overlap checks: %d", n)
	}
	for name, q := range map[string]string{
		"metadata fetch":      `kind='fetch' AND body->>'source_kind'='dataset_metadata' AND body->>'source_id'='geo:GSE1' AND body->>'full_text'='false'`,
		"metadata E":          `kind='evidence' AND body->>'source_id'='geo:GSE1' AND body->>'quote'='Each mouse was collected at the same time after treatment'`,
		"dataset measurement": `kind='matrix' AND body->'measurement'->>'accession'='GSE1' AND body->'measurement'->>'unit'='biological_replicates'`,
	} {
		if n := l.count(t, `SELECT count(*) FROM record WHERE card_id=$1 AND superseded_at IS NULL AND `+q, card); n != 1 && !(name == "metadata fetch" && n > 1) {
			t.Errorf("%s: %d", name, n)
		}
	}
	if n := l.count(t, `SELECT count(*) FROM record WHERE card_id=$1 AND kind='gate' AND board_column='hypotheses' AND body->>'verdict'='pass' AND NOT EXISTS (SELECT 1 FROM jsonb_array_elements(body->'checks') ch WHERE ch->>'result'<>'pass')`, card); n != 1 {
		t.Fatalf("accepted gate with non-overlap checks: %d", n)
	}
	var matrix hyp.Matrix
	var raw []byte
	must(t, l.env.Owner.QueryRow(context.Background(), `SELECT body FROM record WHERE card_id=$1 AND kind='matrix' AND superseded_at IS NULL`, card).Scan(&raw))
	must(t, json.Unmarshal(raw, &matrix))
	if len(matrix.Rivals) != 3 {
		t.Fatalf("rivals %+v", matrix.Rivals)
	}
	if n := l.count(t, `SELECT count(*) FROM record m JOIN record r ON r.card_id=m.card_id AND r.kind='ranking' AND r.superseded_at IS NULL WHERE m.card_id=$1 AND m.kind='matrix' AND m.superseded_at IS NULL AND EXISTS (SELECT 1 FROM jsonb_array_elements(m.body->'rivals') x WHERE x->>'candidate'=r.body->'order'->>0) AND EXISTS (SELECT 1 FROM jsonb_array_elements(m.body->'rivals') x WHERE x->>'kind'='null')`, card); n != 1 {
		t.Fatal("matrix omitted top or null")
	}
}

func TestUnresolvedMetadataDoesNotAdvance(t *testing.T) {
	l := metadataLab(t, "unresolved")
	card := l.metadataCard(t)
	defer l.run(t)()
	c := l.waitCard(t, card, "unresolved metadata escalation", func(c db.Card) bool { return c.Flag == "escalated" })
	if c.BoardColumn != "grounding" || c.Rounds != 3 {
		t.Fatalf("unresolved metadata: %+v", c)
	}
	if n := l.count(t, `SELECT count(*) FROM record WHERE card_id=$1 AND (kind IN ('matrix','nothing_found') OR (kind='evidence' AND body->>'source_id'='geo:GSE1') OR (kind='gate' AND body->>'verdict'='pass'))`, card); n != 0 {
		t.Fatalf("fabricated evidence or advancement: %d", n)
	}
	if n := l.count(t, `SELECT count(*) FROM record WHERE card_id=$1 AND kind='verdict' AND body->>'result'='fail' AND body->>'reason' LIKE '%collection timing remains unknown%'`, card); n != 3 {
		t.Fatalf("concrete reviewer failures: %d", n)
	}
	if n := l.count(t, `SELECT count(*) FROM record WHERE card_id=$1 AND kind='fetch' AND body->>'source_kind'='dataset_metadata'`, card); n < 1 {
		t.Fatal("metadata never inspected")
	}
}

func TestMetadataRunFailureDoesNotAdvance(t *testing.T) {
	for _, mode := range []string{"refused-command", "quota-failure"} {
		t.Run(mode, func(t *testing.T) {
			l := metadataLab(t, mode)
			card := l.metadataCard(t)
			defer l.run(t)()
			c := l.waitCard(t, card, "provider failure escalation", func(c db.Card) bool { return c.Flag == "escalated" })
			if c.BoardColumn != "grounding" || c.Ending != "" {
				t.Fatalf("failed provider advanced: %+v", c)
			}
			if n := l.count(t, `SELECT count(*) FROM record WHERE card_id=$1 AND kind IN ('matrix','verdict','gate')`, card); n != 0 {
				t.Fatalf("successful scientific claim after failed run: %d", n)
			}
			if n := l.count(t, `SELECT count(*) FROM agent_run WHERE card_id=$1 AND harness<>'claude'`, card); n != 0 {
				t.Fatal("switched harness")
			}
			if n := l.count(t, `SELECT count(*) FROM card WHERE workspace_id=$1`, l.ws); n != 1 {
				t.Fatal("replaced card")
			}
			if n := l.count(t, `SELECT count(*) FROM agent_run r WHERE card_id=$1 AND role='literature-scout' AND status='failed' AND error LIKE '%' || $2 || '%'`, card, mode); n != 1 {
				t.Fatalf("provider failure not persisted: %d", n)
			}
			if n := l.count(t, `SELECT count(*) FROM agent_run_usage u JOIN agent_run r ON r.id=u.agent_run_id WHERE r.card_id=$1 AND r.role='literature-scout'`, card); n != 0 {
				t.Fatalf("invented confirmed zero usage: %d", n)
			}
		})
	}
}
