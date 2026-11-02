package service

import (
	"strings"
	"testing"
)

func TestInfraReasons(t *testing.T) {
	notInfra := map[string]bool{"agent_error": true, "context_exhausted": true, "hand_in_invalid": true, "runtime_access_denied": true, "coresearch_access_revoked": true}
	for _, r := range FailureReasons {
		if got := Infra(r); got == notInfra[r] {
			t.Errorf("Infra(%q)=%v", r, got)
		}
	}
	for _, r := range []string{"runtime_offline", "timeout", "provider_network", "sign_in", "cli_missing", "cli_too_old", "door", "cli_broken", "sandbox", "fence_breach"} {
		if !Infra(r) {
			t.Errorf("Infra(%q)=false", r)
		}
	}
	if len(FailureReasons) != 15 {
		t.Errorf("FailureReasons has %d, want 15", len(FailureReasons))
	}
}

// Every infrastructure reason reads as plain words in the owner's Inbox; the
// reason code itself never shows.
func TestMachineWaitTextNamesNoReasonCode(t *testing.T) {
	for _, r := range FailureReasons {
		if !Infra(r) {
			continue
		}
		if got := machineWaitText("laptop", "claude", r, "WebFetch"); strings.Contains(got, "("+r+")") {
			t.Errorf("%s: %q", r, got)
		}
	}
	if got, want := machineWaitText("laptop", "claude", "door", ""), "Claude Code on laptop cannot reach Lykeion's tools. Waiting studies continue once it recovers."; got != want {
		t.Errorf("door: %q", got)
	}
	if got, want := machineWaitText("laptop", "claude", "sign_in", ""), "Claude Code on laptop is signed out. Sign in again on that machine (run `claude login`); waiting studies continue by themselves."; got != want {
		t.Errorf("sign in copy changed: %q", got)
	}
}

// The owner's question about a held run names its last cause in plain words.
func TestEveryInfraReasonHasPlainWords(t *testing.T) {
	for _, r := range FailureReasons {
		if Infra(r) && failureWords[r] == "" {
			t.Errorf("%s has no plain words", r)
		}
	}
}
