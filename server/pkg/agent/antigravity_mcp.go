package agent

import (
	"encoding/json"
	"path/filepath"
)

// writeAntigravityMCP gives a private workspace only the recorded tool door.
// prepareScratch has checked discovery paths while holding the scratch lock.
func writeAntigravityMCP(o ExecOptions) error {
	if o.DoorURL == "" {
		return nil
	}
	config := filepath.Join(o.Dir, ".agents")
	if err := privateDir(config); err != nil {
		return err
	}
	body, err := json.Marshal(map[string]any{"mcpServers": map[string]any{
		"lyk_door": map[string]any{"serverUrl": o.DoorURL, "headers": map[string]string{"Authorization": "Bearer " + o.DoorToken}},
	}})
	if err != nil {
		return err
	}
	return privateFile(filepath.Join(config, "mcp_config.json"), body)
}

// removePriorAntigravityConfig clears only the prior adapter file in an owned,
// locked scratch. Unexpected discovery entries remain for the guard to refuse.
func removePriorAntigravityConfig(dir string) error {
	return removePriorMCPConfig(dir, ".agents", "mcp_config.json")
}
