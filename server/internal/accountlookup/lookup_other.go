//go:build !linux

// Package accountlookup resolves account identity independently of HOME.
package accountlookup

import "os/user"

func Current() (*user.User, error) { return user.Current() }

func LookupId(uid string) (*user.User, error) { return user.LookupId(uid) }
