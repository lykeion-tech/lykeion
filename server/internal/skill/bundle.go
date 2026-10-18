// Package skill manages the workspace's immutable skill folders.
package skill

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"path"
	"slices"
	"strings"
	"unicode/utf8"

	"lykeion/server/internal/science"
	"lykeion/server/pkg/agent"
)

type Bundle struct {
	Name, Description string
	Files             map[string][]byte
}
type Limits struct{ FileBytes, SupportingBytes, SupportingFiles, ArchiveBytes int }

const (
	FileBytes       = 1 << 20
	SupportingBytes = 8 << 20
	SupportingFiles = 256
	ArchiveBytes    = 16 << 20
)

func validPath(p string) bool {
	return p != "" && p != "." && fs.ValidPath(p) && !strings.ContainsAny(p, "\\\x00:")
}

// metadata derives display fields from the main file without changing its bytes.
func metadata(b Bundle) (Bundle, error) {
	return parseMetadata(b, false)
}

func parseMetadata(b Bundle, allowMissingName bool) (Bundle, error) {
	main, ok := b.Files["SKILL.md"]
	if !ok {
		return b, fmt.Errorf("SKILL.md is required")
	}
	if !utf8.Valid(main) || bytes.IndexByte(main, 0) >= 0 {
		return b, fmt.Errorf("SKILL.md must be UTF-8 text without NUL bytes")
	}
	if bytes.HasPrefix(main, []byte("---")) {
		fm, _, err := science.ParseFrontmatter(main)
		if err != nil {
			return b, err
		}
		if fm.Name != "" {
			b.Name = fm.Name
		}
		b.Description = fm.Description
	}
	if !(allowMissingName && b.Name == "") && !agent.ValidSkillName(b.Name) {
		return b, fmt.Errorf("skill name must be 1 to 60 lowercase letters, digits or hyphens, starting with a letter or digit")
	}
	if strings.ContainsRune(b.Description, 0) {
		return b, fmt.Errorf("description contains a NUL byte")
	}
	return b, nil
}

func Validate(b Bundle) error { return validate(b, false) }

func validate(b Bundle, allowMissingName bool) error {
	if _, err := parseMetadata(b, allowMissingName); err != nil {
		return err
	}
	count, total := 0, 0
	for p, data := range b.Files {
		if !validPath(p) {
			return fmt.Errorf("unsafe file path %q", p)
		}
		if p != "SKILL.md" && strings.EqualFold(path.Base(p), "SKILL.md") {
			return fmt.Errorf("multiple SKILL.md files are not supported")
		}
		for parent := path.Dir(p); parent != "."; parent = path.Dir(parent) {
			if _, exists := b.Files[parent]; exists {
				return fmt.Errorf("file %q is also used as a directory", parent)
			}
		}
		if len(data) > FileBytes {
			return fmt.Errorf("file %q exceeds %d bytes", p, FileBytes)
		}
		if p != "SKILL.md" {
			count++
			total += len(data)
		}
	}
	if count > SupportingFiles || total > SupportingBytes {
		return fmt.Errorf("skill exceeds supporting file limits")
	}
	return nil
}

// ParseArchive accepts ZIP containers with a skill at their root or in one wrapper directory.
func ParseArchive(data []byte) (Bundle, error) {
	b := Bundle{Files: map[string][]byte{}}
	if len(data) > ArchiveBytes {
		return b, fmt.Errorf("archive exceeds %d bytes", ArchiveBytes)
	}
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return b, fmt.Errorf("invalid .skill/.zip archive: %w", err)
	}
	root := ""
	mains := 0
	seen := map[string]bool{}
	for _, f := range z.File {
		p := strings.TrimSuffix(f.Name, "/")
		if !validPath(p) || seen[f.Name] {
			return b, fmt.Errorf("unsafe or duplicate archive path %q", f.Name)
		}
		seen[f.Name] = true
		if f.Mode()&fs.ModeSymlink != 0 || (!f.FileInfo().IsDir() && !f.Mode().IsRegular()) {
			return b, fmt.Errorf("archive entry %q is not a regular file", f.Name)
		}
		if !f.FileInfo().IsDir() && path.Base(p) == "SKILL.md" {
			mains++
			root = strings.TrimSuffix(p, "SKILL.md")
		}
	}
	if mains != 1 {
		return b, fmt.Errorf("archive must contain exactly one SKILL.md")
	}
	total, count := 0, 0
	for _, f := range z.File {
		if f.FileInfo().IsDir() {
			continue
		}
		if !strings.HasPrefix(f.Name, root) {
			return b, fmt.Errorf("file %q is outside the skill folder", f.Name)
		}
		p := strings.TrimPrefix(f.Name, root)
		if f.UncompressedSize64 > FileBytes {
			return b, fmt.Errorf("file %q exceeds size limit", p)
		}
		r, e := f.Open()
		if e != nil {
			return b, e
		}
		raw, e := io.ReadAll(io.LimitReader(r, FileBytes+1))
		closeErr := r.Close()
		if e != nil {
			return b, e
		}
		if closeErr != nil {
			return b, closeErr
		}
		if len(raw) > FileBytes {
			return b, fmt.Errorf("file %q exceeds size limit", p)
		}
		if p != "SKILL.md" {
			total += len(raw)
			count++
			if total > SupportingBytes || count > SupportingFiles {
				return b, fmt.Errorf("skill exceeds supporting file limits")
			}
		}
		b.Files[p] = raw
	}
	if root != "" {
		b.Name = path.Base(strings.TrimSuffix(root, "/"))
	}
	if err = validate(b, true); err != nil {
		return b, err
	}
	return parseMetadata(b, true)
}

// Tar encodes files in path order with fixed headers so content addresses are stable.
func Tar(b Bundle) ([]byte, error) {
	if err := Validate(b); err != nil {
		return nil, err
	}
	var out bytes.Buffer
	w := tar.NewWriter(&out)
	paths := make([]string, 0, len(b.Files))
	for p := range b.Files {
		paths = append(paths, p)
	}
	slices.Sort(paths)
	for _, p := range paths {
		data := b.Files[p]
		if err := w.WriteHeader(&tar.Header{Name: p, Mode: 0o644, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
			return nil, err
		}
		if _, err := w.Write(data); err != nil {
			return nil, err
		}
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
func hash(data []byte) string { s := sha256.Sum256(data); return hex.EncodeToString(s[:]) }

type FileMeta struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	Binary bool   `json:"binary"`
}

func fileMetadata(b Bundle) []FileMeta {
	out := make([]FileMeta, 0, len(b.Files))
	for p, data := range b.Files {
		out = append(out, FileMeta{Path: p, Size: int64(len(data)), SHA256: hash(data), Binary: !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0})
	}
	slices.SortFunc(out, func(a, b FileMeta) int { return strings.Compare(a.Path, b.Path) })
	return out
}
func containsScripts(b Bundle) bool {
	for p, data := range b.Files {
		if p == "SKILL.md" {
			continue
		}
		if strings.HasPrefix(p, "scripts/") {
			return true
		}
		if utf8.Valid(data) && bytes.IndexByte(data, 0) < 0 && bytes.HasPrefix(data, []byte("#!")) {
			return true
		}
		switch strings.ToLower(path.Ext(p)) {
		case ".py", ".pyw", ".r", ".jl", ".sh", ".bash", ".zsh", ".fish", ".js", ".mjs", ".cjs", ".ts", ".tsx", ".ps1", ".bat", ".cmd", ".pl", ".rb", ".lua":
			return true
		}
	}
	return false
}
