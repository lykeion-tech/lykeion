// Package storageinfo describes private machine storage measurements.
package storageinfo

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

type Category struct {
	Key          string `json:"key"`
	Path         string `json:"path"`
	Bytes        int64  `json:"bytes"`
	Complete     bool   `json:"complete"`
	Error        string `json:"error,omitempty"`
	FilesystemID string `json:"filesystem_id,omitempty"`
}
type Filesystem struct {
	ID             string   `json:"id"`
	AvailableBytes int64    `json:"available_bytes"`
	Paths          []string `json:"paths"`
}
type Snapshot struct {
	MeasuredAt  time.Time    `json:"measured_at"`
	ReceivedAt  time.Time    `json:"received_at"`
	Categories  []Category   `json:"categories"`
	Filesystems []Filesystem `json:"filesystems"`
}
type Report struct {
	WorkspaceID uuid.UUID `json:"workspace_id"`
	RuntimeID   uuid.UUID `json:"runtime_id"`
	DaemonID    string    `json:"daemon_id"`
	Snapshot    Snapshot  `json:"snapshot"`
}
type Machine struct {
	DaemonID   string    `json:"daemon_id"`
	Name       string    `json:"name"`
	Online     bool      `json:"online"`
	LastSeenAt time.Time `json:"last_seen_at"`
	Snapshot   *Snapshot `json:"snapshot"`
}
type Response struct {
	Machines []Machine `json:"machines"`
}

func (s Snapshot) Validate() error {
	bad := errors.New("invalid storage snapshot")
	if s.MeasuredAt.IsZero() || s.MeasuredAt.After(time.Now().Add(time.Minute)) || len(s.Categories) != 3 {
		return bad
	}
	seen := map[string]bool{}
	fsIDs := map[string]bool{}
	for _, f := range s.Filesystems {
		if f.ID == "" || len(f.ID) > 128 || fsIDs[f.ID] || f.AvailableBytes < 0 || len(f.Paths) == 0 {
			return bad
		}
		fsIDs[f.ID] = true
		for _, p := range f.Paths {
			if !validPath(p) {
				return bad
			}
		}
	}
	for _, c := range s.Categories {
		if (c.Key != "workspaces" && c.Key != "environments" && c.Key != "state") || seen[c.Key] || c.Bytes < 0 || len(c.Error) > 256 || c.Complete && c.Error != "" || !c.Complete && c.Error == "" {
			return bad
		}
		if c.Path != "" && !validPath(c.Path) {
			return bad
		}
		if c.Complete && c.Path == "" {
			return bad
		}
		if c.FilesystemID != "" && !fsIDs[c.FilesystemID] {
			return bad
		}
		seen[c.Key] = true
	}
	return nil
}
func validPath(p string) bool {
	return len(p) <= 4096 && filepath.IsAbs(p) && !strings.ContainsRune(p, 0)
}

// UnmarshalJSON requires measured counts explicitly, so a missing or null
// measurement cannot be accepted as a known zero.
func (c *Category) UnmarshalJSON(data []byte) error {
	type plain Category
	var value plain
	if err := decodeMeasurement(data, &value, "bytes", "complete"); err != nil {
		return err
	}
	*c = Category(value)
	return nil
}
func (f *Filesystem) UnmarshalJSON(data []byte) error {
	type plain Filesystem
	var value plain
	if err := decodeMeasurement(data, &value, "available_bytes"); err != nil {
		return err
	}
	*f = Filesystem(value)
	return nil
}
func decodeMeasurement(data []byte, out any, required ...string) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for _, key := range required {
		value, ok := fields[key]
		if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return errors.New("explicit measurement required")
		}
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	return dec.Decode(out)
}
