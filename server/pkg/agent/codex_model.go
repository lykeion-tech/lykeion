package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/pelletier/go-toml/v2"
)

const codexCatalogTimeout = 15 * time.Second
const codexCatalogLimit = 4 << 20

// codexModel chooses the run's model: an explicit one, else the account's
// configured default when the account can see it, else the first model in
// Codex's own catalog. The live catalog is account-visible; --bundled is the
// binary's offline snapshot.
func codexModel(ctx context.Context, bin string, o ExecOptions, configured string) (string, error) {
	var models []string
	for _, args := range [][]string{{"debug", "models"}, {"debug", "models", "--bundled"}} {
		models = codexCatalog(ctx, bin, o, args...)
		if len(models) > 0 {
			break
		}
	}
	if configured != "" && (len(models) == 0 || slices.Contains(models, configured)) {
		return configured, nil
	}
	if len(models) > 0 {
		return models[0], nil
	}
	return "", errors.New("cannot determine the Codex model: the catalog is unavailable and no default is configured")
}

// codexCatalog lists visible model slugs in catalog order, or nil on failure.
func codexCatalog(ctx context.Context, bin string, o ExecOptions, args ...string) []string {
	ctx, cancel := context.WithTimeout(ctx, codexCatalogTimeout)
	defer cancel()
	cmd, err := command(ctx, bin, "", o, args...)
	if err != nil {
		return nil
	}
	var slugs []string
	var catalogJSON []byte
	parse := func(line []byte, _ emitMessage, _ *Result) (bool, error) {
		if len(catalogJSON)+len(line)+1 > codexCatalogLimit {
			return false, errors.New("Codex catalog exceeds size limit")
		}
		catalogJSON = append(catalogJSON, line...)
		catalogJSON = append(catalogJSON, '\n')
		if !json.Valid(catalogJSON) {
			return false, nil
		}
		var catalog struct {
			Models []struct{ Slug, Visibility string } `json:"models"`
		}
		if err := json.Unmarshal(catalogJSON, &catalog); err != nil {
			return false, err
		}
		for _, m := range catalog.Models {
			if m.Slug != "" && len(m.Slug) <= 200 && m.Visibility != "hide" {
				slugs = append(slugs, m.Slug)
			}
		}
		return true, nil
	}
	session, err := run(ctx, cmd, func(bool) {}, parse, nil, func(Message) error { return nil })
	if err != nil {
		return nil
	}
	result := <-session.Done
	if result.Err != nil {
		return nil
	}
	return slugs
}

// codexConfig is the part of the account's config.toml Lykeion reads.
type codexConfig struct {
	Model      string                    `toml:"model"`
	MCPServers map[string]map[string]any `toml:"mcp_servers"`
	// Unreadable marks a config.toml that exists but cannot be read here,
	// so what it would turn on is unknown.
	Unreadable bool `toml:"-"`
}

// readCodexConfig parses the account's config.toml; a missing file reads as
// empty, and one that cannot be read or parsed as empty and Unreadable.
func readCodexConfig(home string) codexConfig {
	var cfg codexConfig
	if home == "" {
		return cfg
	}
	b, err := readLimited(filepath.Join(home, "config.toml"), codexCatalogLimit)
	if errors.Is(err, fs.ErrNotExist) {
		return codexConfig{}
	}
	if err != nil || toml.Unmarshal(b, &cfg) != nil || len(cfg.Model) > 200 {
		return codexConfig{Unreadable: true}
	}
	return cfg
}
func readLimited(p string, limit int64) ([]byte, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err == nil && int64(len(b)) > limit {
		err = errors.New("file too large")
	}
	return b, err
}
