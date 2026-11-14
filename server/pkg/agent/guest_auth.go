package agent

import (
	"context"
	"errors"
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
