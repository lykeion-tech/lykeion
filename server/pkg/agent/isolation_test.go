package agent

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestTrustedNodeOptionsRemainInChildEnv(t *testing.T) {
	t.Setenv("NODE_OPTIONS", "--no-warnings")
	t.Setenv("HTTPS_PROXY", "http://trusted.invalid:8080")
	t.Setenv("ANTHROPIC_BASE_URL", "http://trusted.invalid")
	child := ChildEnv(ExecOptions{})
	for _, entry := range []string{
		"NODE_OPTIONS=--no-warnings",
		"HTTPS_PROXY=http://trusted.invalid:8080",
		"ANTHROPIC_BASE_URL=http://trusted.invalid",
	} {
		if !slices.Contains(child, entry) {
			t.Errorf("the person's trusted setting %q was removed", entry)
		}
	}
}

func TestGuestChildEnvKeepsOnlyApprovedAmbientSettings(t *testing.T) {
	names := []string{"AWS_SECRET_ACCESS_KEY", "OPENAI_API_KEY", "ANTHROPIC_API_KEY", "GITHUB_TOKEN", "CUSTOM_PERSONAL_VALUE", "SSH_AUTH_SOCK", "NODE_OPTIONS", "HTTP_PROXY", "CLAUDE_CODE_OAUTH_TOKEN"}
	for _, name := range names {
		t.Setenv(name, "ambient-fixture-"+name)
	}
	t.Setenv("LANG", "C.UTF-8")
	dir, config := t.TempDir(), t.TempDir()
	var err error
	dir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	guest := ChildEnv(ExecOptions{Guest: true, Dir: dir, ConfigHome: config, CacheDir: t.TempDir(), Env: []string{"RUN_OPTION=explicit", "LYK_TOKEN=ltk_guest", "LYK_CONFIG=" + filepath.Join(dir, "config.json")}})
	for _, entry := range guest {
		for _, name := range names {
			if strings.HasPrefix(entry, name+"=") {
				t.Fatalf("guest inherited ambient account setting %s", name)
			}
		}
	}
	for _, want := range []string{"LANG=C.UTF-8", "RUN_OPTION=explicit", "LYK_TOKEN=ltk_guest", "HOME=" + config, "CODEX_HOME=" + config, "CLAUDE_CONFIG_DIR=" + config, "LYK_CONFIG=" + filepath.Join(dir, "config.json")} {
		if !slices.Contains(guest, want) {
			t.Errorf("guest lost trusted/private environment %s", want)
		}
	}
	owner := ChildEnv(ExecOptions{ConfigHome: config})
	for _, name := range names {
		if !slices.Contains(owner, name+"=ambient-fixture-"+name) {
			t.Errorf("owner setting changed: %s", name)
		}
	}
}

func TestAristotleLabEnvExcludesProviderKeys(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "ambient")
	t.Setenv("TYPESAFE_API_KEY", "ambient")
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "owner-sign-in")
	env := ChildEnv(ExecOptions{Lab: true, Env: []string{"ANTHROPIC_API_KEY=explicit", "typesafe_api_key=explicit"}})
	for _, v := range env {
		k, _, _ := strings.Cut(v, "=")
		if strings.EqualFold(k, "ANTHROPIC_API_KEY") || strings.EqualFold(k, "TYPESAFE_API_KEY") {
			t.Fatal("provider key inherited")
		}
	}
	if !slices.Contains(env, "CLAUDE_CODE_OAUTH_TOKEN=owner-sign-in") {
		t.Fatal("CLI sign-in missing")
	}
}

func TestLabClaudeDefaultsProviderAndDropsPersonalRoutes(t *testing.T) {
	for _, key := range []string{"ANTHROPIC_BASE_URL", "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "NODE_OPTIONS", "NODE_EXTRA_CA_CERTS", "CLAUDE_CODE_USE_BEDROCK", "OTEL_EXPORTER_OTLP_ENDPOINT", "CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST"} {
		t.Setenv(key, "untrusted")
	}
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "owner-oauth")
	env := ChildEnv(ExecOptions{Lab: true, Env: []string{"ANTHROPIC_BASE_URL=http://evil", "HTTPS_PROXY=http://evil", "LYK_TOKEN=ltk_wrong", "CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST=1", "PATH=/trusted"}})
	var seen = map[string]string{}
	for _, v := range env {
		k, val, _ := strings.Cut(v, "=")
		seen[k] = val
	}
	if seen["ANTHROPIC_BASE_URL"] != "https://api.anthropic.com" || seen["CLAUDE_CODE_OAUTH_TOKEN"] != "owner-oauth" || seen["PATH"] != "/trusted" {
		t.Fatal("missing default provider, account auth or run path")
	}
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "NODE_OPTIONS", "NODE_EXTRA_CA_CERTS", "CLAUDE_CODE_USE_BEDROCK", "OTEL_EXPORTER_OTLP_ENDPOINT", "LYK_TOKEN", "CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST"} {
		if _, ok := seen[key]; ok {
			t.Errorf("inherited %s", key)
		}
	}
}
