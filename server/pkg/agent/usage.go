// Package agent runs one agent CLI headlessly and turns its output into one
// message stream, whatever the harness.
package agent

import "strings"

// Usage is one model's token use in one agent-run attempt. InputTokens excludes
// cached input, which is counted in CacheReadTokens. CostUSD is nil when the
// CLI did not price the run; the server then prices it.
type Usage struct {
	Model            string   `json:"model"`
	InputTokens      int64    `json:"input_tokens"`
	OutputTokens     int64    `json:"output_tokens"`
	CacheReadTokens  int64    `json:"cache_read_tokens"`
	CacheWriteTokens int64    `json:"cache_write_tokens"`
	CostUSD          *float64 `json:"cost_usd,omitempty"`
	// CostReported is set by the server: true only when the CLI reported the
	// cost itself. A cost priced from the rate table is an estimate.
	CostReported bool `json:"-"`
}

// ServerName is the name a server-sent MCP entry is written under. The prefix
// keeps a server-sent entry from merging into a personal one of the same name,
// which could send that entry's credentials to a URL another member chose.
func ServerName(name string) string {
	if strings.HasPrefix(name, "lyk_") {
		return name
	}
	return "lyk_" + name
}

// MCPServer is a tool server the server sent for an agent run, besides sources.
type MCPServer struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}
