package daemon

import (
	"errors"
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
	if err != nil || pol.Mode != ModeContinuity || pol.Explicit || pol.RetentionDays != defaultRetention {
		t.Fatalf("%+v %v", pol, err)
	}
	if err := SavePolicy(dir, ws, Policy{Mode: ModePrivate}); err != nil {
		t.Fatal(err)
	}
	pol, err = LoadPolicy(dir, ws)
	if err != nil || pol.Mode != ModePrivate || !pol.Explicit || pol.Digest == defaultPolicyHash {
		t.Fatalf("%+v %v", pol, err)
	}
	p := filepath.Join(profileDir(dir, ws), "policy.json")
	for _, bad := range []string{`{"version":1,"mode":"private","extra":true}`, `{"version":2,"mode":"private"}`, `{"version":1,"mode":"inherit_all"}`, `{"version":1,"mode":"private","session_retention_days":-1}`, `not json`} {
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
	if pol, err := LoadPolicy(dir, ws); err != nil || pol.Mode != ModeContinuity {
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

func TestPrivateModeHasNoContinuity(t *testing.T) {
	d := registryDaemon(t)
	c := claimFor(uuid.New(), uuid.New(), uuid.New(), "analysis")
	SavePolicy(d.cfg.StateDir, c.AgentRun.WorkspaceID, Policy{Mode: ModePrivate})
	if cont := begin(t, d, c); cont != nil {
		t.Fatalf("private workspace got %+v", cont)
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

func TestEmptyScopeInheritsButNeverResumes(t *testing.T) {
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

func TestPrivateIsForcedByTheRunOrAnUnknownHarness(t *testing.T) {
	d := registryDaemon(t)
	ws := uuid.New()
	SavePolicy(d.cfg.StateDir, ws, Policy{Mode: ModeContinuity})
	private := claimFor(ws, uuid.New(), uuid.New(), "objector")
	private.AgentRun.RunPrivate = true
	cursor := claimFor(ws, uuid.New(), uuid.New(), "analysis")
	cursor.AgentRun.Harness = "cursor"
	for name, c := range map[string]claimed{"run_private": private, "cursor": cursor} {
		pol, _ := LoadPolicy(d.cfg.StateDir, ws)
		if mode := runMode(c, pol); mode != ModePrivate {
			t.Errorf("%s ran %s", name, mode)
		}
		if cont := begin(t, d, c); cont != nil {
			t.Errorf("%s got continuity %+v", name, cont)
		}
	}
}

func TestForgetScopeDeletesOnlyThatRecord(t *testing.T) {
	d := registryDaemon(t)
	ws, user, kept, gone := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	d.finishContinuity(begin(t, d, claimFor(ws, kept, user, "analysis")), cleanClaude)
	d.finishContinuity(begin(t, d, claimFor(ws, gone, user, "analysis")), cleanClaude)
	if err := ForgetScope(d.cfg.StateDir, ws, user, "claude", boardScope(gone, "analysis")); err != nil {
		t.Fatal(err)
	}
	if err := ForgetScope(d.cfg.StateDir, ws, user, "claude", boardScope(gone, "analysis")); err != nil {
		t.Fatal("forgetting twice failed:", err)
	}
	if cont := begin(t, d, claimFor(ws, gone, user, "analysis")); cont.resume != "" {
		t.Fatal("forgotten scope resumed")
	}
	if cont := begin(t, d, claimFor(ws, kept, user, "analysis")); cont.resume != "s1" {
		t.Fatal("another scope was forgotten")
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
	SavePolicy(d.cfg.StateDir, ws, Policy{Mode: ModeContinuity, RetentionDays: 7})
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
	SavePolicy(d.cfg.StateDir, ws, Policy{Mode: ModeContinuity, RetentionDays: 1})
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
