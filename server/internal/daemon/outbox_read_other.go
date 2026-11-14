//go:build !darwin && !linux

package daemon

import (
	"fmt"
	"os"
)

func openReportFile(path string) (*os.File, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("nonregular queue record %s", path)
	}
	return os.Open(path)
}
