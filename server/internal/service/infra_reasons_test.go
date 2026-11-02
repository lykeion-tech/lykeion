package service

import "testing"

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
