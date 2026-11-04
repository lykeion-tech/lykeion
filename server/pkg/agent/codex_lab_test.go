package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLabCodexExecutesWithNativeRestrictionsAndCLIAuth(t *testing.T) {
	for _, coresearch := range []bool{false, true} {
		name := "lab"
		if coresearch {
			name = "coresearch"
		}
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("CODEX_HOME", home)
			auth := []byte(`{"fixture":"synthetic-cli-auth"}`)
			if err := os.WriteFile(filepath.Join(home, "auth.json"), auth, 0600); err != nil {
				t.Fatal(err)
			}
			argsFile := filepath.Join(t.TempDir(), "args")
			bin := script(t, `printf '%s\n' "$@" > `+argsFile+`
read -r line || true
echo '{"type":"thread.started","thread_id":"lab-fixture"}'
echo '{"type":"turn.completed","usage":{"input_tokens":3,"output_tokens":2}}'
`)
			session, err := (Codex{Bin: bin}).Execute(t.Context(), "synthetic question", ExecOptions{Lab: true, Aristotle: true, Coresearch: coresearch, Model: "gpt-5.5", Dir: t.TempDir(), Sandbox: testSandbox, DoorURL: "http://localhost/door", DoorToken: "synthetic-door-secret"})
			if err != nil {
				t.Fatal(err)
			}
			for range session.Messages {
			}
			result := <-session.Done
			if result.Err != nil {
				t.Fatal(result.Err)
			}
			if result.SessionID != "lab-fixture" || result.Model != "gpt-5.5" {
				t.Fatalf("unexpected result: %+v", result)
			}
			data, err := os.ReadFile(argsFile)
			if err != nil {
				t.Fatal(err)
			}
			args := string(data)
			for _, want := range []string{"-s\nread-only\n", "--ignore-user-config\n", "--strict-config\n", "--ignore-rules\n", "--disable\nshell_tool\n", "--disable\nunified_exec\n", "--disable\napps\n", "--disable\ncode_mode\n", "--disable\nbrowser_use\n", "web_search=\"disabled\"", "skills.bundled.enabled=false", "tools.update_plan.enabled=false", "mcp_servers.lyk_door.enabled_tools=[\"call_tool\"]", "mcp_servers.lyk_door.bearer_token_env_var=\"LYK_DOOR_TOKEN\""} {
				if !strings.Contains(args, want) {
					t.Errorf("missing %q", want)
				}
			}
			if strings.Count(args, "--ignore-user-config\n") != 1 {
				t.Error("duplicate ignore-user-config flag")
			}
			if strings.Contains(args, "synthetic-door-secret") || strings.Contains(args, "danger-full-access") {
				t.Error("secret or unrestricted sandbox in args")
			}
			got, err := os.ReadFile(filepath.Join(home, "auth.json"))
			if err != nil || string(got) != string(auth) {
				t.Fatal("CLI authentication was modified")
			}
		})
	}
}
