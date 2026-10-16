package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"lykeion/server/internal/apiclient"
	"lykeion/server/pkg/agent"
)

type fallbackBackend struct {
	agent.Backend
	before func(agent.ExecOptions)
}

func (b fallbackBackend) Execute(ctx context.Context, p string, o agent.ExecOptions) (*agent.Session, error) {
	b.before(o)
	return b.Backend.Execute(ctx, p, o)
}
func TestPrivateDiscoveryFallsBackFresh(t *testing.T) {
	for _, fault := range []string{"", "notice", "journal", "cancel", "collision", "symlink", "unowned", "launch", "second-discovery", "mcp-symlink"} {
		t.Run("fault="+fault, func(t *testing.T) { testPrivateFallback(t, fault) })
	}
}
func testPrivateFallback(t *testing.T, fault string) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	state, home := t.TempDir(), t.TempDir()
	c := claimFor(uuid.New(), uuid.New(), uuid.New(), "analysis")
	rt := uuid.New()
	c.AgentRun.RuntimeID = &rt
	c.AgentRun.Skill = "server skill"
	c.Token = "ltk_test"
	c.AgentRun.SessionID = "prior"
	scope := filepath.Join(home, privateScratch(c))
	must(t, os.MkdirAll(filepath.Join(scope, ".cli-home"), 0700))
	must(t, os.WriteFile(filepath.Join(scope, ".agent-owned"), []byte("lykeion-agent-v1:claude"), 0600))
	refused := filepath.Join(scope, ".cli-home", ".claude.json")
	must(t, os.WriteFile(filepath.Join(scope, ".cli-home", ".credentials.json"), []byte(`{}`), 0600))
	must(t, os.WriteFile(refused, []byte("preserved discovery"), 0600))
	must(t, os.WriteFile(filepath.Join(scope, "history"), []byte("preserved history"), 0600))
	must(t, os.MkdirAll(filepath.Join(scope, ".cli-home", "projects", "old"), 0700))
	must(t, os.WriteFile(filepath.Join(scope, ".cli-home", "projects", "old", "prior.jsonl"), []byte("old session"), 0600))
	must(t, os.WriteFile(filepath.Join(scope, ".cli-home", ".lyk-usage.json"), []byte(`{"version":1,"session":"prior","models":{"test":{}}}`), 0600))
	bin := filepath.Join(t.TempDir(), "claude")
	must(t, os.WriteFile(bin, []byte("#!/bin/sh\necho \"$PWD\" >> '"+filepath.Join(filepath.Dir(bin), "launches")+"'\nprintf '%s\\n' '{\"type\":\"result\",\"subtype\":\"success\",\"session_id\":\"fresh\",\"total_cost_usd\":0}'\n"), 0700))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{}`)) }))
	defer srv.Close()
	d := &daemon{cfg: Config{StateDir: state, Home: home}, api: &apiclient.Client{Server: srv.URL}}
	d.out = &Outbox{Dir: filepath.Join(state, "outbox")}
	d.messages = &Outbox{Dir: filepath.Join(state, "messages")}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if fault == "collision" {
		must(t, os.Mkdir(filepath.Join(home, scratchName(c.AgentRun.ID, c.AgentRun.Attempt)), 0700))
	}
	if fault == "mcp-symlink" {
		must(t, os.Symlink("missing", filepath.Join(scope, ".lyk-mcp.json")))
	}
	if fault == "symlink" {
		must(t, os.Remove(refused))
		must(t, os.Symlink("missing", refused))
	}
	if fault == "unowned" {
		must(t, os.Remove(filepath.Join(scope, ".agent-owned")))
	}
	if fault == "launch" {
		must(t, os.Remove(refused))
		must(t, os.Remove(bin))
	}
	calls := 0
	d.cfg.NewBackend = func(string, string) (agent.Backend, error) {
		return fallbackBackend{Backend: agent.Claude{Bin: bin}, before: func(o agent.ExecOptions) {
			calls++
			if calls == 1 {
				if fault == "" && o.Resume != "prior" {
					t.Error("fixture did not attempt prior session")
				}
				switch fault {
				case "notice":
					must(t, os.WriteFile(d.messages.Dir, []byte("blocked"), 0600))
				case "journal":
					p := d.journal().file(Report{AgentRunID: c.AgentRun.ID, Attempt: c.AgentRun.Attempt})
					must(t, os.Remove(p))
					must(t, os.Mkdir(p, 0700))
					must(t, os.WriteFile(filepath.Join(p, "blocked"), nil, 0600))
				case "cancel":
					cancel()
				}
			}
			if calls == 2 {
				if fault == "second-discovery" {
					must(t, os.MkdirAll(filepath.Join(o.Dir, ".cli-home"), 0700))
					must(t, os.WriteFile(filepath.Join(o.Dir, ".agent-owned"), []byte("lykeion-agent-v1:claude"), 0600))
					must(t, os.WriteFile(filepath.Join(o.Dir, ".cli-home", "settings.json"), []byte(`{}`), 0600))
				}
				if o.Dir != filepath.Join(home, scratchName(c.AgentRun.ID, c.AgentRun.Attempt)) || o.Inherit || o.Resume != "" || len(o.ClaudeBaseline) != 0 {
					t.Errorf("fresh options: %+v", o)
				}
				for _, name := range []string{"history", ".cli-home/.claude.json", ".cli-home/.lyk-usage.json", ".cli-home/projects"} {
					if _, err := os.Stat(filepath.Join(o.Dir, name)); !os.IsNotExist(err) {
						t.Errorf("copied %s", name)
					}
				}
				j, err := readReport(d.journal().file(Report{AgentRunID: c.AgentRun.ID, Attempt: c.AgentRun.Attempt}))
				must(t, err)
				if j.Scratch != "" {
					t.Error("journal points at old scope")
				}
				entries, err := os.ReadDir(d.messages.Dir)
				must(t, err)
				if len(entries) == 0 {
					t.Error("missing durable notice")
				}
				lock, err := agent.ReserveScratch(scope)
				if err == nil {
					lock.Release()
					t.Error("original lock released")
				}
			}
		}}, nil
	}
	must(t, SavePolicy(state, c.AgentRun.WorkspaceID, Policy{Mode: ModePrivate, RetentionDays: 7}))
	runErr := d.runAgentRun(ctx, c)
	if fault == "notice" || fault == "journal" {
		if runErr == nil {
			t.Fatal("durability failure lost")
		}
	} else {
		must(t, runErr)
	}
	r, err := readReport(d.out.file(Report{AgentRunID: c.AgentRun.ID, Attempt: c.AgentRun.Attempt}))
	must(t, err)
	if fault == "mcp-symlink" {
		if !strings.Contains(r.Error, "MCP config is not a regular file") {
			t.Errorf("wrong refusal: %s", r.Error)
		}
		if _, err := os.Stat(filepath.Join(filepath.Dir(bin), "launches")); !os.IsNotExist(err) {
			t.Error("child launched despite root MCP symlink")
		}
		if _, err := os.Lstat(filepath.Join(scope, ".lyk-mcp.json")); err != nil {
			t.Error("refused path removed")
		}
		if b, err := os.ReadFile(refused); err != nil || string(b) != "preserved discovery" {
			t.Error("discovery data changed")
		}
	}
	if fault != "" {
		wantCalls := 1
		if fault == "second-discovery" {
			wantCalls = 2
		}
		if calls != wantCalls || r.Kind != "fail" {
			t.Fatalf("fault %s calls=%d report=%+v", fault, calls, r)
		}
		return
	}
	launches, err := os.ReadFile(filepath.Join(filepath.Dir(bin), "launches"))
	must(t, err)
	if len(strings.Split(strings.TrimSpace(string(launches)), "\n")) != 1 {
		t.Fatalf("child launches: %q", launches)
	}
	if calls != 2 || r.Kind != "complete" || r.Scratch != "" || r.Mode != ModePrivate || r.PolicyDigest == "" || r.RetainedScratch != privateScratch(c) {
		t.Fatalf("calls=%d report=%+v", calls, r)
	}
	for _, dir := range []string{scope, filepath.Join(home, scratchName(c.AgentRun.ID, c.AgentRun.Attempt))} {
		if _, err := os.Stat(filepath.Join(dir, ".finished")); err != nil {
			t.Error(err)
		}
	}
	if _, err := os.Stat(filepath.Join(scope, ".cli-home", ".credentials.json")); !os.IsNotExist(err) {
		t.Error("refused scope credential retained")
	}
	b, err := os.ReadFile(refused)
	must(t, err)
	if string(b) != "preserved discovery" {
		t.Fatal("discovery changed")
	}
}

func TestFallbackRecoveryFinalizesBothFolders(t *testing.T) {
	state, home := t.TempDir(), t.TempDir()
	r := reportFixture()
	r.Kind = "fail"
	// Decode the local journal format so the regression also runs before its fields exist.
	must(t, json.Unmarshal([]byte(`{"retained_scratch":"scope-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","retained_retention_days":7}`), &r))
	d := &daemon{cfg: Config{StateDir: state, Home: home}}
	d.messages = &Outbox{Dir: filepath.Join(state, "messages"), Send: func(context.Context, Report) error { return nil }}
	d.out = &Outbox{Dir: filepath.Join(state, "outbox"), Send: func(context.Context, Report) error { return errors.New("offline") }}
	dirs := []string{filepath.Join(home, scratchName(r.AgentRunID, r.Attempt)), filepath.Join(home, "scope-"+strings.Repeat("a", 64))}
	for _, dir := range dirs {
		must(t, os.MkdirAll(filepath.Join(dir, ".cli-home"), 0700))
		must(t, os.WriteFile(filepath.Join(dir, ".cli-home", "auth.json"), []byte("secret"), 0600))
	}
	must(t, d.journal().Put(r))
	must(t, d.recoverAttempts(t.Context()))
	for i, dir := range dirs {
		if _, err := os.Stat(filepath.Join(dir, ".cli-home", "auth.json")); !os.IsNotExist(err) {
			t.Errorf("credential retained: %s", dir)
		}
		b, err := os.ReadFile(filepath.Join(dir, ".finished"))
		must(t, err)
		var m struct {
			RetentionDays int `json:"retention_days"`
		}
		must(t, json.Unmarshal(b, &m))
		if (i == 0 && m.RetentionDays != 0) || (i == 1 && m.RetentionDays != 7) {
			t.Errorf("retention %d: %s", i, b)
		}
	}
}

func TestFallbackRecoveryRejectsUnownedNamesBeforeCleanup(t *testing.T) {
	for _, name := range []string{"..", "../outside", "unrelated", "scope-short", "scope-" + strings.Repeat("z", 64)} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			r := reportFixture()
			r.RetainedScratch = name
			d := &daemon{cfg: Config{Home: home}}
			actual := filepath.Join(home, scratchName(r.AgentRunID, r.Attempt))
			must(t, os.MkdirAll(filepath.Join(actual, ".cli-home"), 0700))
			credential := filepath.Join(actual, ".cli-home", "auth.json")
			must(t, os.WriteFile(credential, []byte("preserve"), 0600))
			if err := d.recoverAttempt(t.Context(), r, false); err == nil {
				t.Fatal("accepted invalid retained name")
			}
			if _, err := os.Stat(credential); err != nil {
				t.Fatal("cleanup began before all names validated")
			}
		})
	}
}
