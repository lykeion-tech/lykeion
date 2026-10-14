//go:build !unix

package daemon

import (
	"errors"
	"os"
)

func ownState(string) (*os.File, error) {
	return nil, errors.New("daemon process ownership requires Unix")
}
