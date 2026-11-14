//go:build linux

package accountlookup

import (
	"context"
	"errors"
	"os/user"
	"testing"
)

func TestCurrentContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := CurrentContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want canceled", err)
	}
}

func TestFilesOnlyContextCanceledDuringLookup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	u, err := filesOnlyContext(ctx, "42", func(string) (*user.User, error) {
		cancel()
		return &user.User{Uid: "42"}, nil
	})
	if u != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("got account %v and error %v; want canceled without account", u, err)
	}
}
