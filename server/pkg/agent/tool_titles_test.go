package agent

import (
	"strings"
	"testing"
)

func TestToolNamingEvidenceUsesActualAdapterInput(t *testing.T) {
	cases := []struct{ tool, body, id, want string }{
		{"Bash", `{"type":"tool_use","id":"a","input":{"command":"python check.py","description":"Check the fitted model"}}`, "a", "python check.py"},
		{"mcp__lyk_door__call_tool", `{"id":"b","input":{"server":"sources","tool":"fetch_text","args":{"url":"https://example.org/paper"},"human_description":"Read the full paper"}}`, "b", "https://example.org/paper"},
		{"shell", `{"item":{"id":"c","type":"command_execution","command":"ls files"}}`, "c", "ls files"},
		{"shellToolCall", `{"call_id":"d","tool_call":{"shellToolCall":{"args":{"command":"echo done"}}}}`, "d", "echo done"},
		{"", `{"step_update":{"step_index":4,"tool_name":"run_command","tool_info":{"parameters":{"CommandLine":"pwd"}}}}`, "step:4", "pwd"},
	}
	for _, c := range cases {
		t.Run(c.id, func(t *testing.T) {
			id, tool, evidence, ok := ToolNamingEvidence(Message{Type: "tool_use", Tool: c.tool, Content: rawJSONForTest(c.body)})
			if !ok || id != c.id || tool == "" || !strings.Contains(evidence, c.want) {
				t.Fatalf("%q %q %q %v", id, tool, evidence, ok)
			}
		})
	}
	if _, _, _, ok := ToolNamingEvidence(Message{Type: "text", Content: []byte(`"hello"`)}); ok {
		t.Fatal("assistant text named as a tool")
	}
}
func rawJSONForTest(s string) []byte { return []byte(s) }
func TestStepTitlesRejectAmbiguousOrUnsafeMetadata(t *testing.T) {
	for _, body := range []string{
		`{"type":"tool_step_titles","version":2,"titles":[{"seq":1,"title":"Read data"}]}`,
		`{"type":"tool_step_titles","version":1,"titles":[{"seq":0,"title":"Read data"}]}`,
		`{"type":"tool_step_titles","version":1,"titles":[{"seq":1,"title":"Read data"},{"seq":1,"title":"Write data"}]}`,
		`{"type":"tool_step_titles","version":1,"titles":[{"seq":1,"title":"<img src=x>"}]}`,
	} {
		if _, err := ParseStepTitles([]byte(body)); err == nil {
			t.Fatalf("accepted %s", body)
		}
	}
	got, err := ParseStepTitles([]byte(`{"type":"tool_step_titles","version":1,"titles":[{"seq":1,"tool_use_id":"a","title":"Check model residuals"}]}`))
	if err != nil || got.Titles[0].Title != "Check model residuals" {
		t.Fatalf("%+v %v", got, err)
	}
}
