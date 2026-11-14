package agent

import (
	"context"
	"runtime"
	"testing"
)

func TestGuestAuthRequiresLinux(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, harness := range []string{"claude", "codex"} {
		env, err := GuestAuthEnv(ctx, harness)
		if runtime.GOOS == "linux" {
			if err != nil || len(env) != 0 {
				t.Fatalf("Linux must use only file sign-in: %v %v", env, err)
			}
		} else if err == nil || err.Error() != "shared CLI sign-in requires Linux" || len(env) != 0 {
			t.Fatalf("unsupported guest auth must fail before reading sign-in: %v", err)
		}
	}
}
