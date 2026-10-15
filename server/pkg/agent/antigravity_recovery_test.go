package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const agyID = "11111111-2222-3333-4444-555555555555"

func record(kind, source, status string, content any) string {
	b, _ := json.Marshal(map[string]any{"type": kind, "source": source, "status": status, "content": content})
	return string(b) + "\n"
}
func recoveryScript(t *testing.T, dir, transcript, body, extra string) string {
	t.Helper()
	source := filepath.Join(t.TempDir(), "transcript")
	if err := os.WriteFile(source, []byte(transcript), 0600); err != nil {
		t.Fatal(err)
	}
	app := filepath.Join(dir, ".cli-home", ".gemini", "antigravity-cli")
	target := filepath.Join(app, "brain", agyID, ".system_generated", "logs")
	bin, _ := agyScript(t, "conversation="+agyID+"\nCLI app data directory: "+app+"\n"+extra, "mkdir -p "+shellQuote(target)+"\ncp "+shellQuote(source)+" "+shellQuote(filepath.Join(target, "transcript.jsonl"))+"\n"+body)
	return bin
}
func TestAntigravityTranscriptRecovery(t *testing.T) {
	for _, resume := range []string{"", agyID} {
		t.Run("resume-"+resume, func(t *testing.T) {
			dir, _ := filepath.EvalSymlinks(t.TempDir())
			s := record("USER_INPUT", "USER", "DONE", "old") + record("PLANNER_RESPONSE", "MODEL", "DONE", "old answer") + record("USER_INPUT", "USER", "DONE", "new") + "bad json\n" + record("PLANNER_RESPONSE", "MODEL", "ACTIVE", "partial") + record("TOOL", "MODEL", "DONE", "tool") + record("PLANNER_RESPONSE", "TOOL", "DONE", "tool") + record("PLANNER_RESPONSE", "MODEL", "DONE", nil) + record("PLANNER_RESPONSE", "MODEL", "DONE", map[string]string{"text": "object"}) + record("PLANNER_RESPONSE", "MODEL", "DONE", " \t") + record("PLANNER_RESPONSE", "MODEL", "DONE", "first") + record("PLANNER_RESPONSE", "MODEL", "DONE", "second")
			ms, res := agy(t, recoveryScript(t, dir, s, "exit 0", ""), ExecOptions{Dir: dir, Resume: resume})
			if res.Err != nil || res.SessionID != agyID || len(ms) != 1 || ms[0].Type != "text" || string(ms[0].Content) != `"first\n\nsecond"` {
				t.Fatalf("%+v %+v", ms, res)
			}
		})
	}
}
func TestAntigravityRecoveryOutputAndFailures(t *testing.T) {
	for _, c := range []struct {
		name, body, log, want string
		fail                  bool
	}{
		{"event only", `echo '{"event":"init","conversation_id":"` + agyID + `"}'`, "", "recovered", false},
		{"ordinary", `echo '{"event":"result","result":{"status":"SUCCESS","response":"ordinary"}}'`, "", "ordinary", false},
		{"whitespace", `echo '{"event":"step_update","step_update":{"step_type":"agent_response","text_delta":"  "}}'`, "", "recovered", false},
		{"nonzero", "exit 7", "", "agent exit", true},
		{"structured", `echo '{"event":"result","result":{"status":"ERROR","error":"bad provider"}}'`, "", "bad provider", true},
		{"provider", "exit 0", "agent executor error: bad provider\n", "bad provider", true},
		{"timeout", "exit 0", "Print mode: timed out after 100 polls\n", "print timeout", true},
		{"stream", `head -c 16777217 /dev/zero | tr '\000' x`, "", "agent output", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir, _ := filepath.EvalSymlinks(t.TempDir())
			ms, res := agy(t, recoveryScript(t, dir, record("PLANNER_RESPONSE", "MODEL", "DONE", "recovered"), c.body, c.log), ExecOptions{Dir: dir})
			var text []string
			for _, m := range ms {
				if m.Type == "text" {
					var s string
					json.Unmarshal(m.Content, &s)
					if strings.TrimSpace(s) != "" {
						text = append(text, s)
					}
				}
			}
			if c.fail {
				if res.Err == nil || !strings.Contains(res.Err.Error(), c.want) || strings.Join(text, ",") == "recovered" {
					t.Fatalf("%+v %+v", ms, res)
				}
			} else if res.Err != nil || strings.Join(text, ",") != c.want {
				t.Fatalf("%+v %+v", ms, res)
			}
		})
	}
}
func TestAntigravityRecoveryMissingInvalidEmpty(t *testing.T) {
	for _, s := range []string{"", "bad\n", record("USER_INPUT", "USER", "DONE", "new")} {
		dir, _ := filepath.EvalSymlinks(t.TempDir())
		_, res := agy(t, recoveryScript(t, dir, s, "exit 0", ""), ExecOptions{Dir: dir})
		if res.Err == nil || !strings.Contains(res.Err.Error(), "no output") {
			t.Fatalf("%+v", res)
		}
	}
}
func TestAntigravityRecoverySinkAndCancel(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(map[bool]string{true: "cancel", false: "sink"}[cancelled], func(t *testing.T) {
			dir, _ := filepath.EvalSymlinks(t.TempDir())
			body := "exit 0"
			if cancelled {
				body = `echo '{"event":"init","conversation_id":"` + agyID + `"}'` + "\nsleep 30"
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			sent := errors.New("sink refused")
			var seen []Message
			s, err := (Antigravity{Bin: recoveryScript(t, dir, record("PLANNER_RESPONSE", "MODEL", "DONE", "recovered"), body, "")}).Execute(ctx, "p", ExecOptions{Dir: dir, OnMessage: func(m Message) error {
				seen = append(seen, m)
				if cancelled {
					cancel()
					return nil
				}
				return sent
			}})
			if err != nil {
				t.Fatal(err)
			}
			ms, res := collectWithin(t, s, 10*time.Second)
			if len(ms) != 0 || res.Err == nil {
				t.Fatalf("%+v %+v", ms, res)
			}
			if cancelled {
				if !errors.Is(res.Err, context.Canceled) {
					t.Fatalf("%+v", res)
				}
			} else if !errors.Is(res.Err, sent) || len(seen) != 1 || string(seen[0].Content) != `"recovered"` {
				t.Fatalf("%+v %+v", seen, res)
			}
		})
	}
}

