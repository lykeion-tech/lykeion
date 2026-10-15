package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
)

const claudeSnapshotLimit = 1 << 20

type claudeUsageSnapshot struct {
	Version int                     `json:"version"`
	Session string                  `json:"session"`
	Models  map[string]claudeTokens `json:"models"`
}

type claudeAccounting struct {
	path     string
	baseline *claudeUsageSnapshot
	current  *claudeUsageSnapshot
}

// Only the last clean, authoritative snapshot from this scratch is trusted.
// Consume it durably before launch: a crash or interrupted attempt must never
// let the next attempt subtract an older baseline and charge intervening work.
func beginClaudeUsage(home, resume string) (*claudeAccounting, error) {
	a := &claudeAccounting{path: filepath.Join(home, ".lyk-usage.json")}
	if resume != "" {
		b, err := readClaudeSnapshot(a.path)
		if err != nil {
			return nil, fmt.Errorf("Claude resume requires a trustworthy usage baseline: %w", err)
		}
		if b.Session != resume {
			return nil, errors.New("Claude resume session does not match its usage baseline")
		}
		a.baseline = b
	}
	if err := os.Remove(a.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("invalidate Claude usage baseline: %w", err)
	}
	if err := syncUsageDir(home); err != nil {
		return nil, fmt.Errorf("invalidate Claude usage baseline: %w", err)
	}
	return a, nil
}
func readClaudeSnapshot(path string) (*claudeUsageSnapshot, error) {
	st, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() || st.Mode().Perm() != 0600 || st.Size() > claudeSnapshotLimit {
		return nil, errors.New("invalid usage baseline file")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, claudeSnapshotLimit+1))
	if err != nil {
		return nil, err
	}
	if len(data) > claudeSnapshotLimit {
		return nil, errors.New("usage baseline exceeds size limit")
	}
	var b claudeUsageSnapshot
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, err
	}
	if b.Version != 1 || b.Session == "" || len(b.Models) == 0 {
		return nil, errors.New("incomplete usage baseline")
	}
	if err := validateClaudeModels(b.Models); err != nil {
		return nil, err
	}
	return &b, nil
}
func validateClaudeModels(models map[string]claudeTokens) error {
	for model, u := range models {
		if strings.TrimSpace(model) == "" || u.Input < 0 || u.Output < 0 || u.CacheRead < 0 || u.CacheWrite < 0 {
			return errors.New("invalid Claude model usage counters")
		}
		if u.Cost != nil && (math.IsNaN(*u.Cost) || math.IsInf(*u.Cost, 0) || *u.Cost < 0) {
			return errors.New("invalid Claude model usage cost")
		}
	}
	return nil
}

func (a *claudeAccounting) normalize(res *Result, models map[string]claudeTokens) error {
	a.current = nil
	if err := validateClaudeModels(models); err != nil {
		return err
	}
	if a.baseline != nil {
		if res.SessionID != a.baseline.Session || len(models) == 0 {
			return errors.New("Claude resumed result has no matching authoritative model usage baseline")
		}
		// Port the all-model reset rule: subtract token counts only when every
		// previous model and every token bucket is still included in this snapshot.
		cumulative := claudeIncludesBaseline(models, a.baseline.Models)
		for i := range res.Usage {
			u := &res.Usage[i]
			base, known := a.baseline.Models[u.Model]
			if !known {
				continue
			}
			if cumulative {
				u.InputTokens -= base.Input
				u.OutputTokens -= base.Output
				u.CacheReadTokens -= base.CacheRead
				u.CacheWriteTokens -= base.CacheWrite
			}
			if u.CostUSD != nil {
				if base.Cost == nil {
					return errors.New("Claude resumed cost has no authoritative cost baseline")
				}
				// Cost can reset independently of token buckets. A lower counter is
				// already per-run; a cumulative counter contains the previous spend.
				cost := *u.CostUSD
				if cost >= *base.Cost {
					cost -= *base.Cost
				}
				u.CostUSD = &cost
			}
		}
	}
	if res.SessionID != "" && len(models) > 0 {
		a.current = &claudeUsageSnapshot{Version: 1, Session: res.SessionID, Models: models}
	}
	return nil
}
func claudeIncludesBaseline(current, baseline map[string]claudeTokens) bool {
	for model, b := range baseline {
		u, ok := current[model]
		if !ok || u.Input < b.Input || u.Output < b.Output || u.CacheRead < b.CacheRead || u.CacheWrite < b.CacheWrite {
			return false
		}
	}
	return true
}

func (a *claudeAccounting) finish(res *Result) {
	if res.Err != nil || a.current == nil {
		return
	}
	data, err := json.Marshal(a.current)
	if err == nil && len(data) > claudeSnapshotLimit {
		err = errors.New("Claude usage baseline exceeds size limit")
	}
	if err == nil {
		err = writeClaudeSnapshot(a.path, data)
	}
	if err == nil {
		res.ClaudeSnapshot = data
	}
	if err != nil {
		res.Err = fmt.Errorf("persist Claude usage baseline: %w", err)
		res.Reason = ReasonAgentError
	}
}
func writeClaudeSnapshot(path string, data []byte) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".lyk-usage-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(name, path); err != nil {
		return err
	}
	if err = syncUsageDir(dir); err != nil {
		os.Remove(path)
		return err
	}
	return nil
}
func syncUsageDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	err = f.Sync()
	closeErr := f.Close()
	if err != nil {
		return err
	}
	return closeErr
}

// ClaudeResumable reports whether a private folder holds the usage baseline of
// session, which a resumed Claude run is charged against.
func ClaudeResumable(dir, session string) bool {
	b, err := readClaudeSnapshot(filepath.Join(dir, ".cli-home", ".lyk-usage.json"))
	return err == nil && b.Session == session
}
