package flow

import (
	"strings"
	"testing"

	"lykeion/server/internal/pack"
)

func TestStudyCLIRoleModel(t *testing.T) {
	member := pack.Member{Agent: pack.AgentDef{Models: map[string]string{"claude": "sonnet", "codex": "gpt"}}}
	for _, tc := range []struct{ role, worker, audit, wantCLI, wantModel string }{
		{"leader", "claude", "codex", "claude", "sonnet"},
		{"auditor", "claude", "codex", "codex", "gpt"},
		{"member", "codex", "claude", "codex", "gpt"},
	} {
		cli, model := studyCLI(tc.role, tc.worker, tc.audit, member.Agent)
		if cli != tc.wantCLI || model != tc.wantModel {
			t.Errorf("%s: got %q/%q", tc.role, cli, model)
		}
	}
}

func TestStudyCLIDefaultForUnmappedCustomAgent(t *testing.T) {
	cli, model := studyCLI("auditor", "codex", "claude", pack.AgentDef{Harness: "codex", Model: "specific"})
	if cli != "claude" || model != "" {
		t.Fatalf("got %q/%q", cli, model)
	}
}

func TestSelectedStudyAndAuditorCLI(t *testing.T) {
	l := newLab(t)
	l.legacy = true
	c := l.study(t, "hypotheses", true, nil)
	if _, err := l.env.Owner.Exec(t.Context(), "UPDATE card SET study_cli='codex', auditor_cli='claude' WHERE id=$1", c.ID); err != nil {
		t.Fatal(err)
	}
	l.done(t, c.ID)
	var leader, auditor string
	if err := l.env.Owner.QueryRow(t.Context(), "SELECT harness FROM agent_run WHERE card_id=$1 AND role='leader'", c.ID).Scan(&leader); err != nil {
		t.Fatal(err)
	}
	if err := l.env.Owner.QueryRow(t.Context(), "SELECT harness FROM agent_run WHERE card_id=$1 AND role='auditor'", c.ID).Scan(&auditor); err != nil {
		t.Fatal(err)
	}
	if leader != "codex" || auditor != "claude" {
		t.Fatalf("leader %q auditor %q", leader, auditor)
	}
	var waiting *string
	if err := l.env.Owner.QueryRow(t.Context(), "SELECT agent_run_waiting(workspace_id, id) FROM agent_run WHERE card_id=$1 AND role='auditor'", c.ID).Scan(&waiting); err != nil {
		t.Fatal(err)
	}
	if waiting == nil || !strings.Contains(*waiting, "Claude Code") {
		t.Fatalf("auditor wait: %v", waiting)
	}
}
