//go:build !darwin && !linux

package cli

import (
	"errors"
	"os/exec"
)

var errDaemonPlatform = errors.New("daemon process ownership is supported on macOS and Linux only")

func processStart(int) (string, error)                    { return "", errDaemonPlatform }
func detach(*exec.Cmd) error                              { return errDaemonPlatform }
func terminate(int) error                                 { return errDaemonPlatform }
func ownedPrivateDir(string) error                        { return errDaemonPlatform }
func clearStoppedOwner(string, string, daemonOwner) error { return errDaemonPlatform }