func TestAntigravityRecoveryPrivatePaths(t *testing.T) {
	for _, kind := range []string{"escape", "root symlink", "parent symlink", "file symlink", "directory", "bad session", "missing", "first app match"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			home := filepath.Join(root, ".cli-home")
			outside := t.TempDir()
			app := filepath.Join(home, "app")
			id := agyID
			if err := os.MkdirAll(app, 0700); err != nil {
				t.Fatal(err)
			}
			if kind == "escape" {
				app = outside
			}
			if kind == "root symlink" {
				os.RemoveAll(home)
				os.Symlink(outside, home)
			}
			if kind == "parent symlink" {
				os.RemoveAll(app)
				os.Symlink(outside, app)
			}
			target := filepath.Join(app, "brain", id, ".system_generated", "logs", "transcript.jsonl")
			if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(target, []byte(record("PLANNER_RESPONSE", "MODEL", "DONE", "secret")), 0600); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "file symlink":
				os.Remove(target)
				other := filepath.Join(outside, "file")
				os.WriteFile(other, []byte(record("PLANNER_RESPONSE", "MODEL", "DONE", "secret")), 0600)
				os.Symlink(other, target)
			case "directory":
				os.Remove(target)
				os.Mkdir(target, 0700)
			case "bad session":
				id = "../" + agyID
			case "missing":
				os.Remove(target)
			}
			log := "CLI app data directory: " + app + "\n"
			if kind == "first app match" {
				log = "CLI app data directory: " + outside + "\n" + log
			}
			if got := recoverAgyTranscript([]byte(log), home, id); got != "" {
				t.Fatalf("escaped or invalid transcript: %q", got)
			}
		})
	}
}
func TestAntigravityRecoveryScannerLineLimit(t *testing.T) {
	for _, extra := range []int{0, 1} {
		t.Run(map[int]string{0: "exact", 1: "over"}[extra], func(t *testing.T) {
			home := t.TempDir()
			app := filepath.Join(home, "app")
			p := filepath.Join(app, "brain", agyID, ".system_generated", "logs", "transcript.jsonl")
			os.MkdirAll(filepath.Dir(p), 0700)
			// The ceiling includes the newline. Over-limit records must discard even
			// the valid reply prefix accumulated earlier in the scan.
			base := strings.TrimSuffix(record("PLANNER_RESPONSE", "MODEL", "DONE", ""), "\n")
			line := record("PLANNER_RESPONSE", "MODEL", "DONE", strings.Repeat("x", (16<<20)-len(base)-1+extra))
			content := record("PLANNER_RESPONSE", "MODEL", "DONE", "prefix") + line
			os.WriteFile(p, []byte(content), 0600)
			got := recoverAgyTranscript([]byte("CLI app data directory: "+app+"\n"), home, agyID)
			if extra == 0 {
				if !strings.HasPrefix(got, "prefix\n\n") || len(got) != len("prefix\n\n")+(16<<20)-len(base)-1 {
					t.Fatalf("exact limit rejected: %d", len(got))
				}
			} else if got != "" {
				t.Fatalf("partial scan recovered %d bytes", len(got))
			}
		})
	}
}

