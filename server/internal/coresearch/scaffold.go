package coresearch

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/google/uuid"
	"lykeion/server/internal/storage"
	"lykeion/server/internal/store"
)

//go:embed templates
var templateFiles embed.FS

type Template struct {
	ID    string            `json:"id"`
	Label string            `json:"label"`
	Files map[string][]byte `json:"-"`
}

func Templates() []Template {
	ids := []string{"blank", "neurips", "icml", "iclr", "acl", "cvpr", "arxiv", "thesis"}
	labels := []string{"Blank", "NeurIPS", "ICML", "ICLR", "ACL", "CVPR", "arXiv preprint", "Thesis"}
	out := make([]Template, 0, len(ids))
	for i, id := range ids {
		files := map[string][]byte{}
		_ = fs.WalkDir(templateFiles, "templates/"+id, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() {
				body, e := templateFiles.ReadFile(path)
				if e != nil {
					return e
				}
				files[strings.TrimPrefix(path, "templates/"+id+"/")] = body
			}
			return nil
		})
		if _, ok := files["refs.bib"]; !ok {
			files["refs.bib"] = []byte{}
		}
		out = append(out, Template{id, labels[i], files})
	}
	return out
}

func TemplateByID(id string) (Template, error) {
	for _, t := range Templates() {
		if t.ID == id {
			return t, nil
		}
	}
	return Template{}, fmt.Errorf("unknown template %q", id)
}

// ScaffoldBlobs stages template content with WithBlobs before opening the save transaction.
func ScaffoldBlobs(id string) ([]UploadBlob, error) {
	t, err := TemplateByID(id)
	if err != nil {
		return nil, err
	}
	out := []UploadBlob{}
	for _, body := range t.Files {
		out = append(out, UploadBlob{SHA256: fmt.Sprintf("%x", sha256.Sum256(body)), Body: bytes.NewReader(body), Size: int64(len(body))})
	}
	return out, nil
}

// Scaffold publishes files already staged by ScaffoldBlobs and WithBlobs. Storage
// arguments keep the save interface consistent; no storage I/O runs under tx.
func Scaffold(ctx context.Context, tx *store.Tx, _ *store.Store, _ storage.Storage, ws, project, author uuid.UUID, id string) error {
	t, err := TemplateByID(id)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(t.Files))
	for name := range t.Files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		body := t.Files[name]
		if _, err = SaveVersion(ctx, tx, SaveIn{Workspace: ws, Project: project, Author: &author, Path: name, Sha256: fmt.Sprintf("%x", sha256.Sum256(body)), Size: int64(len(body))}); err != nil {
			return err
		}
	}
	return nil
}
