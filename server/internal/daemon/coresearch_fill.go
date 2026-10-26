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

func (d *daemon) fill(ctx context.Context, chat, project uuid.UUID) ([]string, error) {
	d.chatsMu.Lock()
	h := d.chats[chat]
	d.chatsMu.Unlock()
	if h == nil || h.work.ProjectID != project {
		return nil, errors.New("unknown chat project")
	}
	if err := d.checkChat(ctx, h.work); err != nil {
		return nil, err
	}
	f, err := localfiles.Open(h.dir)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	// The shared lock spans the remote snapshot, local read/write and pin update.
	m, err := f.LockManifest(ctx)
	if err != nil {
		return nil, err
	}
	defer m.Close()
	var refs []coresearch.FileRef
	api := d.coreAPI(h.work.WorkspaceID)
	if err = api.Do(ctx, "GET", d.corePath("chats/"+chat.String()+"/files"), nil, &refs); err != nil {
		return nil, err
	}
	var unsaved []string
	for _, ref := range refs {
		name, e := coresearch.CleanPath(ref.Path)
		if e != nil || name != ref.Path {
			return nil, fmt.Errorf("invalid saved path %q", ref.Path)
		}
		digest, e := hex.DecodeString(ref.SHA256)
		if e != nil || len(digest) != sha256.Size || ref.N <= 0 || ref.Size < 0 || ref.Size > 50<<20 {
			return nil, errors.New("invalid saved file reference")
		}
		file, e := f.Read(name)
		local := ""
		if e == nil {
			hash := sha256.New()
			_, e = io.Copy(hash, file)
			file.Close()
			if e != nil {
				return nil, e
			}
			local = hex.EncodeToString(hash.Sum(nil))
		} else if !errors.Is(e, os.ErrNotExist) {
			return nil, e
		}
		pin := m.Entries[name]
		if local != "" && local != ref.SHA256 && (pin.SHA256 == "" || local != pin.SHA256) {
			unsaved = append(unsaved, name)
			continue
		}
		if local != ref.SHA256 {
			target, e := f.Prepare(name)
			if e != nil {
				return nil, e
			}
			e = target.Replace(func(w io.Writer) error {
				hash := sha256.New()
				bounded := &fillWriter{w: io.MultiWriter(w, hash), left: ref.Size}
				if e := api.Download(ctx, d.corePath("blobs/"+ref.SHA256), bounded); e != nil {
					return e
				}
				if bounded.left != 0 || hex.EncodeToString(hash.Sum(nil)) != ref.SHA256 {
					return errors.New("saved blob size or SHA256 mismatch")
				}
				return nil
			})
			target.Close()
			if e != nil {
				return nil, e
			}
		}
		m.Entries[name] = localfiles.Pin{N: int(ref.N), SHA256: ref.SHA256}
	}
	if err = m.Save(); err != nil {
		return nil, err
	}
	return unsaved, nil
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
