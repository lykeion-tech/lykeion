package agent

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCursorDiscoveryPreservesUnexpectedConfig(t *testing.T) {
	for _, kind := range []string{"ancestor", "directory-link", "file-link", "mcp-directory", "extra", "empty", "file", "unowned", "wrong-harness"} {
		t.Run(kind, func(t *testing.T) {
			root, _ := filepath.EvalSymlinks(t.TempDir())
			dir := filepath.Join(root, "scratch")
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			config := filepath.Join(dir, ".cursor")
			target := filepath.Join(root, "target")
			original := []byte("personal configuration")
			if err := os.WriteFile(target, original, 0600); err != nil {
				t.Fatal(err)
			}
			marker := "lykeion-agent-v1:cursor"
			if kind == "wrong-harness" {
				marker = "lykeion-agent-v1:claude"
			}
			if kind != "unowned" {
				if err := os.WriteFile(filepath.Join(dir, ".agent-owned"), []byte(marker), 0600); err != nil {
					t.Fatal(err)
				}
			}
			switch kind {
			case "ancestor":
				config = filepath.Join(root, ".cursor")
			case "directory-link":
				if err := os.Symlink(root, config); err != nil {
					t.Fatal(err)
				}
			case "file":
				if err := os.WriteFile(config, original, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if kind != "directory-link" && kind != "file" {
				if err := os.Mkdir(config, 0700); err != nil {
					t.Fatal(err)
				}
				if kind == "mcp-directory" {
					if err := os.Mkdir(filepath.Join(config, "mcp.json"), 0700); err != nil {
						t.Fatal(err)
					}
				} else if kind == "file-link" {
					if err := os.Symlink(target, filepath.Join(config, "mcp.json")); err != nil {
						t.Fatal(err)
					}
				} else if kind != "empty" {
					if err := os.WriteFile(filepath.Join(config, "mcp.json"), original, 0600); err != nil {
						t.Fatal(err)
					}
				}
				if kind == "extra" {
					if err := os.WriteFile(filepath.Join(config, "rules.md"), original, 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			bin := script(t, `exit 91`)
			s, err := (Cursor{Bin: bin}).Execute(t.Context(), "p", ExecOptions{Dir: dir})
			if err == nil {
				collectWithin(t, s, 5*time.Second)
				t.Fatal("accepted unsafe Cursor discovery")
			}
			if _, err := os.Lstat(config); err != nil {
				t.Fatal("configuration removed", err)
			}
			if got, err := os.ReadFile(target); err != nil || string(got) != string(original) {
				t.Fatal("symlink target changed", err)
			}
			if kind == "extra" || kind == "unowned" || kind == "wrong-harness" {
				if got, err := os.ReadFile(filepath.Join(config, "mcp.json")); err != nil || string(got) != string(original) {
					t.Fatal("config changed", err)
				}
			}
		})
	}
}

func TestPrivateHarnessesRefuseCursorDiscovery(t *testing.T) {
	for _, h := range []string{"claude", "codex"} {
		for _, inherit := range []bool{false, true} {
			t.Run(fmt.Sprint(h, inherit), func(t *testing.T) {
				dir := t.TempDir()
				if err := os.Mkdir(filepath.Join(dir, ".cursor"), 0700); err != nil {
					t.Fatal(err)
				}
				_, cleanup, err := prepareScratch(&ExecOptions{Dir: dir, Inherit: inherit}, h, "TASK5A_CONFIG", ".missing", "auth.json")
				if cleanup != nil {
					cleanup(true)
				}
				if inherit && err != nil {
					t.Fatal(err)
				}
				if !inherit && (err == nil || !strings.Contains(err.Error(), "unsafe scratch discovery")) {
					t.Fatalf("private discovery accepted: %v", err)
				}
			})
		}
	}
}

func TestCursorCompetingProcessPreservesActiveConfig(t *testing.T) {
	if os.Getenv("TASK5A_CURSOR_HOLDER") == "1" {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		s, err := (Cursor{Bin: os.Getenv("TASK5A_BIN")}).Execute(ctx, "p", ExecOptions{Dir: os.Getenv("TASK5A_DIR"), MCPURL: "http://active/mcp", MCPToken: "ltk_active"})
		if err != nil {
			t.Fatal(err)
		}
		fmt.Println("LOCKED")
		if _, err := bufio.NewReader(os.Stdin).ReadString('\n'); err != nil {
			t.Fatal(err)
		}
		cancel()
		collectWithin(t, s, 5*time.Second)
		return
	}
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	bin := script(t, `cat >/dev/null; sleep 300`)
	cmd := exec.Command(os.Args[0], "-test.run=^TestCursorCompetingProcessPreservesActiveConfig$")
	cmd.Env = append(os.Environ(), "TASK5A_CURSOR_HOLDER=1", "TASK5A_DIR="+dir, "TASK5A_BIN="+bin)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	joined := false
	defer func() {
		if !joined {
			in.Close()
			cmd.Process.Kill()
			cmd.Wait()
		}
	}()
	ready := make(chan bool, 1)
	go func() {
		scanner := bufio.NewScanner(out)
		for scanner.Scan() {
			if scanner.Text() == "LOCKED" {
				ready <- true
				return
			}
		}
		ready <- false
	}()
	select {
	case ok := <-ready:
		if !ok {
			t.Fatal("holder did not acquire lock")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("holder handshake timed out")
	}
	config := filepath.Join(dir, ".cursor", "mcp.json")
	before, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (Cursor{Bin: bin}).Execute(t.Context(), "p", ExecOptions{Dir: dir, MCPURL: "http://competing/mcp", MCPToken: "ltk_competing"}); err == nil || !strings.Contains(err.Error(), "scratch is already in use") {
		t.Fatalf("competing run: %v", err)
	}
	after, err := os.ReadFile(config)
	if err != nil || string(before) != string(after) {
		t.Fatal("active configuration changed", err)
	}
	fmt.Fprintln(in, "release")
	in.Close()
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	joined = true
	s, err := (Cursor{Bin: script(t, `cat >/dev/null`)}).Execute(t.Context(), "p", ExecOptions{Dir: dir, MCPURL: "http://next/mcp", MCPToken: "ltk_next"})
	if err != nil {
		t.Fatal(err)
	}
	collectWithin(t, s, 5*time.Second)
	after, err = os.ReadFile(config)
	if err != nil || !strings.Contains(string(after), "http://next/mcp") || strings.Contains(string(after), "ltk_active") {
		t.Fatalf("configuration not rewritten: %s %v", after, err)
	}
}
