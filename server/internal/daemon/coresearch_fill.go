package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	"lykeion/server/internal/coresearch"
	"lykeion/server/internal/coresearch/localfiles"
)

// prepareChatMetadata establishes trusted bind targets before either sandbox.
func prepareChatMetadata(ctx context.Context, dir string) error {
	f, err := localfiles.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	m, err := f.LockManifest(ctx)
	if err != nil {
		return err
	}
	defer m.Close()
	cfg, err := f.Read(".lyk/config.json")
	if err == nil {
		cfg.Close()
	} else if errors.Is(err, os.ErrNotExist) {
		target, e := f.Prepare(".lyk/config.json")
		if e != nil {
			return e
		}
		defer target.Close()
		if e = target.Replace(func(w io.Writer) error { _, e := io.WriteString(w, "{}\n"); return e }); e != nil {
			return e
		}
	} else {
		return err
	}
	return m.Save()
}

type fillChanges struct {
	Discarded []string
	Unsaved   []string
	Deferred  []string
}

func (d *daemon) fill(ctx context.Context, chat, project uuid.UUID) ([]string, error) {
	changes, err := d.fillChanges(ctx, chat, project)
	return changes.Unsaved, err
}
func (d *daemon) fillChanges(ctx context.Context, chat, project uuid.UUID) (fillChanges, error) {
	var changes fillChanges
	d.chatsMu.Lock()
	h := d.chats[chat]
	d.chatsMu.Unlock()
	if h == nil || h.work.ProjectID != project {
		return changes, errors.New("unknown chat project")
	}
	if err := d.checkChat(ctx, h.work); err != nil {
		return changes, err
	}
	f, err := localfiles.Open(h.dir)
	if err != nil {
		return changes, err
	}
	defer f.Close()
	// The shared lock spans the remote snapshot, local read/write and pin update.
	// Ordinary code does not use it; filesMu plus the durable launch guard
	// prevent automatic replacement once such writers may have existed.
	m, err := f.LockManifest(ctx)
	if err != nil {
		return changes, err
	}
	defer m.Close()
	var refs []coresearch.FileRef
	api := d.coreAPI(h.work.WorkspaceID)
	if err = api.Do(ctx, "GET", d.corePath("chats/"+chat.String()+"/files"), nil, &refs); err != nil {
		return changes, err
	}
	for _, ref := range refs {
		name, e := coresearch.CleanPath(ref.Path)
		if e != nil || name != ref.Path {
			return changes, fmt.Errorf("invalid saved path %q", ref.Path)
		}
		digest, e := hex.DecodeString(ref.SHA256)
		if e != nil || len(digest) != sha256.Size || ref.N <= 0 || ref.Size < 0 || ref.Size > 50<<20 {
			return changes, errors.New("invalid saved file reference")
		}
		if ref.DocVersion != nil {
			if e = d.fillLiveFile(ctx, h, f, m, ref, &changes); e != nil {
				return changes, e
			}
			continue
		}
		local, e := fillDigest(f, name)
		if e != nil {
			return changes, e
		}
		pin := m.Entries[name]
		if local != "" && local != ref.SHA256 && (pin.SHA256 == "" || local != pin.SHA256) {
			changes.Unsaved = append(changes.Unsaved, name)
			continue
		}
		if local == ref.SHA256 {
			m.Entries[name] = localfiles.Pin{N: int(ref.N), SHA256: ref.SHA256}
			continue
		}
		h.filesMu.Lock()
		deferred := local != "" && h.writerLaunched
		h.filesMu.Unlock()
		if deferred {
			changes.Deferred = append(changes.Deferred, name)
			continue
		}
		// Download outside the launch gate. Network latency must not suspend
		// a CLI waiting for a cell, nor leave a stale pre-download comparison.
		stage, e := os.CreateTemp("", "lyk-coresearch-fill-")
		if e != nil {
			return changes, e
		}
		e = func() error {
			defer os.Remove(stage.Name())
			defer stage.Close()
			hash := sha256.New()
			bounded := &fillWriter{w: io.MultiWriter(stage, hash), left: ref.Size}
			if e := api.Download(ctx, d.corePath("blobs/"+ref.SHA256), bounded); e != nil {
				return e
			}
			if bounded.left != 0 || hex.EncodeToString(hash.Sum(nil)) != ref.SHA256 {
				return errors.New("saved blob size or SHA256 mismatch")
			}
			if _, e := stage.Seek(0, io.SeekStart); e != nil {
				return e
			}
			h.filesMu.Lock()
			defer h.filesMu.Unlock()
			current, e := fillDigest(f, name)
			if e != nil {
				return e
			}
			if current != local {
				changes.Unsaved = append(changes.Unsaved, name)
				return nil
			}
			if current != "" && h.writerLaunched {
				changes.Deferred = append(changes.Deferred, name)
				return nil
			}
			target, e := f.Prepare(name)
			if e != nil {
				return e
			}
			defer target.Close()
			write := func(w io.Writer) error { _, e := io.Copy(w, stage); return e }
			if current == "" {
				// The final atomic link, unlike rename, cannot replace a file
				// created by an uncooperative writer after the comparison.
				e = target.Create(write)
			} else {
				e = target.Replace(write)
			}
			if errors.Is(e, os.ErrExist) {
				changes.Unsaved = append(changes.Unsaved, name)
				return nil
			}
			if e != nil {
				return e
			}
			m.Entries[name] = localfiles.Pin{N: int(ref.N), SHA256: ref.SHA256}
			return nil
		}()
		if e != nil {
			return changes, e
		}
	}
	if err = m.Save(); err != nil {
		return changes, err
	}
	return changes, nil
}
func fillDigest(f *localfiles.Folder, name string) (string, error) {
	file, err := f.Read(name)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

type fillWriter struct {
	w    io.Writer
	left int64
}

func (w *fillWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.left {
		return 0, errors.New("saved blob exceeds declared size")
	}
	n, e := w.w.Write(p)
	w.left -= int64(n)
	return n, e
}
func (d *daemon) chatFolder(chat uuid.UUID) string {
	return filepath.Join(d.cfg.Home, "coresearch", chat.String())
}

func (d *daemon) fillLiveFile(ctx context.Context, h *chatHost, f *localfiles.Folder, m *localfiles.Manifest, ref coresearch.FileRef, changes *fillChanges) error {
	if ref.Size > coresearch.MaxTextBytes || *ref.DocVersion < 0 {
		return errors.New("invalid live document reference")
	}
	stage, e := os.CreateTemp("", "lyk-paper-fill-")
	if e != nil {
		return e
	}
	defer os.Remove(stage.Name())
	defer stage.Close()
	hash := sha256.New()
	bounded := &fillWriter{w: io.MultiWriter(stage, hash), left: ref.Size}
	if e = d.coreAPI(h.work.WorkspaceID).Download(ctx, d.corePath("blobs/"+ref.SHA256), bounded); e != nil {
		return e
	}
	if bounded.left != 0 || hex.EncodeToString(hash.Sum(nil)) != ref.SHA256 {
		return errors.New("live snapshot hash mismatch")
	}
	if _, e = stage.Seek(0, io.SeekStart); e != nil {
		return e
	}
	h.filesMu.Lock()
	defer h.filesMu.Unlock()
	current, e := fillDigest(f, ref.Path)
	if e != nil {
		return e
	}
	pin := m.Entries[ref.Path]
	if current != "" && current != ref.SHA256 && (pin.SHA256 == "" || current != pin.SHA256) {
		changes.Discarded = append(changes.Discarded, ref.Path)
	}
	target, e := f.Prepare(ref.Path)
	if e != nil {
		return e
	}
	defer target.Close()
	if e = target.Replace(func(w io.Writer) error {
		_, e := io.Copy(w, stage)
		if e != nil {
			return e
		}
		file, ok := w.(*os.File)
		if !ok {
			return errors.New("snapshot target must be a file")
		}
		return file.Chmod(0444)
	}); e != nil {
		return e
	}
	m.Entries[ref.Path] = localfiles.Pin{N: int(ref.N), SHA256: ref.SHA256}
	return nil
}
