package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/pelletier/go-toml/v2"
	"lykeion/server/internal/daemon/sandbox"
)

var mcpCacheTestKeys = map[string]string{
	"npm_config_cache": "npm", "UV_CACHE_DIR": "uv",
	"UV_PYTHON_INSTALL_DIR": "uv-python", "UV_TOOL_DIR": "uv-tools",
}

// This subprocess fixture launches a real child with Codex's stdio allowlist
// and explicit-env precedence. The child observes its environment and writes
// into its caches, independently of the adapter's argument construction.
func init() {
	if len(os.Args) < 3 || os.Args[1] != "--codex-mcp-fixture" {
		return
	}
	args := os.Args[2:]
	if err := codexMCPProcess(args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	os.Exit(0)
}

func codexMCPProcess(args []string) error {
	if args[0] == "probe" {
		seen := map[string]string{}
		for key := range mcpCacheTestKeys {
			seen[key] = os.Getenv(key)
			if seen[key] != "" {
				if err := os.MkdirAll(seen[key], 0700); err != nil {
					return err
				}
				if err := os.WriteFile(filepath.Join(seen[key], "mcp-child-probe"), []byte("child"), 0600); err != nil {
					return err
				}
			}
		}
		for _, key := range []string{"PERSONAL_STATIC", "PERSONAL_FORWARDED"} {
			seen[key] = os.Getenv(key)
		}
		data, _ := json.Marshal(seen)
		if err := os.WriteFile(args[1]+".tmp", data, 0600); err != nil {
			return err
		}
		if err := os.Rename(args[1]+".tmp", args[1]); err != nil {
			return err
		}
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			var request struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
				Params struct {
					ProtocolVersion string `json:"protocolVersion"`
				} `json:"params"`
			}
			if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
				return err
			}
			if len(request.ID) == 0 {
				continue
			}
			var result any = map[string]any{"tools": []any{}}
			if request.Method == "initialize" {
				result = map[string]any{"protocolVersion": request.Params.ProtocolVersion, "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]string{"name": "cache-probe", "version": "1"}}
			}
			if err := json.NewEncoder(os.Stdout).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result}); err != nil {
				return err
			}
		}
		return scanner.Err()
	}
	data, err := os.ReadFile(filepath.Join(os.Getenv("CODEX_HOME"), "config.toml"))
	if err != nil {
		return err
	}
	config := map[string]any{}
	if !slices.Contains(args, "--ignore-user-config") {
		if err := toml.Unmarshal(data, &config); err != nil {
			return err
		}
	}
	layer := map[string]any{}
	for i := 0; i+1 < len(args); i++ {
		if args[i] != "-c" {
			continue
		}
		key, value, ok := strings.Cut(args[i+1], "=")
		if !ok {
			return fmt.Errorf("invalid fixture override")
		}
		parsed := map[string]any{}
		if err := toml.Unmarshal([]byte("value="+value), &parsed); err != nil {
			return err
		}
		current := layer
		parts := strings.Split(strings.TrimSpace(key), ".")
		for _, part := range parts[:len(parts)-1] {
			next, ok := current[part].(map[string]any)
			if !ok {
				next = map[string]any{}
				current[part] = next
			}
			current = next
		}
		current[parts[len(parts)-1]] = parsed["value"]
		i++
	}
	mergeMCPTestConfig(config, layer)
	servers, _ := config["mcp_servers"].(map[string]any)
	for _, rawServer := range servers {
		server := rawServer.(map[string]any)
		command, ok := server["command"].(string)
		if !ok || server["enabled"] == false {
			continue
		}
		var argv []string
		for _, arg := range server["args"].([]any) {
			argv = append(argv, arg.(string))
		}
		env := map[string]string{}
		for _, key := range []string{"HOME", "LOGNAME", "PATH", "SHELL", "USER", "__CF_USER_TEXT_ENCODING", "LANG", "LC_ALL", "TERM", "TMPDIR", "TZ"} {
			if value, ok := os.LookupEnv(key); ok {
				env[key] = value
			}
		}
		forwarded, _ := server["env_vars"].([]any)
		for _, key := range forwarded {
			if value, ok := os.LookupEnv(key.(string)); ok {
				env[key.(string)] = value
			}
		}
		explicit, _ := server["env"].(map[string]any)
		for key, value := range explicit {
			env[key] = value.(string)
		}
		child := exec.Command(command, argv...)
		for key, value := range env {
			child.Env = append(child.Env, key+"="+value)
		}
		if _, err := child.Output(); err != nil {
			return fmt.Errorf("MCP fixture child failed: %w", err)
		}
	}
	fmt.Println(`{"type":"turn.completed","usage":{}}`)
	return nil
}

