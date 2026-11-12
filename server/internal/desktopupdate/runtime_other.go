//go:build !darwin && !linux

package desktopupdate

import (
	"context"
	"os"
)

func CheckAndCache(context.Context) (Result, error) {
	return Result{}, checkError{Unsupported}
}

func StreamVerified(context.Context, *os.File, *os.File) error { return checkError{Unsupported} }
