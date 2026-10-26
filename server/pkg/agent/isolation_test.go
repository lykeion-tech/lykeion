package agent

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestReservedStudyEnvKey(t *testing.T) {
	for _, name := range []string{
		"NODE_OPTIONS", "NODE_PATH", "CODEX_CONFIG", "CODEX_HOME", "CODEX_THREAD_ID",
		"CLAUDE_CODE_SESSION_ID", "CLAUDECODE_TEST", "LYKEION_TEST", "BASH_ENV", "ZDOTDIR",
		"PATH", "LD_PRELOAD", "LYK_DOOR", "VIRTUAL_ENV_EXTRA", "PYTHONHOME_EXTRA",
		"OPENAI_API_KEY", "DB_PASSWORD_X",
		"NODE_EXTRA_CA_CERTS", "NODE_TLS_REJECT_UNAUTHORIZED", "NODE_USE_SYSTEM_CA", "NODE_USE_ENV_PROXY",
		"OPENSSL_CONF", "SSL_CERT_FILE", "SSL_CERT_DIR",
		"ANTHROPIC_BASE_URL", "ANTHROPIC_AWS_BASE_URL", "ANTHROPIC_BEDROCK_BASE_URL",
		"ANTHROPIC_BEDROCK_MANTLE_BASE_URL", "ANTHROPIC_VERTEX_BASE_URL",
		"ANTHROPIC_FOUNDRY_BASE_URL", "ANTHROPIC_FOUNDRY_RESOURCE", "ANTHROPIC_CUSTOM_HEADERS",
		"ANTHROPIC_PROFILE", "ANTHROPIC_ORGANIZATION_ID", "ANTHROPIC_FEDERATION_RULE_ID",
		"CLAUDE_CODE_USE_ANTHROPIC_AWS", "CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_FOUNDRY",
		"CLAUDE_CODE_USE_MANTLE", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_CERT_STORE",
		"CLAUDE_CODE_CLIENT_CERT", "CLAUDE_CODE_CLIENT_KEY", "CLAUDE_CODE_CLIENT_KEY_PASSPHRASE",
		"CLAUDE_CODE_SHELL", "CLAUDE_CODE_SHELL_PREFIX", "CLAUDE_ENV_FILE",
		"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "CODEX_CA_CERTIFICATE",
		"OPENAI_BASE_URL", "OPENAI_FEDERATION_RULE_ID", "OPENAI_IDENTITY_TOKEN_FILE", "OPENAI_ORGANIZATION",
		"OPENAI_PROJECT", "CODEX_OSS_BASE_URL", "CODEX_OSS_PORT",
	} {
		if !ReservedStudyEnvKey(name) {
			t.Errorf("%s was not reserved", name)
		}
	}
	for _, name := range []string{"DATA_DIR", "PYTHONPATH", "NODE_COUNT"} {
		if ReservedStudyEnvKey(name) {
			t.Errorf("%s was unexpectedly reserved", name)
		}
	}
}

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
