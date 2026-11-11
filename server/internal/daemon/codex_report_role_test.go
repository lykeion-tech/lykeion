package daemon

import (
	"fmt"
	"lykeion/server/internal/apiclient"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"lykeion/server/pkg/agent"
)

// codexRig is a daemon whose Codex is a script that records its arguments and
// stdin and the AGENTS.md in its folder, and starts or resumes the session
// codexSession.
type codexRig struct {
	d             *daemon
	ws, owner, rt uuid.UUID
}

const codexSession = "0199aaaa-bbbb-7ccc-8ddd-eeeeffff0001"

func newCodexRig(t *testing.T) codexRig {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	must(t, os.MkdirAll(filepath.Join(home, ".codex"), 0700))
	bin := filepath.Join(home, "installation", "codex")
	must(t, os.MkdirAll(filepath.Dir(bin), 0700))
	must(t, os.WriteFile(bin, []byte(`#!/bin/sh
mkdir -p "$npm_config_cache"
printf '%s\n' "$@" > "$npm_config_cache/args"
cat > "$npm_config_cache/stdin"
rm -f "$npm_config_cache/AGENTS.md"
cp AGENTS.md "$npm_config_cache/AGENTS.md" 2>/dev/null || true
mkdir -p "$CODEX_HOME/sessions/2026/01/01"
touch "$CODEX_HOME/sessions/2026/01/01/rollout-fixture-`+codexSession+`.jsonl"
echo '{"type":"thread.started","thread_id":"`+codexSession+`"}'
echo '{"type":"item.completed","item":{"type":"agent_message","text":"done"}}'
echo '{"type":"turn.completed","usage":{"input_tokens":1,"output_tokens":1}}'
`), 0700))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{}`)
	}))
	t.Cleanup(srv.Close)
	state := filepath.Join(home, ".lyk", "daemon")
	runRoot := filepath.Join(home, ".lyk", "runs")
	must(t, os.MkdirAll(runRoot, 0700))
	g := codexRig{ws: uuid.New(), owner: uuid.New(), rt: uuid.New()}
	g.d = &daemon{cfg: Config{Home: runRoot, StateDir: state, Server: srv.URL, Harnesses: map[string]string{"codex": bin}}, api: &apiclient.Client{Server: srv.URL}, ownerOf: map[uuid.UUID]uuid.UUID{g.rt: g.owner}}
	g.d.cfg.NewBackend = agent.New
	g.d.out = &Outbox{Dir: filepath.Join(state, "outbox")}
	g.d.messages = &Outbox{Dir: filepath.Join(state, "messages")}
	g.d.scopes = map[string]bool{}
	return g
}

// run runs one agent run of kind with role, continuing scope when it is set,
// and returns what the CLI read on stdin, its arguments and the AGENTS.md it
// found, if any.
func (g codexRig) run(t *testing.T, kind, role, scope string) (stdin, args, agentsMD string) {
	t.Helper()
	cl := claimed{Token: "ltk_run"}
	cl.AgentRun.ID, cl.AgentRun.WorkspaceID, cl.AgentRun.ResponsibleUserID = uuid.New(), g.ws, g.owner
	cl.AgentRun.RuntimeID = &g.rt
	cl.AgentRun.Harness, cl.AgentRun.RequestedModel, cl.AgentRun.Attempt = "codex", "fixture", 1
	cl.AgentRun.Kind, cl.AgentRun.Skill, cl.AgentRun.Prompt = kind, role, "Write the report from this brief."
	cl.AgentRun.ContinuityScope = scope
	must(t, g.d.runAgentRun(t.Context(), cl))
	report, err := readReport(g.d.out.file(Report{AgentRunID: cl.AgentRun.ID, Attempt: 1}))
	must(t, err)
	if report.Kind != "complete" {
		t.Fatalf("run did not complete: %s %s", report.Kind, report.Error)
	}
	record := filepath.Join(g.d.runCacheDir(""), "npm")
	in, err := os.ReadFile(filepath.Join(record, "stdin"))
	must(t, err)
	a, err := os.ReadFile(filepath.Join(record, "args"))
	must(t, err)
	doc, _ := os.ReadFile(filepath.Join(record, "AGENTS.md"))
	return string(in), string(a), string(doc)
}

const reportRole = "# Reporter ROLE-MARKER"

// A report run's strict Codex settings read no AGENTS.md, so its role
// travels in the prompt, once; a study run still reads it from AGENTS.md.
func TestCodexReportRunGetsItsRoleOnce(t *testing.T) {
	for _, c := range []struct {
		name, kind   string
		rolesInStdin int
	}{{"report", "report", 1}, {"study", "", 0}} {
		t.Run(c.name, func(t *testing.T) {
			stdin, args, doc := newCodexRig(t).run(t, c.kind, reportRole, "")
			if n := strings.Count(stdin, "ROLE-MARKER"); n != c.rolesInStdin {
				t.Fatalf("role appears %d times in the prompt, want %d:\n%s", n, c.rolesInStdin, stdin)
			}
			if !strings.Contains(stdin, "Write the report from this brief.") {
				t.Fatalf("prompt lost its brief:\n%s", stdin)
			}
			if c.kind == "report" {
				for _, want := range []string{"project_doc_max_bytes=0\n", "--ignore-user-config\n", "--strict-config\n"} {
					if !strings.Contains(args, want) {
						t.Errorf("report run lost %q:\n%s", want, args)
					}
				}
				return
			}
			if strings.Contains(args, "project_doc_max_bytes=0") {
				t.Errorf("study run stopped reading AGENTS.md:\n%s", args)
			}
			if !strings.Contains(doc, "ROLE-MARKER") {
				t.Errorf("study run's AGENTS.md lacks its role: %q", doc)
			}
		})
	}
}

// A report run that resumes its session finds its role already in it.
func TestCodexReportRunResumedGetsNoSecondRole(t *testing.T) {
	g := newCodexRig(t)
	scope := boardScope(uuid.New(), "reporter")
	stdin, args, _ := g.run(t, "report", reportRole, scope)
	if strings.Contains(args, "resume") || strings.Count(stdin, "ROLE-MARKER") != 1 {
		t.Fatalf("fresh run: args\n%s\nstdin\n%s", args, stdin)
	}
	stdin, args, _ = g.run(t, "report", reportRole, scope)
	if !strings.Contains(args, "resume\n"+codexSession+"\n") {
		t.Fatalf("second run did not resume %s:\n%s", codexSession, args)
	}
	if n := strings.Count(stdin, "ROLE-MARKER"); n != 0 {
		t.Fatalf("resumed run got its role %d more times:\n%s", n, stdin)
	}
}
