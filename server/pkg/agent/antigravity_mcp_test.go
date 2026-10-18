package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAntigravityReceivesOnlyTheRecordedDoor(t *testing.T) {
	personal := t.TempDir()
	t.Setenv("HOME", personal)
	t.Setenv("GEMINI_CLI_HOME", personal)
	config := filepath.Join(personal, ".gemini", "config")
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	original := []byte(`{"mcpServers":{"personal":{"serverUrl":"https://personal.invalid"}}}`)
	if err := os.WriteFile(filepath.Join(config, "mcp_config.json"), original, 0600); err != nil {
		t.Fatal(err)
	}
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	seen := filepath.Join(t.TempDir(), "seen")
	bin, args := agyScript(t, "", `cat .agents/mcp_config.json > `+shellQuote(seen)+`
printf '%s\n' "$HOME" "$GEMINI_CLI_HOME" > `+shellQuote(seen+".env")+`
echo '{"event":"result","result":{"conversation_id":"a-2","status":"SUCCESS","response":"ok"}}'`)
	for i, token := range []string{"old-door-token", "new-door-token"} {
		resume := ""
		if i > 0 {
			resume = "a-2"
		}
		_, res := agy(t, bin, ExecOptions{Dir: dir, DoorURL: "http://127.0.0.1:1234/mcp", DoorToken: token, MCPToken: "ltk_run", Resume: resume})
		if res.Err != nil {
			t.Fatal(res.Err)
		}
		data, err := os.ReadFile(seen)
		if err != nil {
			t.Fatal(err)
		}
		var got struct {
			MCPServers map[string]struct {
				ServerURL string            `json:"serverUrl"`
				Headers   map[string]string `json:"headers"`
			} `json:"mcpServers"`
		}
		if err = json.Unmarshal(data, &got); err != nil {
			t.Fatal(err)
		}
		door := got.MCPServers["lyk_door"]
		if len(got.MCPServers) != 1 || door.ServerURL != "http://127.0.0.1:1234/mcp" || len(door.Headers) != 1 || door.Headers["Authorization"] != "Bearer "+token {
			t.Fatalf("wrong MCP scope: %s", data)
		}
		if strings.Contains(string(data), "ltk_run") || (i > 0 && strings.Contains(string(data), "old-door-token")) {
			t.Fatal("stale or run credentials in MCP config")
		}
		env, err := os.ReadFile(seen + ".env")
		if err != nil {
			t.Fatal(err)
		}
		if string(env) != filepath.Join(dir, ".home")+"\n"+filepath.Join(dir, ".cli-home")+"\n" {
			t.Fatalf("private scope escaped: %s", env)
		}
		for _, p := range []string{".agents/skills", "AGENTS.md", ".home/.gemini/config/mcp_config.json", ".cli-home/.gemini/config/mcp_config.json"} {
			if _, err := os.Lstat(filepath.Join(dir, p)); !os.IsNotExist(err) {
				t.Fatalf("unexpected private discovery %s: %v", p, err)
			}
		}
		st, err := os.Stat(filepath.Join(dir, ".agents", "mcp_config.json"))
		if err != nil || st.Mode().Perm() != 0600 {
			t.Fatalf("config permissions: %v %v", st, err)
		}
		st, err = os.Stat(filepath.Join(dir, ".agents"))
		if err != nil || st.Mode().Perm() != 0700 {
			t.Fatalf("directory permissions: %v %v", st, err)
		}
	}
	a, err := os.ReadFile(args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(a), "--conversation\na-2\n") || strings.Contains(string(a), "--settings") {
		t.Fatalf("resume args: %s", a)
	}
	if got, err := os.ReadFile(filepath.Join(config, "mcp_config.json")); err != nil || string(got) != string(original) {
		t.Fatalf("personal configuration changed: %v", err)
	}
}