func TestAntigravityRecoveryUsesDurableSink(t *testing.T) {
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	var stored []Message
	s, err := (Antigravity{Bin: recoveryScript(t, dir, record("PLANNER_RESPONSE", "MODEL", "DONE", "recovered"), "exit 0", "")}).Execute(context.Background(), "p", ExecOptions{Dir: dir, OnMessage: func(m Message) error { stored = append(stored, m); return nil }})
	if err != nil {
		t.Fatal(err)
	}
	ms, res := collectWithin(t, s, 10*time.Second)
	if res.Err != nil || len(ms) != 0 || len(stored) != 1 || string(stored[0].Content) != `"recovered"` {
		t.Fatalf("%+v %+v %+v", stored, ms, res)
	}
}

// Force the cancellation spill inside a synthetic finish hook, so its final
// recovered record exercises the same open retention file as earlier records.
func TestRunFinishEmitterKeepsRecoverySpillOpen(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, ".cli-home")
	app := filepath.Join(home, "app")
	p := filepath.Join(app, "brain", agyID, ".system_generated", "logs", "transcript.jsonl")
	os.MkdirAll(filepath.Dir(p), 0700)
	os.WriteFile(p, []byte(record("PLANNER_RESPONSE", "MODEL", "DONE", "recovered")), 0600)
	logPath := filepath.Join(dir, ".agy.log")
	os.WriteFile(logPath, []byte("conversation="+agyID+"\nCLI app data directory: "+app+"\n"), 0600)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := &agyRun{model: AntigravityModel}
	hook := r.finish(logPath, home)
	finished := make(chan struct{})
	s, err := run(ctx, command(ctx, script(t, "exit 0"), "", ExecOptions{Dir: dir}, home), func(bool) {}, func([]byte, emitMessage, *Result) (bool, error) { return true, nil }, func(res *Result, emit emitMessage) {
		for i := 0; i < 64; i++ {
			emit(Message{Type: "log", Content: raw(i)})
		}
		cancel()
		emit(Message{Type: "log", Content: raw("spilled before recovery")})
		hook(res, emit)
		close(finished)
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-finished:
	case <-time.After(10 * time.Second):
		t.Fatal("finish blocked")
	}
	ms, res := collectWithin(t, s, 10*time.Second)
	if res.Err != nil || len(ms) != 64 || len(res.RetainedEvidence) != 1 {
		t.Fatalf("%d %+v", len(ms), res)
	}
	b, err := os.ReadFile(res.RetainedEvidence[0])
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	var recovered Message
	if len(lines) != 2 || json.Unmarshal([]byte(lines[1]), &recovered) != nil || recovered.Type != "text" || string(recovered.Content) != `"recovered"` {
		t.Fatalf("spool lost final recovery: %s", b)
	}
}

func TestAntigravityRecoveryPreservesExistingEmitFailure(t *testing.T) {
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	sent := errors.New("init persistence failed")
	var seen []Message
	bin := recoveryScript(t, dir, record("PLANNER_RESPONSE", "MODEL", "DONE", "recovered"), `echo '{"event":"init","conversation_id":"`+agyID+`"}'`, "")
	s, err := (Antigravity{Bin: bin}).Execute(context.Background(), "p", ExecOptions{Dir: dir, OnMessage: func(m Message) error { seen = append(seen, m); return sent }})
	if err != nil {
		t.Fatal(err)
	}
	_, res := collectWithin(t, s, 10*time.Second)
	if !errors.Is(res.Err, sent) || len(seen) != 1 || seen[0].Type != "log" {
		t.Fatalf("%+v %+v", seen, res)
	}
}
