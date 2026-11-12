//go:build !darwin && !linux

package desktopupdate

import (
	"context"
	"errors"
)

func CheckAndCache(context.Context) (Result, error) {
	return Result{}, errors.New("update check unavailable")
}
