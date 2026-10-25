package cli

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"lykeion/server/internal/apiclient"
)

const maxCardUploadBytes = 5 << 30

type cardService struct {
	Name         string   `json:"name"`
	AttachmentID string   `json:"attachment_id"`
	Start        string   `json:"start"`
	Client       string   `json:"client"`
	Env          []string `json:"env"`
	folder       string
}
type cardUploadFlags struct{ attachments, services, starts, clients, variables []string }

var cardServiceName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

func (f cardUploadFlags) upload(ctx context.Context, c *apiclient.Client, in map[string]any) error {
	services := []cardService{}
	byName := map[string]int{}
	if len(f.services) > 4 {
		return errors.New("a study can have at most 4 hidden services")
	}
	if len(f.attachments) > 100 {
		return errors.New("a study can have at most 100 files")
	}
	for _, v := range f.services {
		name, folder, ok := strings.Cut(v, "=")
		if !ok || !cardServiceName.MatchString(name) || folder == "" {
			return errors.New("--service requires NAME=DIR with a lowercase service name")
		}
		if _, ok := byName[name]; ok {
			return fmt.Errorf("duplicate hidden service name: %s", name)
		}
		byName[name] = len(services)
		services = append(services, cardService{Name: name, folder: folder, Env: []string{}})
	}
	for _, flag := range []struct {
		name   string
		values []string
	}{{"service-start", f.starts}, {"service-client", f.clients}, {"service-env", f.variables}} {
		for _, v := range flag.values {
			name, value, ok := strings.Cut(v, "=")
			i, exists := byName[name]
			if !ok || !exists || value == "" {
				return fmt.Errorf("--%s requires NAME=VALUE for a named service", flag.name)
			}
			switch flag.name {
			case "service-start":
				services[i].Start = value
			case "service-client":
				services[i].Client = value
			case "service-env":
				services[i].Env = append(services[i].Env, value)
			}
		}
	}
	for _, s := range services {
		if strings.TrimSpace(s.Start) == "" || strings.ContainsRune(s.Start, 0) {
			return fmt.Errorf("hidden service start command required: %s", s.Name)
		}
	}
	// All folders are packed before any upload so a refused tree sends nothing.
	packs := make([]*os.File, 0, len(services))
	defer func() {
		for _, p := range packs {
			p.Close()
			os.Remove(p.Name())
		}
	}()
	for _, s := range services {
		p, err := packServiceFolder(ctx, s.folder)
		if err != nil {
			return err
		}
		packs = append(packs, p)
	}
	c.HTTP = &http.Client{}
	var ids []string
	for _, name := range f.attachments {
		file, err := os.Open(name)
		if err != nil {
			return err
		}
		info, err := file.Stat()
		if err == nil && (!info.Mode().IsRegular() || info.Size() > maxCardUploadBytes) {
			err = fmt.Errorf("%s: an attachment must be a regular file of at most 5 GB", name)
		}
		var out struct{ ID string }
		if err == nil {
			err = c.Upload(ctx, "/api/attachments", nil, "file", filepath.Base(name), io.LimitReader(file, maxCardUploadBytes+1), &out)
		}
		file.Close()
		if err != nil {
			return err
		}
		ids = append(ids, out.ID)
	}
	if len(ids) > 0 {
		in["attachment_ids"] = ids
	}
	for i := range services {
		var out struct{ ID string }
		if err := c.Upload(ctx, "/api/attachments", map[string]string{"kind": "service"}, "file", services[i].Name+".tar", packs[i], &out); err != nil {
			return err
		}
		services[i].AttachmentID = out.ID
	}
	if len(services) > 0 {
		in["services"] = services
	}
	return nil
}

// limitedTarWriter bounds the complete tar, including headers and padding.
type limitedTarWriter struct {
	w    io.Writer
	left int64
}

func (w *limitedTarWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.left {
		return 0, errors.New("a service archive can be at most 5 GB")
	}
	n, err := w.w.Write(p)
	w.left -= int64(n)
	return n, err
}

func packServiceFolder(ctx context.Context, folder string) (*os.File, error) {
	root, err := os.OpenRoot(folder)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	tmp, err := os.CreateTemp("", "lyk-service-*.tar")
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			tmp.Close()
			os.Remove(tmp.Name())
		}
	}()
	tw := tar.NewWriter(&limitedTarWriter{w: tmp, left: maxCardUploadBytes})
	entries := 0
	var total int64
	err = fs.WalkDir(root.FS(), ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if name == "." {
			return nil
		}
		entries++
		if entries > 20000 {
			return errors.New("An archive can contain at most 20,000 files and directories and 10 GB unpacked")
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("%s: service folders cannot contain links or special files", name)
		}
		if strings.ContainsAny(name, "\\\x00") || strings.Contains(strings.Split(name, "/")[0], ":") {
			return errors.New("Unsafe or unreadable archive")
		}
		if info.Mode().IsRegular() {
			total += info.Size()
		}
		if total > 10<<30 {
			return errors.New("An archive can contain at most 20,000 files and directories and 10 GB unpacked")
		}
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = name
		if info.IsDir() {
			header.Name += "/"
			return tw.WriteHeader(header)
		}
		file, err := root.Open(name)
		if err != nil {
			return err
		}
		defer file.Close()
		now, err := file.Stat()
		if err != nil {
			return err
		}
		if !os.SameFile(info, now) || !now.Mode().IsRegular() {
			return fmt.Errorf("%s changed while being packed", name)
		}
		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		_, err = io.CopyN(tw, file, info.Size())
		return err
	})
	if err == nil {
		err = tw.Close()
	}
	if err != nil {
		return nil, err
	}
	if _, err = tmp.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	ok = true
	return tmp, nil
}
