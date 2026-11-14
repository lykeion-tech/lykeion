package agent

import (
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// codexPersonalMCPOff turns off every MCP server in the account's config.toml.
// A study run reaches tools only through Lykeion's own servers; the person's
// would reach the web and private services past the sources server. Plugin
// servers are off with the plugins feature.
func codexPersonalMCPOff(personal codexConfig) (string, error) {
	if len(personal.MCPServers) == 0 {
		return "", nil
	}
	servers := map[string]any{}
	for name := range personal.MCPServers {
		servers[name] = map[string]bool{"enabled": false}
	}
	// Inline table keys preserve literal dots and quotes in server names.
	// Codex splits dotted override paths without interpreting quoted keys.
	// Its config-layer merge keeps the personal fields, and their secrets, out of argv.
	var out strings.Builder
	err := toml.NewEncoder(&out).SetTablesInline(true).Encode(map[string]any{"mcp_servers": servers})
	return strings.TrimSpace(out.String()), err
}
