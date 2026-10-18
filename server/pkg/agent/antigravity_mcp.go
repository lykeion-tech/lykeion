package agent

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
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
	config := filepath.Join(dir, ".agents")
	st, err := os.Lstat(config)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return nil
	}
	entries, err := os.ReadDir(config)
	if err != nil {
		return err
	}
	if len(entries) != 1 || entries[0].Name() != "mcp_config.json" {
		return nil
	}
	file := filepath.Join(config, "mcp_config.json")
	st, err = os.Lstat(file)
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() {
		return nil
	}
	if err = os.Remove(file); err != nil {
		return err
	}
	return os.Remove(config)
}
