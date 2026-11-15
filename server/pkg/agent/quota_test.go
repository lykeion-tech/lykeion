package agent

import (
	"context"
	"testing"
)

func TestProviderQuotaClassification(t *testing.T) {
	for _, text := range []string{
		"You've hit your limit · resets 10pm (Europe/Copenhagen)",
		"You've reached your 5-hour usage limit. Try again later.",
		"You have reached your weekly usage limit",
		"API Error: 429 {\"error\":{\"type\":\"insufficient_quota\"}}",
		"provider quota exhausted: usage limit reached, token budget 0 left",
		"Your credit balance is too low to access the Anthropic API.",
	} {
		if got := classify(text); got != "provider_quota" {
			t.Errorf("%q: %s", text, got)
		}
	}
	for _, text := range []string{"The paper discusses quota sampling", "Please compute the weekly limit", "quota", "rate limited", "overloaded", "connection reset", "context_length_exceeded"} {
		if got := classify(text); got == "provider_quota" {
			t.Errorf("prose/transient/context failure %q became quota", text)
		}
	}
}

func TestQuotaMentionInSuccessfulAnswerIsNotFailure(t *testing.T) {
	for _, harness := range []string{"claude", "codex"} {
		t.Run(harness, func(t *testing.T) {
			event := `{"type":"result","is_error":false,"result":"The quoted message says: provider quota exhausted"}`
			if harness == "codex" {
				event = `{"type":"item.completed","item":{"type":"agent_message","text":"The quoted message says: provider quota exhausted"}}
{"type":"turn.completed","usage":{}}`
			}
			backend, err := New(harness, script(t, "cat <<'EOF'\n"+event+"\nEOF"))
			if err != nil {
				t.Fatal(err)
			}
			session, err := backend.Execute(context.Background(), "p", ExecOptions{Sandbox: testSandbox, Dir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			_, result := collect(t, session)
			if result.Err != nil || result.Reason != "" || result.Output == "" {
				t.Fatalf("%+v", result)
			}
		})
	}
}
