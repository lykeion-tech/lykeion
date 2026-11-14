package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"lykeion/server/internal/toolroute"
	"lykeion/server/pkg/agent"
)

// The offline provider drives real file/skill tools, so nested instruction
// discovery is exercised rather than inferred from startup flags.
func TestRealCoresearchClaudeExcludesAutomaticInstructions(t *testing.T) {
	if os.Getenv("LYK_TEST_SKILLS_REAL") != "1" {
		t.Skip("opt-in installed Claude offline transport")
	}
	for _, late := range []bool{false, true} {
		t.Run(fmt.Sprintf("late-nested=%v", late), func(t *testing.T) {
			bin, err := exec.LookPath("claude")
			must(t, err)
			run := filepath.Join(t.TempDir(), ".lyk", "coresearch", "chat")
			must(t, os.MkdirAll(run, 0700))
			account := filepath.Join(cliAccountFixture(t), ".claude")
			rootFile := filepath.Join(run, "CLAUDE.md")
			nested := filepath.Join(run, ".hidden", "sections")
			target := filepath.Join(nested, "notes.tex")
			const rootMarker = "UPLOADED_ROOT_INSTRUCTION_9d280b"
			const nestedMarker = "UPLOADED_NESTED_INSTRUCTION_47a960"
			const dataMarker = "EXPLICIT_SOURCE_DATA_7ec8bb"
			const skillMarker = "MANAGED_SKILL_BODY_3cb86d"
			const prompt = "Run the Coresearch document fence fixture."
			must(t, os.WriteFile(rootFile, []byte(rootMarker+"\n"), 0600))
			introduce := func() error {
				if err := os.MkdirAll(nested, 0700); err != nil {
					return err
				}
				if err := os.WriteFile(filepath.Join(nested, "CLAUDE.md"), []byte(nestedMarker+"\n"), 0600); err != nil {
					return err
				}
				return os.WriteFile(target, []byte(dataMarker+"\n"), 0600)
			}
			if !late {
				must(t, introduce())
			}
			must(t, placeClaimedSkills("claude", run, []toolroute.RoutedSkill{{Name: "managed-method", Folder: skillFolder(t, map[string]string{"SKILL.md": "---\nname: managed-method\ndescription: Fixture managed method\n---\n" + skillMarker + "\n"})}}))
			var mu sync.Mutex
			var requests [][]byte
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
				if err != nil {
					t.Error(err)
					w.WriteHeader(500)
					return
				}
				if strings.HasSuffix(r.URL.Path, "/count_tokens") {
					w.Header().Set("Content-Type", "application/json")
					io.WriteString(w, `{"input_tokens":1}`)
					return
				}
				if !strings.HasSuffix(r.URL.Path, "/messages") {
					w.WriteHeader(404)
					return
				}
				if !strings.Contains(string(body), prompt) {
					claudeFixtureResponse(w, "", "", nil)
					return
				}
				mu.Lock()
				requests = append(requests, slices.Clone(body))
				step := len(requests)
				mu.Unlock()
				switch step {
				case 1:
					if late {
						if err := introduce(); err != nil {
							t.Error(err)
							w.WriteHeader(500)
							return
						}
					}
					claudeFixtureResponse(w, "read_nested", "Read", map[string]any{"file_path": target})
				case 2:
					claudeFixtureResponse(w, "read_root", "Read", map[string]any{"file_path": rootFile})
				case 3:
					claudeFixtureResponse(w, "load_managed", "Skill", map[string]any{"skill": "lyk-managed-method"})
				default:
					claudeFixtureResponse(w, "", "", nil)
				}
			}))
			defer upstream.Close()
			d := &daemon{cfg: Config{Home: t.TempDir()}}
			ctx, cancel := context.WithTimeout(t.Context(), 25*time.Second)
			defer cancel()
			options := agent.ExecOptions{Dir: run, Coresearch: true, Guest: true, ConfigHome: account, Env: []string{"ANTHROPIC_API_KEY=offline-fixture", "ANTHROPIC_BASE_URL=" + upstream.URL, "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_TELEMETRY=1"}, Sandbox: d.cliSandbox(run, nil, false)}
			session, err := (agent.Claude{Bin: bin}).Execute(ctx, prompt, options)
			must(t, err)
			for range session.Messages {
			}
			result := <-session.Done
			mu.Lock()
			captured := slices.Clone(requests)
			mu.Unlock()
			if result.Err != nil || len(captured) != 4 {
				t.Fatalf("offline Claude tool flow: requests=%d result=%+v", len(captured), result)
			}
			for i := 0; i < 2; i++ {
				text := string(captured[i])
				if strings.Contains(text, rootMarker) {
					t.Errorf("request %d implicitly loaded uploaded root instructions", i+1)
				}
				if strings.Contains(text, nestedMarker) {
					t.Errorf("request %d implicitly loaded nested instructions", i+1)
				}
			}
			if !strings.Contains(string(captured[0]), "lyk-managed-method") {
				t.Error("managed skill missing from real CLI catalogue")
			}
			if !strings.Contains(string(captured[1]), dataMarker) {
				t.Error("real nested source Read did not run")
			}
			if !strings.Contains(string(captured[2]), rootMarker) {
				t.Error("uploaded root document is no longer explicitly readable as data")
			}
			// After explicit Read, the marker belongs only to that tool's data
			// result, never to a separate system/user instruction block.
			var third struct {
				System   json.RawMessage `json:"system"`
				Messages []struct {
					Content json.RawMessage `json:"content"`
				} `json:"messages"`
			}
			must(t, json.Unmarshal(captured[2], &third))
			if strings.Contains(string(third.System), rootMarker) {
				t.Error("explicit document Read promoted source data to system instructions")
			}
			for _, message := range third.Messages {
				var blocks []json.RawMessage
				if err := json.Unmarshal(message.Content, &blocks); err != nil {
					if strings.Contains(string(message.Content), rootMarker) {
						t.Error("source marker appeared outside a tool result")
					}
					continue
				}
				for _, block := range blocks {
					if !strings.Contains(string(block), rootMarker) {
						continue
					}
					var result struct {
						Type      string `json:"type"`
						ToolUseID string `json:"tool_use_id"`
					}
					must(t, json.Unmarshal(block, &result))
					if result.Type != "tool_result" || result.ToolUseID != "tool_offline_read_root" {
						t.Errorf("source marker promoted outside its explicit Read result: type=%s id=%s", result.Type, result.ToolUseID)
					}
				}
			}

			if !strings.Contains(string(captured[3]), skillMarker) {
				t.Error("managed Skill invocation did not load its body")
			}
			if strings.Contains(string(captured[3]), nestedMarker) {
				t.Error("nested instructions entered context after explicit data/skill reads")
			}
			if !t.Failed() {
				t.Logf("Claude: root/nested auto instructions excluded; late=%v; explicit source Read and managed Skill loaded; four localhost requests, no external model", late)
			}
		})
	}
}

func claudeFixtureResponse(w http.ResponseWriter, callID, tool string, input map[string]any) {
	w.Header().Set("Content-Type", "text/event-stream")
	emit := func(event string, value any) {
		b, _ := json.Marshal(value)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
	}
	emit("message_start", map[string]any{"type": "message_start", "message": map[string]any{"id": "msg_offline", "type": "message", "role": "assistant", "model": "offline-fixture", "content": []any{}, "stop_reason": nil, "stop_sequence": nil, "usage": map[string]int{"input_tokens": 1, "output_tokens": 0}}})
	reason := "end_turn"
	if tool != "" {
		reason = "tool_use"
		emit("content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "tool_use", "id": "tool_offline_" + callID, "name": tool, "input": map[string]any{}}})
		b, _ := json.Marshal(input)
		emit("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "input_json_delta", "partial_json": string(b)}})
	} else {
		emit("content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}})
		emit("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": "done"}})
	}
	emit("content_block_stop", map[string]any{"type": "content_block_stop", "index": 0})
	emit("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": reason, "stop_sequence": nil}, "usage": map[string]int{"output_tokens": 1}})
	emit("message_stop", map[string]any{"type": "message_stop"})
}
