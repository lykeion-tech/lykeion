package cli

import (
	"strings"
	"testing"
)

func TestHypCommandsNeedAnAgentRunToken(t *testing.T) {
	t.Setenv("LYK_TOKEN", "")
	for _, args := range [][]string{{"hyp", "candidate"}, {"hyp", "rank"}, {"hyp", "missing-premise", "--gap", "g"},
		{"hyp", "novelty", "--candidate", "H1", "--status", "unknown", "--query", "q"}} {
		cmd := Root(strings.NewReader(`{}`), &strings.Builder{})
		cmd.SetArgs(args)
		cmd.SetErr(&strings.Builder{})
		if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "LYK_TOKEN") {
			t.Fatalf("%v: %v", args, err)
		}
	}
}

func TestHypStdinMustBeOneJSONObject(t *testing.T) {
	t.Setenv("LYK_TOKEN", "ltk_x")
	t.Setenv("LYK_SERVER", "http://127.0.0.1:1")
	for _, in := range []string{"", "not json", "[1, 2]", `{"a": 1} {"b": 2}`} {
		cmd := Root(strings.NewReader(in), &strings.Builder{})
		cmd.SetArgs([]string{"hyp", "matrix"})
		cmd.SetErr(&strings.Builder{})
		if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "one JSON object") {
			t.Fatalf("%q: %v", in, err)
		}
	}
}
