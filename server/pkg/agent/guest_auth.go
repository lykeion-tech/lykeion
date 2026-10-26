package agent

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
)

// AccountHome returns the owner's configured CLI home before a run overrides it.
func AccountHome(harness string) string { return accountHome(harness) }

// GuestAuthEnv refuses unsupported platforms before accessing any sign-in.
// Linux uses only the credential file prepared in the guest's config home.
func GuestAuthEnv(_ context.Context, _ string) ([]string, error) {
	if runtime.GOOS != "linux" {
		return nil, errors.New("shared CLI sign-in requires Linux")
	}
	return nil, nil
}

func claudeKeychainService(configured string) string {
	service := "Claude Code-credentials"
	if configured != "" {
		sum := sha256.Sum256([]byte(configured))
		service += fmt.Sprintf("-%x", sum[:4])
	}
	return service
}

func claudeGuestAuthEnv(data []byte) ([]string, error) {
	var credential struct {
		ClaudeAiOauth struct {
			AccessToken string `json:"accessToken"`
		} `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal(data, &credential); err != nil || credential.ClaudeAiOauth.AccessToken == "" {
		return nil, errors.New("owner Claude Keychain sign-in has no OAuth access token")
	}
	return []string{"CLAUDE_CODE_OAUTH_TOKEN=" + credential.ClaudeAiOauth.AccessToken}, nil
}
