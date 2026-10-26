package agent

import (
	"context"
	"runtime"
	"strings"
	"testing"
)

func TestGuestClaudeKeychainServiceUsesOwnerConfig(t *testing.T) {
	for configured, want := range map[string]string{"": "Claude Code-credentials", "/owner/claude": "Claude Code-credentials-6e45a4ca"} {
		if got := claudeKeychainService(configured); got != want {
			t.Errorf("service = %q, want %q", got, want)
		}
	}
}

func TestGuestClaudeAuthEnvContainsOnlyAccessToken(t *testing.T) {
	env, err := claudeGuestAuthEnv([]byte(`{"claudeAiOauth":{"accessToken":"access-fixture","refreshToken":"refresh-fixture"},"settings":"private"}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(env) != 1 || env[0] != "CLAUDE_CODE_OAUTH_TOKEN=access-fixture" {
		t.Fatal("unexpected guest credential fields")
	}
}

func TestGuestClaudeAuthErrorsNeverIncludeCredentials(t *testing.T) {
	for _, data := range []string{`{"claudeAiOauth":{"refreshToken":"secret-fixture"}}`, `secret-fixture`} {
		env, err := claudeGuestAuthEnv([]byte(data))
		if err == nil || len(env) != 0 || strings.Contains(err.Error(), "secret-fixture") {
			t.Fatal("invalid credential was accepted or exposed")
		}
	}
}

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
