package agent

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func usageFrame(session string, models map[string]any) string {
	return string(raw(map[string]any{"type": "result", "is_error": false, "session_id": session, "modelUsage": models}))
}
func modelUsage(input, output, read, write int64, cost any) map[string]any {
	m := map[string]any{"inputTokens": input, "outputTokens": output, "cacheReadInputTokens": read, "cacheCreationInputTokens": write}
	if cost != nil {
		m["costUSD"] = cost
	}
	return m
}
func runClaudeUsage(t *testing.T, dir, resume, frame string) Result {
	if resume != "" {
		seedClaudeTranscript(t, resume)
	}
	t.Helper()
	s, err := (Claude{Bin: script(t, "echo '"+frame+"'")}).Execute(context.Background(), "p", ExecOptions{Sandbox: testSandbox, Dir: dir, Resume: seedClaudeTranscript(t, resume)})
	if err != nil {
		t.Fatal(err)
	}
	_, r := collect(t, s)
	return r
}
func usageByModel(t *testing.T, r Result, model string) Usage {
	t.Helper()
	if r.Err != nil {
		t.Fatal(r.Err)
	}
	for _, u := range r.Usage {
		if u.Model == model {
			return u
		}
	}
	t.Fatalf("model %s missing from %+v", model, r.Usage)
	return Usage{}
}
func TestClaudeResumeSubtractsAuthoritativeSnapshot(t *testing.T) {
	dir := t.TempDir()
	session := "session-cumulative"
	first := runClaudeUsage(t, dir, "", usageFrame(session, map[string]any{"main": modelUsage(100, 20, 300, 40, 1.0)}))
	if u := usageByModel(t, first, "main"); u.InputTokens != 100 || u.CostUSD == nil || *u.CostUSD != 1 {
		t.Fatalf("first usage %+v", u)
	}
	second := runClaudeUsage(t, dir, session, usageFrame(session, map[string]any{"main": modelUsage(115, 27, 360, 42, 1.25), "other": modelUsage(3, 5, 11, 2, 0.1)}))
	u := usageByModel(t, second, "main")
	if u.InputTokens != 15 || u.OutputTokens != 7 || u.CacheReadTokens != 60 || u.CacheWriteTokens != 2 || u.CostUSD == nil || *u.CostUSD != 0.25 {
		t.Fatalf("historical usage charged: %+v", u)
	}
	other := usageByModel(t, second, "other")
	if other.InputTokens != 3 || other.CostUSD == nil || *other.CostUSD != 0.1 {
		t.Fatalf("new model usage %+v", other)
	}
	// The next baseline must store 115, not the emitted delta of 15.
	third := runClaudeUsage(t, dir, session, usageFrame(session, map[string]any{"main": modelUsage(120, 30, 370, 44, 1.5), "other": modelUsage(3, 5, 11, 2, 0.1)}))
	u = usageByModel(t, third, "main")
	if u.InputTokens != 5 || u.OutputTokens != 3 || u.CacheReadTokens != 10 || u.CacheWriteTokens != 2 || u.CostUSD == nil || *u.CostUSD != 0.25 {
		t.Fatalf("delta was saved as baseline: %+v", u)
	}
}
func TestClaudeResumeCounterResetUsesWholeCurrentSnapshot(t *testing.T) {
	for _, tc := range []struct {
		name                                 string
		current                              map[string]any
		input, output, cacheRead, cacheWrite int64
		cost                                 float64
	}{
		{"all_reset", map[string]any{"main": modelUsage(15, 7, 60, 2, 0.25)}, 15, 7, 60, 2, 0.25},
		{"one_counter_reset", map[string]any{"main": modelUsage(115, 27, 1, 42, 0.25)}, 115, 27, 1, 42, 0.25},
		{"prior_model_absent", map[string]any{"new": modelUsage(120, 30, 400, 50, 0.5)}, 120, 30, 400, 50, 0.5},
		{"cost_stays_cumulative", map[string]any{"main": modelUsage(15, 7, 60, 2, 1.25)}, 15, 7, 60, 2, 0.25},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			first := runClaudeUsage(t, dir, "", usageFrame("s", map[string]any{"main": modelUsage(100, 20, 300, 40, 1.0)}))
			if first.Err != nil {
				t.Fatal(first.Err)
			}
			r := runClaudeUsage(t, dir, "s", usageFrame("s", tc.current))
			model := "main"
			if tc.name == "prior_model_absent" {
				model = "new"
			}
			u := usageByModel(t, r, model)
			if u.InputTokens != tc.input || u.OutputTokens != tc.output || u.CacheReadTokens != tc.cacheRead || u.CacheWriteTokens != tc.cacheWrite || u.CostUSD == nil || math.Abs(*u.CostUSD-tc.cost) > 1e-9 {
				t.Fatalf("reset usage %+v", u)
			}
		})
	}
}
func TestClaudeResumeRefusesUnknownOrWrongBaselineBeforeLaunch(t *testing.T) {
	for _, tc := range []string{"missing", "session_mismatch", "cost_only", "corrupt", "oversized"} {
		t.Run(tc, func(t *testing.T) {
			dir := t.TempDir()
			if tc != "missing" {
				frame := usageFrame("known", map[string]any{"main": modelUsage(100, 20, 300, 40, 1.0)})
				if tc == "cost_only" {
					frame = `{"type":"result","session_id":"known","total_cost_usd":1}`
				}
				r := runClaudeUsage(t, dir, "", frame)
				if r.Err != nil {
					t.Fatal(r.Err)
				}
			}
			if tc == "corrupt" || tc == "oversized" {
				data := []byte("broken")
				if tc == "oversized" {
					data = []byte(strings.Repeat("x", (1<<20)+1))
				}
				if err := os.WriteFile(filepath.Join(dir, ".cli-home", ".lyk-usage.json"), data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			started := filepath.Join(t.TempDir(), "started")
			resume := "known"
			if tc == "session_mismatch" {
				resume = "different"
			}
			_, err := (Claude{Bin: script(t, "touch '"+started+"'")}).Execute(context.Background(), "p", ExecOptions{Sandbox: testSandbox, Dir: dir, Resume: seedClaudeTranscript(t, resume)})
			if err == nil || !strings.Contains(err.Error(), "baseline") {
				t.Fatalf("unsafe resume accepted: %v", err)
			}
			if _, err := os.Stat(started); !os.IsNotExist(err) {
				t.Fatal("unsafe resume reached CLI")
			}
		})
	}
}
func TestClaudeResumeCostsAndMissingCostBaseline(t *testing.T) {
	for _, tc := range []struct {
		name          string
		base, current any
		wantCost      *float64
		wantError     bool
	}{
		{"both_unpriced", nil, nil, nil, false},
		{"current_unpriced", 1.0, nil, nil, false},
		{"baseline_unpriced", nil, 1.25, nil, true},
		{"cost_reset", 1.0, 0.25, ptrCost(0.25), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			r := runClaudeUsage(t, dir, "", usageFrame("s", map[string]any{"main": modelUsage(100, 20, 300, 40, tc.base)}))
			if r.Err != nil {
				t.Fatal(r.Err)
			}
			r = runClaudeUsage(t, dir, "s", usageFrame("s", map[string]any{"main": modelUsage(115, 27, 360, 42, tc.current)}))
			if tc.wantError {
				if r.Err == nil || len(r.Usage) != 0 {
					t.Fatalf("unknown historical cost was reported: %+v", r)
				}
				return
			}
			u := usageByModel(t, r, "main")
			if u.InputTokens != 15 || (u.CostUSD == nil) != (tc.wantCost == nil) {
				t.Fatalf("usage %+v", u)
			}
			if tc.wantCost != nil && *u.CostUSD != *tc.wantCost {
				t.Fatalf("cost %v", *u.CostUSD)
			}
		})
	}
}
func ptrCost(v float64) *float64 { return &v }
func TestClaudeResumeInvalidatesBaselineOnInterruptedAttempt(t *testing.T) {
	for _, body := range []string{"exit 1", `echo '{"type":"result","session_id":"s","is_error":true,"result":"failed","modelUsage":{"main":{"inputTokens":115}}}'`} {
		dir := t.TempDir()
		r := runClaudeUsage(t, dir, "", usageFrame("s", map[string]any{"main": modelUsage(100, 20, 300, 40, nil)}))
		if r.Err != nil {
			t.Fatal(r.Err)
		}
		s, err := (Claude{Bin: script(t, body)}).Execute(context.Background(), "p", ExecOptions{Sandbox: testSandbox, Dir: dir, Resume: seedClaudeTranscript(t, "s")})
		if err != nil {
			t.Fatal(err)
		}
		_, r = collect(t, s)
		if r.Err == nil {
			t.Fatal("expected failed resumed attempt")
		}
		if _, err := (Claude{Bin: script(t, "exit 0")}).Execute(context.Background(), "p", ExecOptions{Sandbox: testSandbox, Dir: dir, Resume: seedClaudeTranscript(t, "s")}); err == nil {
			t.Fatal("failed run reused stale baseline")
		}
	}
}
func TestClaudeResumeRejectsAmbiguousTerminalUsage(t *testing.T) {
	for _, frame := range []string{
		`{"type":"result","session_id":"s","total_cost_usd":1.25,"usage":{"input_tokens":15}}`,
		usageFrame("wrong", map[string]any{"main": modelUsage(115, 27, 360, 42, 1.25)}),
		usageFrame("s", map[string]any{"main": modelUsage(-1, 27, 360, 42, 1.25)}),
	} {
		dir := t.TempDir()
		r := runClaudeUsage(t, dir, "", usageFrame("s", map[string]any{"main": modelUsage(100, 20, 300, 40, 1.0)}))
		if r.Err != nil {
			t.Fatal(r.Err)
		}
		r = runClaudeUsage(t, dir, "s", frame)
		if r.Err == nil || len(r.Usage) != 0 {
			t.Fatalf("ambiguous resumed spend accepted: %+v", r)
		}
	}
}
func TestClaudeBaselinePersistsPrivateAuthoritativeData(t *testing.T) {
	dir := t.TempDir()
	r := runClaudeUsage(t, dir, "", usageFrame("s", map[string]any{"main": modelUsage(100, 20, 300, 40, 1.0)}))
	if r.Err != nil {
		t.Fatal(r.Err)
	}
	path := filepath.Join(dir, ".cli-home", ".lyk-usage.json")
	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != 0600 {
		t.Fatalf("baseline permissions: %v %v", st, err)
	}
	data, err := os.ReadFile(path)
	if err != nil || !json.Valid(data) {
		t.Fatalf("invalid persisted snapshot: %v", err)
	}
}
func TestScratchRemainsLockedUntilUsageParsed(t *testing.T) {
	dir := t.TempDir()
	r := runClaudeUsage(t, dir, "", usageFrame("s", map[string]any{"main": modelUsage(100, 20, 300, 40, 1.0)}))
	if r.Err != nil {
		t.Fatal(r.Err)
	}
	exited := filepath.Join(t.TempDir(), "exit")
	body := `i=0; while [ "$i" -lt 100 ]; do echo '{"type":"log"}'; i=$((i+1)); done
echo '` + usageFrame("s", map[string]any{"main": modelUsage(115, 27, 360, 42, 1.25)}) + `'
touch '` + exited + `'`
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s, err := (Claude{Bin: script(t, body)}).Execute(ctx, "p", ExecOptions{Sandbox: testSandbox, Dir: dir, Resume: seedClaudeTranscript(t, "s")})
	if err != nil {
		t.Fatal(err)
	}
	waitFile(t, exited)
	time.Sleep(100 * time.Millisecond)
	if _, err := (Claude{Bin: script(t, "exit 0")}).Execute(context.Background(), "p", ExecOptions{Sandbox: testSandbox, Dir: dir}); err == nil {
		t.Fatal("scratch released before pending usage normalization")
	}
	_, r = collect(t, s)
	if u := usageByModel(t, r, "main"); u.InputTokens != 15 {
		t.Fatalf("usage %+v", u)
	}
}

func TestClaudeUsageRejectsInvalidNumericBaselines(t *testing.T) {
	for _, bad := range []string{
		`{"version":1,"session":"s","models":{"main":{"inputTokens":-1}}}`,
		`{"version":1,"session":"s","models":{"main":{"inputTokens":9223372036854775808}}}`,
		`{"version":1,"session":"s","models":{"main":{"costUSD":-0.5}}}`,
		`{"version":1,"session":"s","models":{"main":{"costUSD":1e400}}}`,
		`{"version":1,"session":"s","models":{"main":{"costUSD":NaN}}}`,
	} {
		dir := t.TempDir()
		r := runClaudeUsage(t, dir, "", usageFrame("s", map[string]any{"main": modelUsage(100, 20, 300, 40, 1.0)}))
		if r.Err != nil {
			t.Fatal(r.Err)
		}
		if err := os.WriteFile(filepath.Join(dir, ".cli-home", ".lyk-usage.json"), []byte(bad), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := (Claude{Bin: script(t, "exit 0")}).Execute(context.Background(), "p", ExecOptions{Sandbox: testSandbox, Dir: dir, Resume: seedClaudeTranscript(t, "s")}); err == nil {
			t.Fatalf("invalid baseline accepted: %s", bad)
		}
	}
}
func TestClaudeResumeCancellationConsumesBaseline(t *testing.T) {
	dir := t.TempDir()
	r := runClaudeUsage(t, dir, "", usageFrame("s", map[string]any{"main": modelUsage(100, 20, 300, 40, 1.0)}))
	if r.Err != nil {
		t.Fatal(r.Err)
	}
	ready := filepath.Join(t.TempDir(), "ready")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, err := (Claude{Bin: script(t, "touch '"+ready+"'\nsleep 300")}).Execute(ctx, "p", ExecOptions{Sandbox: testSandbox, Dir: dir, Resume: seedClaudeTranscript(t, "s")})
	if err != nil {
		t.Fatal(err)
	}
	waitFile(t, ready)
	cancel()
	_, r = collect(t, s)
	if r.Reason != ReasonRuntimeOffline {
		t.Fatalf("%+v", r)
	}
	if _, err := (Claude{Bin: script(t, "exit 0")}).Execute(context.Background(), "p", ExecOptions{Sandbox: testSandbox, Dir: dir, Resume: seedClaudeTranscript(t, "s")}); err == nil {
		t.Fatal("cancelled execution left reusable stale baseline")
	}
}

func TestClaudeResumeTerminalFailureSurvivesMissingUsage(t *testing.T) {
	for _, tc := range []struct{ name, frame, reason, detail string }{
		{"exhausted_without_error_flag", `{"type":"result","session_id":"s","terminal_reason":"prompt_too_long","is_error":false}`, ReasonContextExhausted, "prompt_too_long"},
		{"exhausted_with_error_flag", `{"type":"result","session_id":"s","terminal_reason":"prompt_too_long","is_error":true,"result":"stopped","total_cost_usd":1.25}`, ReasonContextExhausted, "prompt_too_long"},
		{"provider_failure", `{"type":"result","session_id":"s","is_error":true,"result":"rate limited"}`, ReasonProviderNetwork, "rate limited"},
		{"subtype_failure", `{"type":"result","session_id":"s","subtype":"error_during_execution","errors":["connection reset"]}`, ReasonProviderNetwork, "connection reset"},
		{"agent_failure", `{"type":"result","session_id":"s","is_error":true,"result":"max turns reached"}`, ReasonAgentError, "max turns reached"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			first := runClaudeUsage(t, dir, "", usageFrame("s", map[string]any{"main": modelUsage(100, 20, 300, 40, 1.0)}))
			if first.Err != nil {
				t.Fatal(first.Err)
			}
			r := runClaudeUsage(t, dir, "s", tc.frame)
			if r.Err == nil || r.Reason != tc.reason || !strings.Contains(r.Err.Error(), tc.detail) || r.SessionID != "s" {
				t.Fatalf("terminal reason lost: %+v", r)
			}
			if len(r.Usage) != 0 {
				t.Fatalf("ambiguous resumed usage retained: %+v", r.Usage)
			}
			if _, err := (Claude{Bin: script(t, "exit 0")}).Execute(context.Background(), "p", ExecOptions{Sandbox: testSandbox, Dir: dir, Resume: seedClaudeTranscript(t, "s")}); err == nil {
				t.Fatal("failed terminal result restored unsafe baseline")
			}
		})
	}
}