func mergeMCPTestConfig(base, overlay map[string]any) {
	for key, value := range overlay {
		left, lok := base[key].(map[string]any)
		right, rok := value.(map[string]any)
		if lok && rok {
			mergeMCPTestConfig(left, right)
		} else {
			base[key] = value
		}
	}
}

func mcpCacheFixture(t *testing.T, names []string, explicit bool) (root, cache, configPath string, probes []string) {
	t.Helper()
	root = t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	codexHome := filepath.Join(root, "account codex")
	if err := os.MkdirAll(codexHome, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", codexHome)
	t.Setenv("PERSONAL_FORWARDED", "forwarded-sentinel")
	cache = filepath.Join(root, "cache with space \"quote\" \\ backslash λ")
	servers := map[string]any{}
	for i, name := range names {
		probe := filepath.Join(root, fmt.Sprintf("probe-%d.json", i))
		probes = append(probes, probe)
		env := map[string]string{"PERSONAL_STATIC": "private-sentinel"}
		if explicit {
			for key := range mcpCacheTestKeys {
				env[key] = filepath.Join(root, "shared", key)
			}
		}
		servers[name] = map[string]any{"command": os.Args[0], "args": []string{"--codex-mcp-fixture", "probe", probe}, "env": env, "env_vars": []string{"PERSONAL_FORWARDED"}}
	}
	servers["remote"] = map[string]any{"url": "http://127.0.0.1:1/mcp", "enabled": false, "http_headers": map[string]string{"Authorization": "private-header"}}
	data, err := toml.Marshal(map[string]any{"mcp_servers": servers})
	if err != nil {
		t.Fatal(err)
	}
	configPath = filepath.Join(codexHome, "config.toml")
	if err := os.WriteFile(configPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	return
}

func assertMCPCacheProbe(t *testing.T, path, cache string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var seen map[string]string
	if err := json.Unmarshal(data, &seen); err != nil {
		t.Fatal(err)
	}
	for key, sub := range mcpCacheTestKeys {
		want := filepath.Join(cache, sub)
		if seen[key] != want {
			t.Errorf("MCP child %s=%q, want %q", key, seen[key], want)
		}
		if _, err := os.Stat(filepath.Join(want, "mcp-child-probe")); err != nil {
			t.Errorf("MCP child did not write private %s cache: %v", key, err)
		}
	}
	if seen["PERSONAL_STATIC"] != "private-sentinel" || seen["PERSONAL_FORWARDED"] != "forwarded-sentinel" {
		t.Error("personal MCP environment was not preserved")
	}
}

func TestCodexAccountMCPCacheEnvironment(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(fmt.Sprintf("explicit=%v", explicit), func(t *testing.T) {
			root, cache, path, probes := mcpCacheFixture(t, []string{"ordinary", "dots.and spaces \"quote\" = \\ λ"}, explicit)
			before, _ := os.ReadFile(path)
			stat, _ := os.Stat(path)
			var argv []string
			opts := ExecOptions{Dir: t.TempDir(), CacheDir: cache, Model: "fixture", DoorURL: "http://127.0.0.1:1/mcp", Sandbox: func(ctx context.Context, _ CLIPaths, bin string, args ...string) (*exec.Cmd, error) {
				argv = slices.Clone(args)
				return exec.CommandContext(ctx, os.Args[0], append([]string{"--codex-mcp-fixture", "cli"}, args...)...), nil
			}}
			s, err := (Codex{Bin: os.Args[0]}).Execute(context.Background(), "probe", opts)
			if err != nil {
				t.Fatal(err)
			}
			if _, result := collectWithin(t, s, 10*time.Second); result.Err != nil {
				t.Fatal(result.Err)
			}
			for _, probe := range probes {
				assertMCPCacheProbe(t, probe, cache)
			}
			if _, err := os.Stat(filepath.Join(root, "shared")); !os.IsNotExist(err) {
				t.Error("MCP child wrote a shared cache")
			}
			for _, secret := range []string{"private-sentinel", "private-header", "forwarded-sentinel"} {
				if strings.Contains(strings.Join(argv, "\n"), secret) {
					t.Error("personal secret copied into argv")
				}
			}
			after, _ := os.ReadFile(path)
			afterStat, _ := os.Stat(path)
			if !reflect.DeepEqual(before, after) || !stat.ModTime().Equal(afterStat.ModTime()) {
				t.Error("personal config changed")
			}
			for i, arg := range argv {
				if arg == "-c" && strings.TrimSpace(strings.SplitN(argv[i+1], "=", 2)[0]) == "mcp_servers" {
					var overlay map[string]map[string]map[string]any
					if err := toml.Unmarshal([]byte(argv[i+1]), &overlay); err != nil {
						t.Fatal(err)
					}
					if len(overlay["mcp_servers"]) != 2 {
						t.Error("cache overlay changed HTTP server or literal IDs")
					}
					for _, server := range overlay["mcp_servers"] {
						if len(server) != 1 || server["env"] == nil {
							t.Error("overlay copied personal server fields")
						}
					}
				}
			}
		})
	}
}

func TestCodexAccountMCPCacheClash(t *testing.T) {
	_, cache, _, probes := mcpCacheFixture(t, []string{"lyk_door", "ordinary"}, false)
	var argv []string
	opts := ExecOptions{Dir: t.TempDir(), CacheDir: cache, Model: "fixture", DoorURL: "http://127.0.0.1:1/mcp", Sandbox: func(ctx context.Context, _ CLIPaths, bin string, args ...string) (*exec.Cmd, error) {
		argv = slices.Clone(args)
		return exec.CommandContext(ctx, os.Args[0], append([]string{"--codex-mcp-fixture", "cli"}, args...)...), nil
	}}
	s, err := (Codex{Bin: os.Args[0]}).Execute(context.Background(), "probe", opts)
	if err != nil {
		t.Fatal(err)
	}
	if _, result := collectWithin(t, s, 10*time.Second); result.Err != nil {
		t.Fatal(result.Err)
	}
	if !slices.Contains(argv, "--ignore-user-config") {
		t.Error("reserved-name clash no longer ignores personal config")
	}
	for _, arg := range argv {
		if strings.TrimSpace(strings.SplitN(arg, "=", 2)[0]) == "mcp_servers" {
			t.Error("cache overlay resurrected ignored personal servers")
		}
	}
	for _, probe := range probes {
		if _, err := os.Stat(probe); !os.IsNotExist(err) {
			t.Error("ignored personal MCP was started")
		}
	}
}

// Opt in with an installed native Codex binary. This runs its actual config
// merge and MCP launcher inside the OS sandbox with a disposable account home,
// no credentials, and a local provider. It cancels after the MCP child probe.
func TestCodexAccountMCPCacheRealProvider(t *testing.T) {
	bin := os.Getenv("LYK_TEST_CODEX_BIN")
	if bin == "" {
		t.Skip("set LYK_TEST_CODEX_BIN to test the installed Codex MCP launcher")
	}
	bin, err := filepath.EvalSymlinks(bin)
	if err != nil {
		t.Fatal(err)
	}
	realHome, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	root, cache, _, probes := mcpCacheFixture(t, []string{"cache_probe"}, true)
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("CODEX_API_KEY", "")
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "local fixture; no model call", http.StatusServiceUnavailable)
	}))
	defer provider.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	opts := ExecOptions{Dir: root, CacheDir: cache, Model: "gpt-fixture", Env: []string{"TMPDIR=" + root}, OnMessage: func(Message) error { return nil }, Sandbox: func(ctx context.Context, _ CLIPaths, bin string, args ...string) (*exec.Cmd, error) {
		extra := []string{"-c", `model_provider="local_fixture"`, "-c", `model_providers.local_fixture={name="local fixture",base_url=` + string(raw(provider.URL)) + `,wire_api="responses",requires_openai_auth=false,request_max_retries=0,stream_max_retries=0}`}
		args = append(args[:len(args)-1], append(extra, args[len(args)-1])...)
		return (sandbox.Policy{Home: realHome, Write: []string{root}, Read: []string{filepath.Dir(bin), filepath.Dir(os.Args[0])}}).Command(ctx, bin, args...)
	}}
	s, err := (Codex{Bin: bin}).Execute(ctx, "probe MCP", opts)
	if err != nil {
		t.Fatal(err)
	}
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case result := <-s.Done:
			if _, err := os.Stat(probes[0]); err != nil {
				t.Fatalf("Codex exited before MCP probe: %v", result.Err)
			}
			assertMCPCacheProbe(t, probes[0], cache)
			return
		case <-ticker.C:
			if _, err := os.Stat(probes[0]); err == nil {
				cancel()
				<-s.Done
				assertMCPCacheProbe(t, probes[0], cache)
				return
			}
		case <-ctx.Done():
			<-s.Done
			t.Fatal("Codex did not start MCP probe before timeout")
		}
	}
}
