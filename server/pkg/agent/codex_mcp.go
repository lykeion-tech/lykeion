package agent

import (
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// codexMCPCacheOverride redirects package caches for account-config stdio
// servers. Codex filters the parent environment before starting those children.
// Plugin, project and runtime-provided servers are outside this account map.
func codexMCPCacheOverride(personal codexConfig, cacheDir string) (string, error) {
	if cacheDir == "" {
		return "", nil
	}
	servers := map[string]any{}
	for name, server := range personal.MCPServers {
		command, _ := server["command"].(string)
		if command == "" {
			continue
		}
		servers[name] = map[string]any{"env": map[string]string{
			"npm_config_cache":      filepath.Join(cacheDir, "npm"),
			"UV_CACHE_DIR":          filepath.Join(cacheDir, "uv"),
			"UV_PYTHON_INSTALL_DIR": filepath.Join(cacheDir, "uv-python"),
			"UV_TOOL_DIR":           filepath.Join(cacheDir, "uv-tools"),
		}}
	}
	if len(servers) == 0 {
		return "", nil
	}
	// Inline table keys preserve literal dots and quotes in server names.
	// Codex splits dotted override paths without interpreting quoted keys.
	// Its config-layer merge keeps unrelated personal fields out of argv.
	var out strings.Builder
	err := toml.NewEncoder(&out).SetTablesInline(true).Encode(map[string]any{"mcp_servers": servers})
	return strings.TrimSpace(out.String()), err
}
