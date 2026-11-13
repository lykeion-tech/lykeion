//go:build !linux

// Package accountlookup resolves account identity independently of HOME.
package accountlookup

import (
	"context"
	"os/user"
)

func Current() (*user.User, error) { return user.Current() }

func CurrentContext(ctx context.Context) (*user.User, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return Current()
}

func LookupId(uid string) (*user.User, error) { return user.LookupId(uid) }
