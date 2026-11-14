package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNamingSessionDeniesToolsAndReusesOnlyItsOwnConversation(t *testing.T) {
	args := filepath.Join(t.TempDir(), "args")
	bin := script(t, `printf '%s\n' "$@" > `+args+`
i=0
while IFS= read -r prompt; do
 i=$((i+1))
 echo '{"type":"assistant","message":{"content":[{"type":"text","text":"1. Check model residuals"}]}}'
 echo "{\"type\":\"result\",\"subtype\":\"success\",\"session_id\":\"naming-only\",\"modelUsage\":{\"m\":{\"inputTokens\":$i,\"outputTokens\":$i,\"costUSD\":0.01}}}"
done`)
	n, err := OpenNamingSession(context.Background(), "claude", bin, t.TempDir(), "m", testSandbox)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		got, err := n.Ask(context.Background(), "Name the step")
		if err != nil || got != "1. Check model residuals" {
			t.Fatalf("answer %q: %v", got, err)
		}
	}
	result := n.Close()
	if len(result.Usage) != 1 || result.Usage[0].InputTokens != 2 {
		t.Fatalf("cumulative usage counted more than once: %+v", result)
	}
	data, _ := os.ReadFile(args)
	argv := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	has := func(flag, value string) bool {
		for i := range argv {
			if argv[i] == flag && i+1 < len(argv) && argv[i+1] == value {
				return true
			}
		}
		return false
	}
	if !has("--tools", "") || !has("--setting-sources", "") || !has("--mcp-config", `{"mcpServers":{}}`) || !strings.Contains(string(data), "--strict-mcp-config") || !strings.Contains(string(data), "--disable-slash-commands") {
		t.Fatalf("naming capabilities: %q", argv)
	}
	if strings.Contains(string(data), "--resume") || strings.Contains(string(data), "--continue") {
		t.Fatalf("research continuity leaked: %q", argv)
	}
}

func TestNamingCancellationReleasesScratchAndNeverAnswersAnotherBatch(t *testing.T) {
	bin := script(t, `while IFS= read -r prompt; do sleep 10; done`)
	dir := t.TempDir()
	n, err := OpenNamingSession(context.Background(), "claude", bin, dir, "m", testSandbox)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := n.Ask(ctx, "name"); err == nil {
		t.Fatal("hung answer succeeded")
	}
	if _, err := n.Ask(context.Background(), "next batch"); err == nil {
		t.Fatal("retired session answered another batch")
	}
	n.Close()
	lock, err := ReserveScratch(dir)
	if err != nil {
		t.Fatalf("naming retained scratch: %v", err)
	}
	lock.Release()
	if _, err := os.Stat(filepath.Join(dir, ".cli-home", claudeCredFile)); !os.IsNotExist(err) {
		t.Fatalf("credential retained: %v", err)
	}
}

func TestNamingRefusesUnverifiedHarnessAndToolRequests(t *testing.T) {
	if _, err := OpenNamingSession(context.Background(), "codex", "does-not-exist", t.TempDir(), "", testSandbox); err == nil {
		t.Fatal("unverified tool denial accepted")
	}
	bin := script(t, `read prompt
echo '{"type":"assistant","message":{"content":[{"type":"tool_use","id":"bad","name":"Read","input":{"file_path":"secret"}}]}}'
`)
	n, err := OpenNamingSession(context.Background(), "claude", bin, t.TempDir(), "", testSandbox)
	if err != nil {
		t.Fatal(err)
	}
	defer n.Close()
	if _, err := n.Ask(context.Background(), "name"); err == nil {
		t.Fatal("tool request accepted in naming session")
	}
}

func TestGuestNamingUsesGuestConfigAndCredentialEnvironment(t *testing.T) {
	t.Setenv("CUSTOM_PERSONAL_VALUE", "owner-ambient-fixture")
	guest, cache := t.TempDir(), t.TempDir()
	bin := script(t, `while IFS= read -r prompt; do
 [ "$CLAUDE_CODE_OAUTH_TOKEN" = "guest-access-fixture" ] || exit 51
 [ -z "$CUSTOM_PERSONAL_VALUE" ] || exit 52
 printf '{"type":"assistant","message":{"content":[{"type":"text","text":"%s|%s"}]}}\n' "$CLAUDE_CONFIG_DIR" "$npm_config_cache"
 echo '{"type":"result","subtype":"success","session_id":"naming-only","modelUsage":{"m":{"inputTokens":1,"outputTokens":1}}}'
done`)
	n, err := OpenNamingSession(t.Context(), "claude", bin, t.TempDir(), "m", testSandbox, ExecOptions{Guest: true, ConfigHome: guest, CacheDir: cache, Env: []string{"CLAUDE_CODE_OAUTH_TOKEN=guest-access-fixture"}})
	if err != nil {
		t.Fatal(err)
	}
	defer n.Close()
	got, err := n.Ask(t.Context(), "name")
	if err != nil || got != guest+"|"+filepath.Join(cache, "npm") {
		t.Fatalf("naming guest environment: %q %v", got, err)
	}
}
