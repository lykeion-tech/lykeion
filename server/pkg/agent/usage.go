// Package agent runs one agent CLI headlessly and turns its output into one
// message stream, whatever the harness.
package agent

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
}

// MCPServer is a tool server the server sent for an agent run, besides sources.
type MCPServer struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}
