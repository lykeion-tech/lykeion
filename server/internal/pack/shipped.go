package pack

import (
	"archive/zip"
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"slices"
)

//go:embed all:shipped
var shippedFS embed.FS

// Shipped is the packs Lykeion ships, one folder per pack name.
func Shipped() fs.FS {
	sub, err := fs.Sub(shippedFS, "shipped")
	if err != nil {
		panic(err)
	}
	return sub
}

// ShippedNames lists the pack folders in shipped, which may be nil.
func ShippedNames(shipped fs.FS) []string {
	if shipped == nil {
		return nil
	}
	entries, err := fs.ReadDir(shipped, ".")
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names
}

// ReadShipped zips the named folder in memory and parses it, so a shipped
// pack passes the same checks as an uploaded one.
// ponytail: zips on every call; cache by folder if catalog reads get hot.
func ReadShipped(shipped fs.FS, name string) ([]byte, *Pack, error) {
	if !slices.Contains(ShippedNames(shipped), name) {
		return nil, nil, fmt.Errorf("pack: no shipped pack %q: %w", name, fs.ErrNotExist)
	}
	sub, err := fs.Sub(shipped, name)
	if err != nil {
		return nil, nil, err
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	err = fs.WalkDir(sub, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("pack: shipped %s/%s is not a regular file", name, path)
		}
		body, err := fs.ReadFile(sub, path)
		if err != nil {
			return err
		}
		w, err := zw.CreateHeader(&zip.FileHeader{Name: path, Method: zip.Deflate})
		if err != nil {
			return err
		}
		_, err = w.Write(body)
		return err
	})
	if err == nil {
		err = zw.Close()
	}
	if err != nil {
		return nil, nil, err
	}
	p, err := Parse(buf.Bytes())
	if err != nil {
		return nil, nil, err
	}
	if p.Manifest.Name != name {
		return nil, nil, fmt.Errorf("pack: shipped folder %q holds pack %q", name, p.Manifest.Name)
	}
	return buf.Bytes(), p, nil
}
