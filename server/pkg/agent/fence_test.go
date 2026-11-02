package agent

import (
	"bytes"
	"os"
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

func TestClaudeSplitsMCPServer(t *testing.T) {
	ms := parsed(t, parseClaude("m", "", nil, nil), `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t","name":"mcp__lyk_door__call_tool","input":{}}]}}`)
	if len(ms) != 1 || ms[0].Server != "lyk_door" || ms[0].Tool != "mcp__lyk_door__call_tool" {
		t.Fatalf("%+v", ms)
	}
}
