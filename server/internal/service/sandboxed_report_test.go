package service

import "testing"

func TestSandboxedReportMode(t *testing.T) {
	for _, mode := range []string{"", "workspace_continuity", "private", "sandboxed"} {
		t.Run(mode, func(t *testing.T) {
			r := Report{Mode: mode}
			if err := r.check(true); err != nil {
				t.Fatalf("known profile %q refused: %v", mode, err)
			}
		})
	}
	r := Report{Mode: "unknown"}
	if err := r.check(true); err == nil {
		t.Fatal("unknown profile accepted")
	}
}