func TestAntigravityMCPRefreshIsSymlinkSafe(t *testing.T) {
	for _, kind := range []string{"directory-link", "file-link", "config-directory", "skills", "instructions", "unowned", "wrong-harness", "ancestor", "empty-directory"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "scratch")
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			bin, _ := agyScript(t, "", `echo '{"event":"result","result":{"status":"SUCCESS","response":"ok"}}'`)
			opts := ExecOptions{Dir: dir, DoorURL: "http://127.0.0.1:1234/mcp", DoorToken: "old"}
			_, res := agy(t, bin, opts)
			if res.Err != nil {
				t.Fatal(res.Err)
			}
			config := filepath.Join(dir, ".agents", "mcp_config.json")
			target := filepath.Join(root, "personal")
			if err := os.WriteFile(target, []byte("personal"), 0600); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "directory-link":
				if err := os.RemoveAll(filepath.Dir(config)); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(root, filepath.Dir(config)); err != nil {
					t.Fatal(err)
				}
			case "file-link", "config-directory":
				if err := os.Remove(config); err != nil {
					t.Fatal(err)
				}
				if kind == "file-link" {
					if err := os.Symlink(target, config); err != nil {
						t.Fatal(err)
					}
				} else {
					if err := os.Mkdir(config, 0700); err != nil {
						t.Fatal(err)
					}
				}
			case "skills":
				if err := os.Mkdir(filepath.Join(dir, ".agents", "skills"), 0700); err != nil {
					t.Fatal(err)
				}
			case "instructions":
				if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("personal"), 0600); err != nil {
					t.Fatal(err)
				}
			case "unowned":
				if err := os.Remove(filepath.Join(dir, ".agent-owned")); err != nil {
					t.Fatal(err)
				}
			case "wrong-harness":
				if err := os.WriteFile(filepath.Join(dir, ".agent-owned"), []byte("lykeion-agent-v1:cursor"), 0600); err != nil {
					t.Fatal(err)
				}
			case "ancestor":
				if err := os.Mkdir(filepath.Join(root, ".agents"), 0700); err != nil {
					t.Fatal(err)
				}
			case "empty-directory":
				if err := os.Remove(config); err != nil {
					t.Fatal(err)
				}
			}
			ran := filepath.Join(root, "ran")
			opts.DoorToken = "new"
			sess, err := (Antigravity{Bin: script(t, "touch "+shellQuote(ran))}).Execute(context.Background(), "p", opts)
			if err == nil {
				collectWithin(t, sess, 5*time.Second)
				t.Fatal("accepted unsafe private discovery")
			}
			if _, err := os.Stat(ran); !os.IsNotExist(err) {
				t.Fatalf("CLI launched: %v", err)
			}
			if got, err := os.ReadFile(target); err != nil || string(got) != "personal" {
				t.Fatalf("symlink target changed: %v", err)
			}
			if kind == "skills" || kind == "unowned" || kind == "wrong-harness" {
				data, err := os.ReadFile(config)
				if err != nil || !strings.Contains(string(data), "Bearer old") {
					t.Fatalf("unrelated config changed: %s %v", data, err)
				}
			}
		})
	}
}

func TestAntigravityMCPRefreshRequiresScratchLock(t *testing.T) {
	dir := t.TempDir()
	bin, _ := agyScript(t, "", `echo '{"event":"result","result":{"status":"SUCCESS","response":"ok"}}'`)
	_, res := agy(t, bin, ExecOptions{Dir: dir, DoorURL: "http://127.0.0.1:1234/mcp", DoorToken: "old"})
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	lock, err := ReserveScratch(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	_, err = (Antigravity{Bin: bin}).Execute(t.Context(), "p", ExecOptions{Dir: dir, DoorURL: "http://127.0.0.1:1234/mcp", DoorToken: "new"})
	if err == nil || !strings.Contains(err.Error(), "scratch is already in use") {
		t.Fatalf("competing launch: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, ".agents", "mcp_config.json"))
	if err != nil || !strings.Contains(string(data), "Bearer old") {
		t.Fatalf("active config changed: %s %v", data, err)
	}
}
