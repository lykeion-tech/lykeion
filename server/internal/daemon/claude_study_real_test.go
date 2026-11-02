package daemon

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"lykeion/server/pkg/agent"
)

// A study run starts and resumes against an offline provider while the
// account's instructions, customizations, plugin, hooks and MCP servers exist:
// none of them reach the model or run, but the account's settings still load.
func TestRealClaudeStudyRunResumesWithoutPersonalContext(t *testing.T) {
	if os.Getenv("LYK_TEST_SKILLS_REAL") != "1" {
		t.Skip("opt-in installed Claude offline transport")
	}
	bin, err := exec.LookPath("claude")
	must(t, err)
	root := t.TempDir()
	run := filepath.Join(root, "study")
	must(t, os.MkdirAll(run, 0700))
	account := filepath.Join(cliAccountFixture(t), ".claude")
	const personal = "USER-PERSONAL-CONTEXT-5be1"
	const claudeMd = "USER-CLAUDE-MD-71c0"
	const pluginSkill = "USER-PLUGIN-SKILL-2e9d"
	const outsideSkill = "USER-OUTSIDE-PLUGIN-SKILL-8a4f"
	const outputStyle = "USER-OUTPUT-STYLE-c37b"
	for _, dir := range []string{"skills/mine", "plugins/cache", "agents", "commands", "rules", "output-styles"} {
		must(t, os.MkdirAll(filepath.Join(account, dir), 0700))
	}
	must(t, os.WriteFile(filepath.Join(account, "CLAUDE.md"), []byte(claudeMd+"\n"), 0600))
	for _, file := range []string{"agents/mine.md", "commands/mine.md", "rules/mine.md", "history.jsonl"} {
		must(t, os.WriteFile(filepath.Join(account, file), []byte(personal+"\n"), 0600))
	}
	must(t, os.WriteFile(filepath.Join(account, "output-styles/marker.md"), []byte("---\nname: Marker Style\ndescription: fixture style\n---\n"+outputStyle+"\n"), 0600))
	must(t, os.WriteFile(filepath.Join(account, "skills/mine/SKILL.md"), []byte("---\nname: mine\ndescription: "+personal+"\n---\n"+personal+"\n"), 0600))
	mcpMarker := filepath.Join(run, "personal-mcp-started")
	hookMarker := filepath.Join(run, "personal-hook-ran")
	must(t, os.WriteFile(filepath.Join(account, ".claude.json"), []byte(`{"mcpServers":{"personal":{"type":"stdio","command":"/usr/bin/touch","args":["`+mcpMarker+`"]}}}`), 0600))
	// An installed plugin from a local marketplace, with a skill, and one
	// from a marketplace outside the account that only the settings declare.
	market := filepath.Join(account, "plugins", "marketplaces", "fixture")
	plugin := filepath.Join(market, "mine")
	outside := filepath.Join(filepath.Dir(account), "market")
	for dir, files := range map[string]map[string]string{
		filepath.Join(outside, ".claude-plugin"):                  {"marketplace.json": `{"name":"outside","owner":{"name":"fixture"},"plugins":[{"name":"ext","source":"./ext"}]}`},
		filepath.Join(outside, "ext", ".claude-plugin"):           {"plugin.json": `{"name":"ext","version":"1.0.0"}`},
		filepath.Join(outside, "ext", "skills", "outside-method"): {"SKILL.md": "---\nname: outside-method\ndescription: " + outsideSkill + "\n---\n" + outsideSkill + "\n"},
		filepath.Join(market, ".claude-plugin"):                   {"marketplace.json": `{"name":"fixture","owner":{"name":"fixture"},"plugins":[{"name":"mine","source":"./mine"}]}`},
		filepath.Join(plugin, ".claude-plugin"):                   {"plugin.json": `{"name":"mine","version":"1.0.0"}`},
		filepath.Join(plugin, "skills", "plugin-method"):          {"SKILL.md": "---\nname: plugin-method\ndescription: " + pluginSkill + "\n---\n" + pluginSkill + "\n"},
		filepath.Join(account, "plugins"): {
			"known_marketplaces.json": `{"fixture":{"source":{"source":"directory","path":"` + market + `"},"installLocation":"` + market + `","lastUpdated":"2026-01-01T00:00:00.000Z"}}`,
			"installed_plugins.json":  `{"version":2,"plugins":{"mine@fixture":[{"scope":"user","installPath":"` + plugin + `","version":"1.0.0","installedAt":"2026-01-01T00:00:00.000Z","lastUpdated":"2026-01-01T00:00:00.000Z"}]}}`,
		},
	} {
		must(t, os.MkdirAll(dir, 0700))
		for name, body := range files {
			must(t, os.WriteFile(filepath.Join(dir, name), []byte(body), 0600))
		}
	}
	// The settings env adds a header, so a request shows the settings loaded.
	must(t, os.WriteFile(filepath.Join(account, "settings.json"), []byte(`{"env":{"ANTHROPIC_CUSTOM_HEADERS":"X-Lyk-Settings: loaded"},"outputStyle":"Marker Style","extraKnownMarketplaces":{"outside":{"source":{"source":"directory","path":"`+outside+`"}}},"enabledPlugins":{"mine@fixture":true,"ext@outside":true},"hooks":{"UserPromptSubmit":[{"hooks":[{"type":"command","command":"/usr/bin/touch `+hookMarker+`"}]}]}}`), 0600))
	const prompt = "Run the study isolation fixture."
	var mu sync.Mutex
	var requests [][]byte
	var settingsHeader []bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
		if err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/count_tokens") {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"input_tokens":1}`)
			return
		}
		if !strings.HasSuffix(r.URL.Path, "/messages") {
			w.WriteHeader(404)
			return
		}
		if strings.Contains(string(body), prompt) {
			mu.Lock()
			requests = append(requests, slices.Clone(body))
			settingsHeader = append(settingsHeader, r.Header.Get("X-Lyk-Settings") == "loaded")
			mu.Unlock()
		}
		claudeFixtureResponse(w, "", "", nil)
	}))
	defer upstream.Close()
	d := &daemon{cfg: Config{Home: filepath.Join(root, "daemon")}}
	var resume string
	for turn := 0; turn < 2; turn++ {
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		opts := agent.ExecOptions{Dir: run, ConfigHome: account, Resume: resume, Env: []string{"ANTHROPIC_API_KEY=offline-fixture", "ANTHROPIC_BASE_URL=" + upstream.URL, "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_TELEMETRY=1"}, Sandbox: d.cliSandbox(run, nil, false)}
		sess, err := (agent.Claude{Bin: bin}).Execute(ctx, prompt, opts)
		must(t, err)
		var logs strings.Builder
		for m := range sess.Messages {
			logs.Write(m.Content)
		}
		result := <-sess.Done
		cancel()
		mu.Lock()
		captured := slices.Clone(requests)
		withSettings := slices.Clone(settingsHeader)
		mu.Unlock()
		if result.Err != nil || len(captured) != turn+1 {
			t.Fatalf("turn %d: requests=%d result=%+v logs=%s", turn, len(captured), result, logs.String())
		}
		last := string(captured[turn])
		for name, marker := range map[string]string{"CLAUDE.md": claudeMd, "plugin skill": pluginSkill, "outside plugin skill": outsideSkill, "output style": outputStyle, "personal context": personal} {
			if strings.Contains(last, marker) {
				t.Errorf("turn %d: the account's %s reached the model", turn, name)
			}
		}
		if !withSettings[turn] {
			t.Errorf("turn %d: the account's settings env did not load", turn)
		}
		// A resumed turn carries the first turn's exchange.
		if turn == 1 && strings.Count(last, prompt) < 2 {
			t.Fatal("turn 1 did not resume the first turn's session")
		}
		for _, marker := range []string{mcpMarker, hookMarker} {
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Errorf("turn %d: %s", turn, filepath.Base(marker))
			}
		}
		if t.Failed() {
			t.FailNow()
		}
		if result.SessionID == "" {
			t.Fatal("no persisted session for resume")
		}
		resume = result.SessionID
		t.Logf("turn %d: offline request without personal context; resumed=%v", turn, turn == 1)
	}
}
