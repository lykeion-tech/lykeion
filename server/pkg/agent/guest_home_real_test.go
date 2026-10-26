package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"lykeion/server/internal/daemon/sandbox"
)

var realGuestEnv = func() map[string]string {
	values := map[string]string{}
	for _, key := range []string{"HOME", "CLAUDE_CONFIG_DIR", "CODEX_HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME"} {
		values[key] = os.Getenv(key)
	}
	return values
}()

func TestRealGuestCLIStarts(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("shared CLI execution requires Linux")
	}
	if os.Getenv("LYK_REAL_CLI") != "1" {
		t.Skip("set LYK_REAL_CLI=1 to run signed-in CLIs")
	}
	for key, value := range realGuestEnv {
		t.Setenv(key, value)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	for _, harness := range []string{"claude", "codex"} {
		t.Run(harness, func(t *testing.T) {
			candidates := []string{}
			if bin, err := exec.LookPath(harness); err == nil {
				candidates = append(candidates, bin)
			}
			candidates = append(candidates, filepath.Join(home, ".local", "bin", harness), "/opt/homebrew/bin/"+harness, "/usr/local/bin/"+harness)
			if harness == "codex" {
				candidates = append(candidates, "/Applications/ChatGPT.app/Contents/Resources/codex-cli/CodexCLI.app/Contents/MacOS/codex")
			}
			var bin string
			for _, candidate := range candidates {
				if st, err := os.Stat(candidate); err == nil && st.Mode().IsRegular() && st.Mode()&0111 != 0 {
					bin, err = filepath.EvalSymlinks(candidate)
					if err != nil {
						t.Fatal(err)
					}
					break
				}
			}
			if bin == "" {
				t.Skip("CLI not installed")
			}
			owner := accountHome(harness)
			if harness == "codex" {
				if _, err := os.Stat(filepath.Join(owner, "auth.json")); os.IsNotExist(err) {
					t.Skip("no file sign-in")
				}
			}
			root := t.TempDir()
			guest := filepath.Join(root, "guest")
			run := filepath.Join(root, "run")
			if err := os.Mkdir(run, 0700); err != nil {
				t.Fatal(err)
			}
			if err := PrepareGuestHome(harness, owner, guest); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
			defer cancel()
			var backend Backend = Claude{Bin: bin}
			if harness == "codex" {
				backend = Codex{Bin: bin}
			}
			var transcript strings.Builder
			options := ExecOptions{Guest: true, Dir: run, ConfigHome: guest, Env: []string{"TMPDIR=" + run, "CLAUDE_CODE_TMPDIR=" + run}, OnMessage: func(m Message) error {
				if m.Type == "text" {
					transcript.Write(m.Content)
					transcript.WriteByte('\n')
				}
				return nil
			}}

			envAuth := harness == "claude" && runtime.GOOS == "darwin" && os.Getenv("LYK_REAL_CLI_CLAUDE_ENV_AUTH") == "1"
			if envAuth {
				auth, err := GuestAuthEnv(ctx, harness)
				if err != nil {
					t.Fatal(err)
				}
				options.Env = append(options.Env, auth...)
			}
			keychain := runtime.GOOS == "darwin" && harness == "claude" && !envAuth
			securityServer := runtime.GOOS == "darwin" && harness == "codex"
			options.Sandbox = func(ctx context.Context, paths CLIPaths, name string, args ...string) (*exec.Cmd, error) {
				policy := sandbox.Policy{Home: home, HomeRead: false, Read: []string{filepath.Dir(bin)}, Write: []string{run, guest}, Keychain: paths.Keychain && keychain}
				return policy.Command(ctx, name, args...)
			}
			session, err := backend.Execute(ctx, "Reply with the word ok", options)
			if err != nil {
				t.Fatalf("guest startup: %v", err)
			}
			for range session.Messages {
			}
			result := <-session.Done
			t.Logf("CLI %s; HomeRead=false; Keychain=%t; SecurityServer=%t; reply: %s", harness, keychain, securityServer, transcript.String())
			if result.Err != nil {
				t.Fatalf("guest CLI: %v", result.Err)
			}
			if !strings.Contains(strings.ToLower(transcript.String()), "ok") {
				t.Fatal("reply does not contain ok")
			}
		})
	}
}
