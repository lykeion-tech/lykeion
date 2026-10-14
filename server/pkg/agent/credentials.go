package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"time"
)

const maxCredentialBytes = 1 << 20

// Explicit source profiles never fall through to the user's global keychain.
func readSignIn(path, harness, platform string, explicit bool, keychain func() ([]byte, error)) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		return data, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	if harness == "claude" && platform == "darwin" && !explicit {
		return keychain()
	}
	return nil, nil
}

func defaultClaudeSignIn() ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return readClaudeKeychain(ctx, func(ctx context.Context, out io.Writer) error {
		cmd := exec.CommandContext(ctx, "/usr/bin/security", "find-generic-password", "-s", "Claude Code-credentials", "-w")
		cmd.Stdout = out
		// Never surface command stderr, which may include credential-source details.
		cmd.Stderr = io.Discard
		cmd.WaitDelay = time.Second
		return cmd.Run()
	})
}

type credentialBuffer struct {
	buffer   bytes.Buffer
	overflow bool
}

func (b *credentialBuffer) Write(p []byte) (int, error) {
	if len(p) > maxCredentialBytes-b.buffer.Len() {
		b.overflow = true
		return 0, errors.New("credential size limit exceeded")
	}
	return b.buffer.Write(p)
}

func readClaudeKeychain(ctx context.Context, run func(context.Context, io.Writer) error) ([]byte, error) {
	var out credentialBuffer
	err := run(ctx, &out)
	if ctx.Err() != nil {
		return nil, errors.New("Claude keychain lookup timed out")
	}
	if out.overflow {
		return nil, errors.New("Claude keychain credential exceeds size limit")
	}
	// Missing or unavailable keychain sign-in leaves the CLI to report auth state.
	if err != nil {
		return nil, nil
	}
	data := bytes.TrimSpace(out.buffer.Bytes())
	var object map[string]json.RawMessage
	if len(data) == 0 || json.Unmarshal(data, &object) != nil || object == nil {
		return nil, errors.New("Claude keychain credential is not a JSON object")
	}
	return data, nil
}
