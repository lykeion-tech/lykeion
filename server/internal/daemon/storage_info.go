package daemon

import (
	"context"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"lykeion/server/internal/storageinfo"
)

func measureStorage(ctx context.Context, cfg Config) storageinfo.Snapshot {
	out := storageinfo.Snapshot{MeasuredAt: time.Now().UTC(), Categories: []storageinfo.Category{{Key: "workspaces", Path: cfg.Home}, {Key: "environments", Path: cfg.EnvDir}, {Key: "state", Path: cfg.StateDir}}, Filesystems: []storageinfo.Filesystem{}}
	roots := make([]string, 3)
	canonical := make([]string, 3)
	for i, c := range out.Categories {
		if c.Path != "" {
			roots[i], _ = filepath.Abs(c.Path)
			// Resolve only configured roots, after rejecting a symlink at the
			// root itself. Parent aliases must share the same overlap identity.
			if info, err := os.Lstat(roots[i]); err == nil && info.IsDir() {
				canonical[i], _ = filepath.EvalSymlinks(roots[i])
			}
		}
	}
	filesystems := map[string]int{}
	for i := range out.Categories {
		c := &out.Categories[i]
		c.Path = roots[i]
		if ctx.Err() != nil {
			c.Error = "measurement cancelled"
			continue
		}
		if c.Path == "" {
			c.Error = "location not configured"
			continue
		}
		info, err := os.Lstat(c.Path)
		if err != nil || !info.IsDir() || canonical[i] == "" {
			c.Error = "location unavailable or not a directory"
			continue
		}
		root, err := os.OpenRoot(c.Path)
		if err != nil {
			c.Error = "location unreadable"
			continue
		}
		opened, statErr := root.Stat(".")
		if statErr != nil || !os.SameFile(info, opened) {
			root.Close()
			c.Error = "location changed during measurement"
			continue
		}
		c.Complete = true
		err = fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if walkErr != nil {
				c.Complete = false
				c.Error = "some entries could not be measured"
				return nil
			}
			full := filepath.Join(c.Path, filepath.FromSlash(name))
			identity := filepath.Join(canonical[i], filepath.FromSlash(name))
			for j, other := range canonical {
				if j == i || other == "" {
					continue
				}
				if (other == canonical[i] && j < i) || (other != canonical[i] && insideStorage(canonical[i], other) && insideStorage(other, identity)) {
					if entry.IsDir() {
						return fs.SkipDir
					}
					return nil
				}
			}
			stat, err := root.Lstat(name)
			if err != nil {
				c.Complete = false
				c.Error = "some entries could not be measured"
				return nil
			}
			if stat.Mode()&os.ModeSymlink != 0 {
				if entry.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if stat.IsDir() {
				id, available, err := storageFilesystem(root, name, stat)
				if err != nil {
					c.Complete = false
					c.Error = "filesystem capacity unavailable"
					return nil
				}
				if name == "." {
					c.FilesystemID = id
				}
				if index, ok := filesystems[id]; ok {
					if name == "." {
						out.Filesystems[index].Paths = append(out.Filesystems[index].Paths, c.Path)
					}
				} else {
					filesystems[id] = len(out.Filesystems)
					out.Filesystems = append(out.Filesystems, storageinfo.Filesystem{ID: id, AvailableBytes: available, Paths: []string{full}})
				}
			} else if stat.Mode().IsRegular() {
				c.Bytes += stat.Size()
			}
			return nil
		})
		root.Close()
		if err != nil {
			c.Complete = false
			c.Error = "measurement cancelled"
		}
	}
	return out
}
func insideStorage(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && (rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel))
}

func (d *daemon) storageLoop(ctx context.Context) {
	// Reporting follows registration independently of heartbeats and claiming.
	snapshot := measureStorage(ctx, d.cfg)
	sent := map[uuid.UUID]bool{}
	report := func() {
		for _, ref := range d.references() {
			if sent[ref.WorkspaceID] || ctx.Err() != nil {
				continue
			}
			in := storageinfo.Report{WorkspaceID: ref.WorkspaceID, RuntimeID: ref.RuntimeID, DaemonID: d.id, Snapshot: snapshot}
			if err := d.api.Do(ctx, "POST", "/daemon/storage", in, nil); err != nil {
				slog.Debug("storage report pending", "err", err)
				continue
			}
			sent[ref.WorkspaceID] = true
		}
	}
	report()
	refresh := time.NewTicker(10 * time.Minute)
	defer refresh.Stop()
	registration := time.NewTicker(15 * time.Second)
	defer registration.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-refresh.C:
			snapshot = measureStorage(ctx, d.cfg)
			sent = map[uuid.UUID]bool{}
			report()
		case <-registration.C:
			report()
		}
	}
}
