//go:build !darwin && !linux

package agent

import (
	"errors"
	"os"
)

func mcpDescriptor([]byte) (*os.File, error) {
	return nil, errors.New("anonymous MCP configuration is unsupported on this platform")
}
