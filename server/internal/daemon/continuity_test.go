package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"lykeion/server/internal/apiclient"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"lykeion/server/pkg/agent"
)

func registryDaemon(t *testing.T) *daemon {
	return &daemon{cfg: Config{StateDir: t.TempDir()}, scopes: map[string]bool{}}
}

func boardScope(card uuid.UUID, role string) string { return "card:" + card.String() + "/role:" + role }

func claimFor(ws, card, user uuid.UUID, role string) claimed {
	var c claimed
	c.AgentRun.ID, c.AgentRun.Attempt = uuid.New(), 1
	c.AgentRun.WorkspaceID, c.AgentRun.ResponsibleUserID = ws, user
	c.AgentRun.ContinuityScope, c.AgentRun.Harness, c.AgentRun.Skill = boardScope(card, role), "claude", "skill v1"
	return c
}

func begin(t *testing.T, d *daemon, c claimed) *continuity {
	t.Helper()
	pol, err := LoadPolicy(d.cfg.StateDir, c.AgentRun.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	cont, err := d.beginContinuity(c, pol)
	if err != nil {
		t.Fatal(err)
	}
	return cont
}

var cleanClaude = agent.Result{SessionID: "s1", ClaudeSnapshot: []byte(`{"version":1}`)}

func TestPolicyDefaultsToContinuityAndRejectsInvalid(t *testing.T) {
	dir, ws := t.TempDir(), uuid.New()
	pol, err := LoadPolicy(dir, ws)
	if err != nil || pol.Explicit || pol.RetentionDays != defaultRetention {
		t.Fatalf("%+v %v", pol, err)
	}
	if err := SavePolicy(dir, ws, Policy{}); err != nil {
		t.Fatal(err)
	}
	pol, err = LoadPolicy(dir, ws)
	if err != nil || !pol.Explicit || pol.Digest == defaultPolicyHash {
		t.Fatalf("%+v %v", pol, err)
	}
	p := filepath.Join(profileDir(dir, ws), "policy.json")
	for _, bad := range []string{`{"version":1,"mode":"private","extra":true}`, `{"version":2,"mode":"private"}`, `{"version":1,"mode":"private","session_retention_days":-1}`, `not json`} {
		os.WriteFile(p, []byte(bad), 0o600)
		if _, err := LoadPolicy(dir, ws); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
	// An explicit invalid policy fails the run instead of falling back.
	d := &daemon{cfg: Config{StateDir: dir}, scopes: map[string]bool{}}
	if _, err := LoadPolicy(d.cfg.StateDir, ws); err == nil {
		t.Fatal("invalid policy loaded")
	}
	if err := ResetProfile(dir, ws); err != nil {
		t.Fatal(err)
	}
	if pol, err := LoadPolicy(dir, ws); err != nil || pol.RetentionDays != defaultRetention {
		t.Fatalf("reset left %+v %v", pol, err)
	}
}

func TestPolicyRefusesPlantedLinks(t *testing.T) {
	dir, ws := t.TempDir(), uuid.New()
	os.MkdirAll(filepath.Join(dir, "workspace-profiles"), 0o700)
	if err := os.Symlink(t.TempDir(), profileDir(dir, ws)); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPolicy(dir, ws); err == nil {
		t.Fatal("followed a linked profile directory")
	}
	if err := ResetProfile(dir, ws); err == nil {
		t.Fatal("reset through a linked profile directory")
	}
	other := uuid.New()
	os.MkdirAll(profileDir(dir, other), 0o700)
	target := filepath.Join(t.TempDir(), "p.json")
	os.WriteFile(target, []byte(`{"version":1,"mode":"private"}`), 0o600)
	os.Symlink(target, filepath.Join(profileDir(dir, other), "policy.json"))
	if _, err := LoadPolicy(dir, other); err == nil {
		t.Fatal("followed a linked policy file")
	}
}

func TestLegacyPrivatePolicyLoads(t *testing.T) {
	dir, ws := t.TempDir(), uuid.New()
	must(t, os.MkdirAll(profileDir(dir, ws), 0700))
	must(t, os.WriteFile(filepath.Join(profileDir(dir, ws), "policy.json"), []byte(`{"version":1,"mode":"private","session_retention_days":7}`), 0600))
	pol, err := LoadPolicy(dir, ws)
	if err != nil || pol.RetentionDays != 7 {
		t.Fatalf("%+v %v", pol, err)
	}
	first := pol.Digest
	must(t, os.WriteFile(filepath.Join(profileDir(dir, ws), "policy.json"), []byte(`{"session_retention_days":7,"version":1,"mode":"workspace_continuity"}`), 0600))
	pol, err = LoadPolicy(dir, ws)
	if err != nil || pol.Digest != first {
		t.Fatalf("legacy mode changed retention digest: %+v %v", pol, err)
	}
}

func TestLegacyPrivateClaimResumesScope(t *testing.T) {
	d := registryDaemon(t)
	c := claimFor(uuid.New(), uuid.New(), uuid.New(), "analysis")
	must(t, d.finishContinuity(begin(t, d, c), cleanClaude))
	raw, err := json.Marshal(c)
	must(t, err)
	var legacy map[string]json.RawMessage
	must(t, json.Unmarshal(raw, &legacy))
	var run map[string]json.RawMessage
	must(t, json.Unmarshal(legacy["agent_run"], &run))
	run["run_private"] = json.RawMessage(`true`)
	legacy["agent_run"], err = json.Marshal(run)
	must(t, err)
	raw, err = json.Marshal(legacy)
	must(t, err)
	must(t, json.Unmarshal(raw, &c))
	cont := begin(t, d, c)
	if cont == nil || cont.resume != "s1" || len(cont.baseline) == 0 {
		t.Fatalf("legacy private claim did not resume: %+v", cont)
	}
}

func TestSameScopeResumesAndOthersStartFresh(t *testing.T) {
	d := registryDaemon(t)
	ws, card, user := uuid.New(), uuid.New(), uuid.New()
	c := claimFor(ws, card, user, "analysis")
	first := begin(t, d, c)
	if first.resume != "" || !first.owned {
		t.Fatalf("%+v", first)
	}
	if err := d.finishContinuity(first, cleanClaude); err != nil {
		t.Fatal(err)
	}
	again := begin(t, d, claimFor(ws, card, user, "analysis"))
	if again.resume != "s1" || string(again.baseline) != `{"version":1}` || again.note != "" {
		t.Fatalf("same scope did not resume: %+v", again)
	}
	d.finishContinuity(again, agent.Result{SessionID: "s1", ClaudeSnapshot: []byte(`{"version":1,"n":2}`)})
	for name, other := range map[string]claimed{
		"card":      claimFor(ws, uuid.New(), user, "analysis"),
		"user":      claimFor(ws, card, uuid.New(), "analysis"),
		"role":      claimFor(ws, card, user, "review"),
		"workspace": claimFor(uuid.New(), card, user, "analysis"),
	} {
		cont := begin(t, d, other)
		if cont.resume != "" || cont.baseline != nil {
			t.Errorf("cross-%s run resumed %q", name, cont.resume)
		}
		d.releaseScope(cont)
	}
	codex := claimFor(ws, card, user, "analysis")
	codex.AgentRun.Harness = "codex"
	if cont := begin(t, d, codex); cont.resume != "" {
		t.Errorf("another harness resumed %q", cont.resume)
	}
}

func TestContinuityIgnoresTheClaimedSessionID(t *testing.T) {
	d := registryDaemon(t)
	ws, card, user := uuid.New(), uuid.New(), uuid.New()
	unknown := claimFor(ws, card, user, "analysis")
	unknown.AgentRun.SessionID = "never-recorded"
	first := begin(t, d, unknown)
	if first.resume != "" {
		t.Fatalf("resumed the claim's session: %+v", first)
	}
	d.finishContinuity(first, cleanClaude)
	// Another scope's session id never redirects this scope's resume.
	mismatch := claimFor(ws, card, user, "analysis")
	mismatch.AgentRun.SessionID = "someone-elses"
	if cont := begin(t, d, mismatch); cont.resume != "s1" {
		t.Fatalf("%+v", cont)
	}
}

func TestEmptyScopeNeverResumes(t *testing.T) {
	d := registryDaemon(t)
	ws, user := uuid.New(), uuid.New()
	for i := 0; i < 2; i++ {
		c := claimFor(ws, uuid.New(), user, "analysis")
		c.AgentRun.ContinuityScope = ""
		cont := begin(t, d, c)
		if cont == nil || cont.owned || cont.resume != "" {
			t.Fatalf("empty scope: %+v", cont)
		}
		if err := d.finishContinuity(cont, cleanClaude); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(sessionsDir(d.cfg.StateDir, ws)); !os.IsNotExist(err) {
		t.Fatal("an empty scope recorded a session")
	}
}

func TestContextChangeStartsFresh(t *testing.T) {
	d := registryDaemon(t)
	ws, card, user := uuid.New(), uuid.New(), uuid.New()
	d.finishContinuity(begin(t, d, claimFor(ws, card, user, "analysis")), cleanClaude)
	changed := claimFor(ws, card, user, "analysis")
	changed.AgentRun.Skill = "skill v2"
	if cont := begin(t, d, changed); cont.resume != "" || !strings.Contains(cont.note, "context changed") {
		t.Fatalf("%+v", cont)
	}
	d.scopes = map[string]bool{}
	SavePolicy(d.cfg.StateDir, ws, Policy{RetentionDays: 7})
	if cont := begin(t, d, claimFor(ws, card, user, "analysis")); cont.resume != "" {
		t.Fatalf("a policy change kept the session: %+v", cont)
	}
}

func TestConcurrentSameScopeRunsDoNotShareASession(t *testing.T) {
	d := registryDaemon(t)
	ws, card, user := uuid.New(), uuid.New(), uuid.New()
	d.finishContinuity(begin(t, d, claimFor(ws, card, user, "analysis")), cleanClaude)
	holder := begin(t, d, claimFor(ws, card, user, "analysis"))
	second := begin(t, d, claimFor(ws, card, user, "analysis"))
	if holder.resume != "s1" || second.resume != "" || second.owned || second.note == "" {
		t.Fatalf("holder %+v second %+v", holder, second)
	}
	// The fresh side run leaves the holder's record alone.
	if err := d.finishContinuity(second, agent.Result{SessionID: "side", ClaudeSnapshot: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	d.finishContinuity(holder, agent.Result{SessionID: "s1", ClaudeSnapshot: []byte(`{"version":1}`)})
	if next := begin(t, d, claimFor(ws, card, user, "analysis")); next.resume != "s1" {
		t.Fatalf("%+v", next)
	}
}

func TestFailedOrUnaccountedRunIsNeverResumed(t *testing.T) {
	d := registryDaemon(t)
	ws, card, user := uuid.New(), uuid.New(), uuid.New()
	for _, res := range []agent.Result{
		{SessionID: "s1", Err: errors.New("provider failed")},
		{SessionID: "s1"},
	} {
		d.finishContinuity(begin(t, d, claimFor(ws, card, user, "analysis")), res)
		if cont := begin(t, d, claimFor(ws, card, user, "analysis")); cont.resume != "" || cont.note == "" {
			t.Fatalf("%+v resumed after %+v", cont, res)
		}
		d.scopes = map[string]bool{}
	}
}

func TestHardDeathLeavesTheBaselineUnknown(t *testing.T) {
	d := registryDaemon(t)
	ws, card, user := uuid.New(), uuid.New(), uuid.New()
	d.finishContinuity(begin(t, d, claimFor(ws, card, user, "analysis")), cleanClaude)
	// The daemon dies while an attempt holds the consumed record.
	if cont := begin(t, d, claimFor(ws, card, user, "analysis")); cont.resume != "s1" {
		t.Fatalf("%+v", cont)
	}
	restarted := &daemon{cfg: d.cfg, scopes: map[string]bool{}}
	if err := restarted.recoverSessions(); err != nil {
		t.Fatal(err)
	}
	if cont := begin(t, restarted, claimFor(ws, card, user, "analysis")); cont.resume != "" || !strings.Contains(cont.note, "baseline") {
		t.Fatalf("a dead attempt's baseline was reused: %+v", cont)
	}
}

func TestAbandonRestoresTheConsumedSession(t *testing.T) {
	d := registryDaemon(t)
	ws, card, user := uuid.New(), uuid.New(), uuid.New()
	d.finishContinuity(begin(t, d, claimFor(ws, card, user, "analysis")), cleanClaude)
	if err := d.abandonContinuity(begin(t, d, claimFor(ws, card, user, "analysis"))); err != nil {
		t.Fatal(err)
	}
	if cont := begin(t, d, claimFor(ws, card, user, "analysis")); cont.resume != "s1" {
		t.Fatalf("%+v", cont)
	}
}

func TestRegistryRetentionPrunesOldRecords(t *testing.T) {
	d := registryDaemon(t)
	ws := uuid.New()
	SavePolicy(d.cfg.StateDir, ws, Policy{RetentionDays: 1})
	old := begin(t, d, claimFor(ws, uuid.New(), uuid.New(), "analysis"))
	d.finishContinuity(old, cleanClaude)
	p := filepath.Join(old.dir, old.key+".json")
	rec, _ := readRecord(p)
	rec.UpdatedAt = time.Now().Add(-48 * time.Hour)
	atomicJSON(old.dir, old.key+".json", rec)
	if err := d.recoverSessions(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("expired session record kept")
	}
}

func TestInvalidScopeIsRefused(t *testing.T) {
	d := registryDaemon(t)
	traversal := claimFor(uuid.New(), uuid.New(), uuid.New(), "analysis")
	traversal.AgentRun.ContinuityScope = "card:../../x"
	upper := claimFor(uuid.New(), uuid.New(), uuid.New(), "analysis")
	upper.AgentRun.ContinuityScope = "Card:" + uuid.NewString()
	for _, c := range []claimed{
		claimFor(uuid.New(), uuid.New(), uuid.New(), "../escape"),
		claimFor(uuid.New(), uuid.New(), uuid.Nil, "analysis"),
		traversal, upper,
	} {
		pol, _ := LoadPolicy(d.cfg.StateDir, c.AgentRun.WorkspaceID)
		if _, err := d.beginContinuity(c, pol); err == nil {
			t.Errorf("accepted scope %+v", c.AgentRun)
		}
	}
}

// Forgetting a scope also deletes its private folder, never while a run holds
// it, and leaves other scopes' folders.
func TestEveryRunResumesItsScope(t *testing.T) {
	config := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", config)
	must(t, os.MkdirAll(filepath.Join(config, "projects", "test"), 0700))
	must(t, os.WriteFile(filepath.Join(config, "projects", "test", "s1.jsonl"), nil, 0600))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"running"}`))
	}))
	defer srv.Close()
	d := registryDaemon(t)
	d.cfg.Home = t.TempDir()
	d.cfg.Server = srv.URL
	d.api = &apiclient.Client{Server: srv.URL}
	d.out = &Outbox{Dir: filepath.Join(d.cfg.StateDir, "outbox")}
	d.messages = &Outbox{Dir: filepath.Join(d.cfg.StateDir, "messages")}
	c := claimFor(uuid.New(), uuid.New(), uuid.New(), "analysis")
	runtime := uuid.New()
	c.AgentRun.RuntimeID = &runtime
	must(t, d.finishContinuity(begin(t, d, c), cleanClaude))
	raw, err := json.Marshal(c)
	must(t, err)
	var legacy map[string]json.RawMessage
	must(t, json.Unmarshal(raw, &legacy))
	var run map[string]json.RawMessage
	must(t, json.Unmarshal(legacy["agent_run"], &run))
	run["run_private"] = json.RawMessage(`true`)
	legacy["agent_run"], err = json.Marshal(run)
	must(t, err)
	raw, err = json.Marshal(legacy)
	must(t, err)
	must(t, json.Unmarshal(raw, &c))
	launched, dir := false, ""
	d.cfg.NewBackend = func(string, string) (agent.Backend, error) {
		return backendFunc(func(_ context.Context, _ string, o agent.ExecOptions) (*agent.Session, error) {
			launched = true
			dir = o.Dir
			if o.Resume != "s1" || len(o.ClaudeBaseline) == 0 {
				t.Errorf("resume=%q baseline=%s", o.Resume, o.ClaudeBaseline)
			}
			messages := make(chan agent.Message)
			close(messages)
			done := make(chan agent.Result, 1)
			done <- cleanClaude
			close(done)
			return &agent.Session{Messages: messages, Done: done}, nil
		}), nil
	}
	d.ownerOf = map[uuid.UUID]uuid.UUID{*c.AgentRun.RuntimeID: c.AgentRun.ResponsibleUserID}
	must(t, d.runAgentRun(t.Context(), c))
	report, err := readReport(d.out.file(Report{AgentRunID: c.AgentRun.ID, Attempt: c.AgentRun.Attempt}))
	must(t, err)
	if !launched || report.Kind != "complete" || report.Mode != "sandboxed" {
		t.Fatalf("launched=%v report=%+v", launched, report)
	}
	if filepath.Base(dir) != scratchName(c.AgentRun.ID, c.AgentRun.Attempt) {
		t.Fatalf("ran in %s, not the attempt's own folder", dir)
	}
	entries, err := os.ReadDir(d.cfg.Home)
	must(t, err)
	if len(entries) != 0 {
		t.Fatalf("run folders left=%v", entries)
	}
}

// A rejected resume and a new process both return Session before the CLI has
// accepted work. Only the successful fresh attempt may acknowledge the input.
func TestResumedCommentWaitsForBackendAcceptance(t *testing.T) {
	for _, scenario := range []string{"fresh_launch_failure", "fresh_async_rejection", "fresh_success"} {
		t.Run(scenario, func(t *testing.T) {
			config := t.TempDir()
			t.Setenv("CLAUDE_CONFIG_DIR", config)
			must(t, os.MkdirAll(filepath.Join(config, "projects", "test"), 0700))
			must(t, os.WriteFile(filepath.Join(config, "projects", "test", "s1.jsonl"), nil, 0600))
			comment := uuid.New()
			acks := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case strings.HasSuffix(r.URL.Path, "/comments/launch"):
					json.NewEncoder(w).Encode(map[string]any{"prompt": "Reserved input.", "comment_ids": []uuid.UUID{comment}})
				case strings.HasSuffix(r.URL.Path, "/comments/ack"):
					acks++
					w.WriteHeader(204)
				default:
					w.Write([]byte(`{"status":"running"}`))
				}
			}))
			defer srv.Close()
			d := registryDaemon(t)
			d.cfg.Home, d.cfg.Server = t.TempDir(), srv.URL
			d.api = &apiclient.Client{Server: srv.URL}
			d.out = &Outbox{Dir: filepath.Join(d.cfg.StateDir, "outbox")}
			d.messages = &Outbox{Dir: filepath.Join(d.cfg.StateDir, "messages")}
			c := claimFor(uuid.New(), uuid.New(), uuid.New(), "agent")
			runtime := uuid.New()
			c.AgentRun.RuntimeID = &runtime
			c.HasCommentSnapshot = true
			must(t, d.finishContinuity(begin(t, d, c), cleanClaude))
			calls := 0
			d.cfg.NewBackend = func(string, string) (agent.Backend, error) {
				return backendFunc(func(_ context.Context, p string, o agent.ExecOptions) (*agent.Session, error) {
					calls++
					if p != "Reserved input." || acks != 0 {
						t.Errorf("before acceptance: prompt=%q acks=%d", p, acks)
					}
					if calls == 1 && o.Resume != "s1" || calls == 2 && o.Resume != "" {
						t.Errorf("call=%d resume=%q", calls, o.Resume)
					}
					if calls == 2 && scenario == "fresh_launch_failure" {
						return nil, errors.New("launch refused")
					}
					messages := make(chan agent.Message, 1)
					// Session initialization is not evidence that the prompt was accepted.
					messages <- agent.Message{Type: "status", Content: []byte(`{"type":"session.started"}`)}
					close(messages)
					done := make(chan agent.Result, 1)
					result := agent.Result{Err: errors.New("authentication rejected")}
					if calls == 1 {
						result.ResumeRefused = true
					} else if scenario == "fresh_success" {
						result = cleanClaude
					}
					done <- result
					close(done)
					return &agent.Session{Messages: messages, Done: done}, nil
				}), nil
			}
			d.ownerOf = map[uuid.UUID]uuid.UUID{runtime: c.AgentRun.ResponsibleUserID}
			must(t, d.runAgentRun(t.Context(), c))
			want := 0
			if scenario == "fresh_success" {
				want = 1
			}
			if calls != 2 || acks != want {
				t.Fatalf("calls=%d acks=%d want=%d", calls, acks, want)
			}
		})
	}
}

func SavePolicy(stateDir string, ws uuid.UUID, p Policy) error {
	p.Version = policyVersion
	if err := p.check(); err != nil {
		return err
	}
	dir := profileDir(stateDir, ws)
	if err := checkProfileDir(dir); err != nil {
		return err
	}
	if err := privateDir(filepath.Dir(dir)); err != nil {
		return err
	}
	return atomicJSON(dir, "policy.json", p)
}
