//go:build !unix

package agent

import (
	"context"
	"errors"
	"os"
	"os/exec"
)

func supervised(context.Context, *exec.Cmd) (*exec.Cmd, *os.File, *os.File, string, error) {
	return nil, nil, nil, "", errors.New("agent supervisor requires Unix")
}
func recordProcess(*exec.Cmd, string) error { return errors.New("agent supervisor requires Unix") }
func supervisorExit(*exec.Cmd, string, error) error {
	return errors.New("agent supervisor requires Unix")
}
func RecoverScratch(string) error       { return errors.New("agent recovery requires Unix") }
func readMarker(string) ([]byte, error) { return nil, errors.New("agent supervisor requires Unix") }
