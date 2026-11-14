//go:build !darwin && !linux

package daemon

import (
	"errors"
	"os"
)

func storageFilesystem(*os.Root, string, os.FileInfo) (string, int64, error) {
	return "", 0, errors.New("filesystem capacity unavailable")
}
