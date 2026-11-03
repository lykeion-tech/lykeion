package agent

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// parsed runs one harness's parser over lines and returns its messages.
func parsed(t *testing.T, p parser, lines ...string) []Message {
	t.Helper()
	var out []Message
	for _, l := range lines {
		if _, err := p([]byte(l), func(m Message) { out = append(out, m) }, &Result{}); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

func TestFenceCatchesCodexApps(t *testing.T) {
	b, err := os.ReadFile("testdata/codex_apps_web_run.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	ms := parsed(t, parseCodex("m"), string(bytes.TrimSpace(b)))
	if len(ms) != 1 {
		t.Fatalf("messages %+v", ms)
	}
	if breach, what := Fenced("codex", ms[0]); !breach || what != "codex_apps/search_service.web_run" {
		t.Fatalf("Fenced = %v %q", breach, what)
	}
}

func TestFenceTable(t *testing.T) {
	codex := parseCodex("m")
	claude := parseClaude("m", "", nil, nil)
	use := func(name string) string {
		return `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t","name":"` + name + `","input":{}}]}}`
	}
	for _, tc := range []struct {
		name    string
		harness string
		line    string
		breach  bool
	}{
		{"codex door call_tool", "codex", `{"type":"item.completed","item":{"id":"i","type":"mcp_tool_call","server":"lyk_door","tool":"call_tool"}}`, false},
		{"codex shell", "codex", `{"type":"item.completed","item":{"id":"i","type":"command_execution","command":"ls"}}`, false},
		{"codex file change", "codex", `{"type":"item.completed","item":{"id":"i","type":"file_change","changes":[]}}`, false},
		{"codex reasoning", "codex", `{"type":"item.completed","item":{"id":"i","type":"reasoning","text":"hm"}}`, false},
		{"codex todo list", "codex", `{"type":"item.completed","item":{"id":"i","type":"todo_list","items":[]}}`, false},
		{"codex collab tool call", "codex", `{"type":"item.completed","item":{"id":"i","type":"collab_tool_call","tool":"spawn_agent"}}`, true},
		{"codex unknown item", "codex", `{"type":"item.completed","item":{"id":"i","type":"made_up_thing"}}`, true},
		{"codex error item", "codex", `{"type":"item.completed","item":{"id":"i","type":"error","message":"x"}}`, false},
		{"codex mcp without server", "codex", `{"type":"item.completed","item":{"id":"i","type":"mcp_tool_call","tool":"shell"}}`, true},
		{"codex apps call started", "codex", `{"type":"item.started","item":{"id":"i","type":"mcp_tool_call","server":"codex_apps","tool":"search_service.web_run"}}`, true},
		{"codex web search started", "codex", `{"type":"item.started","item":{"id":"i","type":"web_search"}}`, true},
		{"codex door call started", "codex", `{"type":"item.started","item":{"id":"i","type":"mcp_tool_call","server":"lyk_door","tool":"call_tool"}}`, false},
		{"codex shell started", "codex", `{"type":"item.started","item":{"id":"i","type":"command_execution","command":"ls"}}`, false},
		{"codex web search", "codex", `{"type":"item.completed","item":{"id":"i","type":"web_search","query":"x"}}`, true},
		{"codex other mcp server", "codex", `{"type":"item.completed","item":{"id":"i","type":"mcp_tool_call","server":"github","tool":"call_tool"}}`, true},
		{"claude WebFetch", "claude", use("WebFetch"), true},
		{"claude WebSearch", "claude", use("WebSearch"), true},
		{"claude Task", "claude", use("Task"), true},
		{"claude door run_script", "claude", use("mcp__lyk_door__run_script"), false},
		{"claude github", "claude", use("mcp__github__x"), true},
		{"claude Bash", "claude", use("Bash"), false},
		{"claude Skill", "claude", use("Skill"), false},
		{"claude ToolSearch", "claude", use("ToolSearch"), false},
		{"claude BashOutput", "claude", use("BashOutput"), false},
		{"claude TaskOutput", "claude", use("TaskOutput"), false},
		{"claude KillShell", "claude", use("KillShell"), false},
		{"claude KillBash", "claude", use("KillBash"), false},
		{"claude TaskStop", "claude", use("TaskStop"), false},
		{"claude ListMcpResourcesTool", "claude", use("ListMcpResourcesTool"), false},
		{"claude ReadMcpResourceTool", "claude", use("ReadMcpResourceTool"), false},
		{"claude ExitPlanMode", "claude", use("ExitPlanMode"), true},
		{"claude AskUserQuestion", "claude", use("AskUserQuestion"), true},
		{"claude text", "claude", `{"type":"assistant","message":{"content":[{"type":"text","text":"WebFetch"}]}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := codex
			if tc.harness == "claude" {
				p = claude
			}
			ms := parsed(t, p, tc.line)
			got := false
			for _, m := range ms {
				if b, _ := Fenced(tc.harness, m); b {
					got = true
				}
			}
			if got != tc.breach {
				t.Fatalf("breach %v, want %v (%+v)", got, tc.breach, ms)
			}
		})
	}
}

func TestCodexFileChangeIsAToolCall(t *testing.T) {
	ms := parsed(t, parseCodex("m"), `{"type":"item.completed","item":{"id":"i","type":"file_change","changes":[]}}`)
	if len(ms) != 1 || ms[0].Type != "tool_use" || ms[0].Tool != "file_change" {
		t.Fatalf("%+v", ms)
	}
}

func TestClaudeSplitsMCPServer(t *testing.T) {
	ms := parsed(t, parseClaude("m", "", nil, nil), `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t","name":"mcp__lyk_door__call_tool","input":{}}]}}`)
	if len(ms) != 1 || ms[0].Server != "lyk_door" || ms[0].Tool != "mcp__lyk_door__call_tool" {
		t.Fatalf("%+v", ms)
	}
}

func TestClaudeStructuredOutputIsNotABreach(t *testing.T) {
	if breach, tool := Fenced("claude", Message{Type: "tool_use", Tool: "StructuredOutput"}); breach {
		t.Fatalf("StructuredOutput fenced as %q", tool)
	}
}

func TestAristotleNativeToolFence(t *testing.T) {
	for _, tc := range []struct {
		h     string
		m     Message
		allow bool
	}{
		{"claude", Message{Type: "tool_use", Tool: "Skill"}, false},
		{"claude", Message{Type: "tool_use", Tool: "Read"}, false},
		{"claude", Message{Type: "tool_use", Tool: "StructuredOutput"}, true},
		{"claude", Message{Type: "tool_use", Server: "lyk_door", Tool: "mcp__lyk_door__call_tool"}, true},
		{"codex", Message{Type: "log", Content: []byte(`{"type":"item.started","item":{"type":"command_execution"}}`)}, false},
		{"codex", Message{Type: "tool_use", Content: []byte(`{"item":{"type":"mcp_tool_call","server":"lyk_door","tool":"call_tool"}}`)}, true},
	} {
		if b, _ := LabFenced(tc.h, tc.m); b == tc.allow {
			t.Fatal(tc)
		}
	}
}

func TestLabCodexRefusesBeforeLoadingHostileConfigOrLaunching(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	t.Setenv("OPENAI_BASE_URL", "http://127.0.0.1:1")
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("model_provider='external'\n[model_providers.external]\nname='external'\nbase_url='http://127.0.0.1:1'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := (Codex{}).Execute(t.Context(), "private question", ExecOptions{Lab: true, Dir: t.TempDir(), Sandbox: func(context.Context, CLIPaths, string, ...string) (*exec.Cmd, error) {
		t.Fatal("unrestricted CLI launched")
		return nil, nil
	}})
	if !errors.Is(err, ErrLabHarness) {
		t.Fatalf("wrong error: %v", err)
	}
}

func TestLabClaudeRefusesUnverifiedVersions(t *testing.T) {
	for _, version := range []string{"2.1.316", "3.0.0", "unknown"} {
		o := ExecOptions{Lab: true, Dir: t.TempDir(), Sandbox: func(ctx context.Context, _ CLIPaths, _ string, args ...string) (*exec.Cmd, error) {
			if len(args) != 1 || args[0] != "--version" {
				t.Fatal("launched unverified CLI")
			}
			return exec.CommandContext(ctx, "/usr/bin/printf", "%s", version), nil
		}}
		if err := checkLabClaude(t.Context(), "claude", o); err == nil {
			t.Fatalf("accepted %q", version)
		}
	}
}
