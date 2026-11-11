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

// A report run's strict Codex settings read no AGENTS.md, so its role
// travels in the prompt, once; a study run still reads it from AGENTS.md.
func TestCodexReportRunGetsItsRoleOnce(t *testing.T) {
	const role = "# Reporter ROLE-MARKER"
	for _, c := range []struct {
		kind         string
		rolesInStdin int
	}{{"report", 1}, {"study", 0}} {
		t.Run(c.kind, func(t *testing.T) {
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
echo '{"type":"item.completed","item":{"type":"agent_message","text":"done"}}'
echo '{"type":"turn.completed","usage":{"input_tokens":1,"output_tokens":1}}'
`), 0700))
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{}`)
			}))
			defer srv.Close()
			state := filepath.Join(home, ".lyk", "daemon")
			runRoot := filepath.Join(home, ".lyk", "runs")
			must(t, os.MkdirAll(runRoot, 0700))
			rt, owner := uuid.New(), uuid.New()
			d := &daemon{cfg: Config{Home: runRoot, StateDir: state, Server: srv.URL, Harnesses: map[string]string{"codex": bin}}, api: &apiclient.Client{Server: srv.URL}, ownerOf: map[uuid.UUID]uuid.UUID{rt: owner}}
			d.cfg.NewBackend = agent.New
			d.out = &Outbox{Dir: filepath.Join(state, "outbox")}
			d.messages = &Outbox{Dir: filepath.Join(state, "messages")}
			d.scopes = map[string]bool{}
			cl := claimed{Token: "ltk_run"}
			cl.AgentRun.ID, cl.AgentRun.WorkspaceID, cl.AgentRun.ResponsibleUserID = uuid.New(), uuid.New(), owner
			cl.AgentRun.RuntimeID = &rt
			cl.AgentRun.Harness, cl.AgentRun.RequestedModel, cl.AgentRun.Attempt = "codex", "fixture", 1
			cl.AgentRun.Kind, cl.AgentRun.Skill, cl.AgentRun.Prompt = strings.TrimPrefix(c.kind, "study"), role, "Write the report from this brief."
			must(t, d.runAgentRun(t.Context(), cl))
			report, err := readReport(d.out.file(Report{AgentRunID: cl.AgentRun.ID, Attempt: 1}))
			must(t, err)
			if report.Kind != "complete" {
				t.Fatalf("run did not complete: %s %s", report.Kind, report.Error)
			}
			record := filepath.Join(d.runCacheDir(""), "npm")
			stdin, err := os.ReadFile(filepath.Join(record, "stdin"))
			must(t, err)
			args, err := os.ReadFile(filepath.Join(record, "args"))
			must(t, err)
			if n := strings.Count(string(stdin), "ROLE-MARKER"); n != c.rolesInStdin {
				t.Fatalf("role appears %d times in the prompt, want %d:\n%s", n, c.rolesInStdin, stdin)
			}
			if !strings.Contains(string(stdin), "Write the report from this brief.") {
				t.Fatalf("prompt lost its brief:\n%s", stdin)
			}
			if c.kind == "report" {
				for _, want := range []string{"project_doc_max_bytes=0\n", "--ignore-user-config\n", "--strict-config\n"} {
					if !strings.Contains(string(args), want) {
						t.Errorf("report run lost %q:\n%s", want, args)
					}
				}
			} else if strings.Contains(string(args), "project_doc_max_bytes=0") {
				t.Errorf("study run stopped reading AGENTS.md:\n%s", args)
			}
		})
	}
}
