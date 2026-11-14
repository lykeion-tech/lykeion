//go:build !unix

package envs

import (
	"context"
	"errors"
)

func projectFileLease(context.Context, string, string) (func(), error) {
	return nil, errors.New("project execution leases are unavailable on this platform")
}
