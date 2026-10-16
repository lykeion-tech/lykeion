package e2e

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	db "lykeion/server/pkg/db/generated"
)

func TestAddonReachesTheRolesButNotTheReviewer(t *testing.T) {
	capture := t.TempDir()
	t.Setenv("LYK_TEST_CAPTURE", capture)
	l := newBoardLab(t)
	ctx := context.Background()
	if _, err := l.env.Owner.Exec(ctx, `INSERT INTO stage_addon (workspace_id, board_column, skill_md, mcp, updated_by)
		VALUES ($1, 'grounding', 'Also search the lab index.', '[{"name":"lab-index","url":"http://127.0.0.1:9/mcp"}]', $2)`, l.ws, l.owner); err != nil {
		t.Fatal(err)
	}
	card := l.startCard(t)
	defer l.run(t)()
	l.waitCard(t, card, "Hypotheses", func(c db.Card) bool { return c.BoardColumn == "hypotheses" })

	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join(capture, name))
		if err != nil {
			t.Fatalf("%s was not captured: %v", name, err)
		}
		return string(b)
	}
	if s := read("scoper.skill"); !strings.Contains(s, "\n## Lab additions\n\nAlso search the lab index.\n") {
		t.Fatalf("scoper skill:\n%s", s)
	}
	cfg := read("scoper.mcp")
	if i, j := strings.Index(cfg, `"lyk_lab-index"`), strings.Index(cfg, `"lyk_sources"`); i < 0 || j < i {
		t.Fatalf("the add-on server must be written as lyk_lab-index, before sources: %s", cfg)
	}
	if a := read("scoper.args"); !strings.Contains(a, ",mcp__lyk_lab-index") {
		t.Fatalf("the add-on server's tools were not allowed: %s", a)
	}
	if s := read("grounding-reviewer.skill"); strings.Contains(s, "Lab additions") || strings.Contains(s, "lab index") {
		t.Fatalf("the reviewer got the add-on text:\n%s", s)
	}
	if a := read("grounding-reviewer.args"); strings.Contains(read("grounding-reviewer.mcp"), "lab-index") || strings.Contains(a, "lab-index") {
		t.Fatal("the reviewer got the add-on server")
	}

	var body []byte
	if err := l.env.Owner.QueryRow(ctx, `SELECT body FROM record WHERE card_id = $1 AND kind = 'gate'`, card).Scan(&body); err != nil {
		t.Fatal(err)
	}
	type provenance struct {
		AgentRun     string `json:"agent_run"`
		Role         string `json:"role"`
		Hash         string `json:"addon_hash"`
		ProfileMode  string `json:"profile_mode"`
		PolicyDigest string `json:"policy_digest"`
	}
	var g struct {
		Verdict string       `json:"verdict"`
		Addons  []provenance `json:"addons"`
	}
	if err := json.Unmarshal(body, &g); err != nil {
		t.Fatal(err)
	}
	rows, err := l.env.Owner.Query(ctx, `SELECT id::text, role, addon_hash, profile_mode, policy_digest, round
		FROM agent_run WHERE card_id = $1 AND board_column = 'grounding'`, card)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	expected := map[string]provenance{}
	daemonRuns := map[string]bool{}
	for rows.Next() {
		var p provenance
		var round int32
		if err := rows.Scan(&p.AgentRun, &p.Role, &p.Hash, &p.ProfileMode, &p.PolicyDigest, &round); err != nil {
			t.Fatal(err)
		}
		expected[p.AgentRun] = p
		if round == 1 {
			daemonRuns[p.AgentRun] = true
		} else if round != 0 {
			t.Fatalf("unexpected round %d for %s", round, p.AgentRun)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	// Earlier source-fetch fixture runs remain live and retain their stored provenance.
	if len(expected) != 12 || len(daemonRuns) != 6 || len(g.Addons) != len(expected) {
		t.Fatalf("gate coverage: stored=%d daemon=%d gate=%d", len(expected), len(daemonRuns), len(g.Addons))
	}
	hashes := map[string]string{}
	seen := map[string]bool{}
	for _, a := range g.Addons {
		p, ok := expected[a.AgentRun]
		if !ok || seen[a.AgentRun] || a != p {
			t.Fatalf("gate provenance differs from stored run or repeats it: got=%+v stored=%+v", a, p)
		}
		seen[a.AgentRun] = true
		if !daemonRuns[a.AgentRun] {
			continue
		}
		if _, duplicate := hashes[a.Role]; duplicate {
			t.Fatalf("duplicate daemon role: %s", a.Role)
		}
		hashes[a.Role] = a.Hash
		// No policy file: each daemon run uses the account's setup.
		if a.ProfileMode != "workspace_continuity" || a.PolicyDigest == "" {
			t.Fatalf("gate mode: %+v", a)
		}
	}
	if g.Verdict != "pass" || len(hashes) != 6 {
		t.Fatalf("gate provenance: %s %+v", g.Verdict, g.Addons)
	}
	if hash, ok := hashes["grounding-reviewer"]; !ok || hash != "" {
		t.Fatalf("reviewer hash: %q present=%v", hash, ok)
	}
	for _, role := range []string{"scoper", "literature-scout", "contrarian", "data-scout", "methods-scout"} {
		if hash := hashes[role]; len(hash) != 64 || hash != hashes["scoper"] {
			t.Fatalf("%s hash: %q scoper=%q", role, hash, hashes["scoper"])
		}
	}
}
