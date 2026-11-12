//go:build !darwin && !linux

package desktopupdate

import (
	"context"
)

func CheckAndCache(context.Context) (Result, error) {
	return Result{}, checkError{Unsupported}
}
