// Package pack parses and validates field-pack archives: a manifest, the
// skills each role loads and the Starlark checks for each column. An archive
// is untrusted input, so every limit is enforced while reading it.
package pack

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"reflect"
	"slices"
	"strings"

	"lykeion/server/internal/board"
	"lykeion/server/internal/pack/check"
)

const (
	MaxArchive = 5 << 20
	MaxFiles   = 200
	MaxFile    = 1 << 20
	// maxExpanded bounds the bytes all entries inflate to; the headers'
	// declared sizes are not trusted, the reads are counted.
	maxExpanded = MaxArchive * 4
)

type Column struct {
	Phases   [][]string         `json:"phases"`
	Reviewer string             `json:"reviewer"`
	Kinds    map[string]KindDef `json:"kinds"`
	Config   json.RawMessage    `json:"config,omitempty"` // the column's own settings, decoded by its stage plan; core never reads it
}

type KindDef struct {
	Roles     []string `json:"roles"`
	KeyPrefix string   `json:"key_prefix,omitempty"`
}

type Split struct {
	Unit  string  `json:"unit"`
	Ratio float64 `json:"ratio"`
}

type Manifest struct {
	Name               string            `json:"name"`
	Version            string            `json:"version"`
	Description        string            `json:"description"` // shown when a researcher picks their field
	StartKinds         []string          `json:"start_kinds"`
	Cap                int32             `json:"cap,omitempty"` // 0 means the core default
	Bands              *board.Bands      `json:"bands,omitempty"`
	Columns            map[string]Column `json:"columns"`
	Failures           map[string]string `json:"failures"`                // failure type → target column
	Sources            []string          `json:"sources,omitempty"`       // connector ids a study may use; narrows the workspace's enabled set
	ScienceIndex       bool              `json:"science_index,omitempty"` // the pack's roles get the Lykeion science skills index
	Vendored           map[string]string `json:"vendored,omitempty"`      // skills/<column>/<role>/<path> → sha256 of a vendored third-party file
	Split              *Split            `json:"split,omitempty"`
	ReportingGuideline string            `json:"reporting_guideline,omitempty"`
	Benchmark          string            `json:"benchmark,omitempty"`
}

type Pack struct {
	Manifest Manifest
	SHA256   string
	Skills   map[string]map[string][]byte // "<column>/<role>" → files under skills/<column>/<role>/, keyed by relative path
	Checks   map[string][]byte            // column → checks/<column>.star
}

// Parse reads and validates an archive. Any problem is an error and nothing
// is returned.
func Parse(zipBytes []byte) (*Pack, error) {
	if len(zipBytes) > MaxArchive {
		return nil, fmt.Errorf("pack: archive exceeds %d bytes", MaxArchive)
	}
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		return nil, fmt.Errorf("pack: not a zip archive: %w", err)
	}
	if len(zr.File) > MaxFiles {
		return nil, fmt.Errorf("pack: more than %d entries", MaxFiles)
	}
	files, err := readFiles(zr)
	if err != nil {
		return nil, err
	}
	raw, ok := files["pack.json"]
	if !ok {
		return nil, errors.New("pack: pack.json is missing")
	}
	sum := sha256.Sum256(zipBytes)
	p := &Pack{
		SHA256: hex.EncodeToString(sum[:]),
		Skills: map[string]map[string][]byte{},
		Checks: map[string][]byte{},
	}
	if err := checkKeys(raw); err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p.Manifest); err != nil {
		return nil, fmt.Errorf("pack: pack.json: %w", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("pack: pack.json: trailing data")
	}
	for name, body := range files {
		parts := strings.Split(name, "/")
		switch {
		case name == "pack.json":
		case parts[0] == "skills" && len(parts) >= 4 && slices.Contains(board.Stages, parts[1]):
			key := parts[1] + "/" + parts[2]
			if p.Skills[key] == nil {
				p.Skills[key] = map[string][]byte{}
			}
			p.Skills[key][strings.Join(parts[3:], "/")] = body
		case parts[0] == "checks" && len(parts) == 2 && strings.HasSuffix(parts[1], ".star") &&
			slices.Contains(board.Stages, strings.TrimSuffix(parts[1], ".star")):
			p.Checks[strings.TrimSuffix(parts[1], ".star")] = body
		default:
			return nil, fmt.Errorf("pack: unexpected file %q", name)
		}
	}
	if err := p.validate(files); err != nil {
		return nil, err
	}
	return p, nil
}

// readFiles returns every regular file by name, refusing unsafe names, links,
// duplicates and archives that inflate past maxExpanded.
func readFiles(zr *zip.Reader) (map[string][]byte, error) {
	files := map[string][]byte{}
	lower := map[string]bool{}
	budget := int64(maxExpanded)
	for _, f := range zr.File {
		name := f.Name
		if f.FileInfo().IsDir() {
			continue
		}
		if !f.Mode().IsRegular() {
			return nil, fmt.Errorf("pack: %q is not a regular file", name)
		}
		if strings.ContainsAny(name, "\\\x00") || !fs.ValidPath(name) {
			return nil, fmt.Errorf("pack: unsafe path %q", name)
		}
		// Names that differ only in case collide on a case-insensitive
		// file system, so they count as duplicates.
		if l := strings.ToLower(name); lower[l] {
			return nil, fmt.Errorf("pack: duplicate entry %q", name)
		} else {
			lower[l] = true
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("pack: %q: %w", name, err)
		}
		body, err := io.ReadAll(io.LimitReader(rc, min(budget, MaxFile)+1))
		rc.Close()
		if err != nil {
			return nil, fmt.Errorf("pack: %q: %w", name, err)
		}
		if len(body) > MaxFile {
			return nil, fmt.Errorf("pack: %q exceeds %d bytes", name, MaxFile)
		}
		if int64(len(body)) > budget {
			return nil, fmt.Errorf("pack: contents exceed %d bytes", maxExpanded)
		}
		budget -= int64(len(body))
		files[name] = body
	}
	return files, nil
}

func (p *Pack) validate(files map[string][]byte) error {
	m := &p.Manifest
	if m.Name == "" || m.Version == "" {
		return errors.New("pack: name and version are required")
	}
	for key := range p.Skills {
		if _, ok := m.Columns[strings.SplitN(key, "/", 2)[0]]; !ok {
			return fmt.Errorf("pack: skills for undeclared column in %q", key)
		}
	}
	for col := range p.Checks {
		if _, ok := m.Columns[col]; !ok {
			return fmt.Errorf("pack: check script for undeclared column %q", col)
		}
	}
	if len(m.StartKinds) == 0 {
		return errors.New("pack: start_kinds is empty")
	}
	for _, k := range m.StartKinds {
		if !slices.Contains([]string{"question", "dataset", "claim"}, k) {
			return fmt.Errorf("pack: start kind %q is not question, dataset or claim", k)
		}
	}
	if m.Cap < 0 {
		return errors.New("pack: cap is negative")
	}
	if b := m.Bands; b != nil && !(0 < b.No && b.No < b.Yes && b.Yes < 1) {
		return errors.New("pack: bands must satisfy 0 < no < yes < 1")
	}
	if s := m.Split; s != nil && (s.Unit == "" || !(0 < s.Ratio && s.Ratio < 1)) {
		return errors.New("pack: split needs a unit and 0 < ratio < 1")
	}
	for failure, target := range m.Failures {
		if failure == "" {
			return errors.New("pack: a failure type is empty")
		}
		if !slices.Contains(board.Stages, target) {
			return fmt.Errorf("pack: failure %q targets %q, not a stage", failure, target)
		}
	}
	for name, c := range m.Columns {
		if !slices.Contains(board.Stages, name) {
			return fmt.Errorf("pack: column %q is not a stage", name)
		}
		if _, ok := p.Checks[name]; !ok {
			return fmt.Errorf("pack: column %q has no checks/%s.star", name, name)
		}
		inColumn := map[string]bool{}
		roles := slices.Concat(slices.Concat(c.Phases...), []string{c.Reviewer})
		for _, r := range roles {
			if r == "" {
				continue
			}
			inColumn[r] = true
			if _, ok := p.Skills[name+"/"+r]["SKILL.md"]; !ok {
				return fmt.Errorf("pack: role %q in %q has no SKILL.md", r, name)
			}
		}
		for kind, def := range c.Kinds {
			for _, r := range def.Roles {
				if !inColumn[r] {
					return fmt.Errorf("pack: kind %q in %q names role %q outside its phases and reviewer", kind, name, r)
				}
			}
		}
	}
	for file, want := range m.Vendored {
		body, ok := files[file]
		if !ok {
			return fmt.Errorf("pack: vendored file %q is not in the archive", file)
		}
		sum := sha256.Sum256(body)
		if hex.EncodeToString(sum[:]) != strings.ToLower(want) {
			return fmt.Errorf("pack: vendored file %q does not match its sha256", file)
		}
	}
	return nil
}

// Skill is the role's SKILL.md, or "" when the pack has none.
func (p *Pack) Skill(column, role string) string {
	return string(p.Skills[column+"/"+role]["SKILL.md"])
}

// SkillFiles is the role's whole folder, keyed by path relative to it.
func (p *Pack) SkillFiles(column, role string) map[string][]byte {
	return p.Skills[column+"/"+role]
}

// Check runs the column's script over recs; its failure types are the
// manifest's declared ones.
func (p *Pack) Check(ctx context.Context, column string, recs []check.Record) ([]check.Result, error) {
	src, ok := p.Checks[column]
	if !ok {
		return nil, fmt.Errorf("pack: no check script for %q", column)
	}
	failures := make([]string, 0, len(p.Manifest.Failures))
	for f := range p.Manifest.Failures {
		failures = append(failures, f)
	}
	slices.Sort(failures)
	return check.Run(ctx, column+".star", src, recs, failures, check.Default)
}

// Target is the column a failure type routes to, or "" if undeclared.
func (p *Pack) Target(failure string) string { return p.Manifest.Failures[failure] }

// checkKeys refuses duplicate keys and keys that are not spelled exactly as a
// field, in pack.json and in each column; encoding/json alone would fold case
// and let the last duplicate win.
func checkKeys(raw []byte) error {
	if err := exactKeys(raw, reflect.TypeFor[Manifest]()); err != nil {
		return fmt.Errorf("pack: pack.json: %w", err)
	}
	var top struct {
		Columns json.RawMessage `json:"columns"`
	}
	if err := json.Unmarshal(raw, &top); err != nil || top.Columns == nil {
		return nil // the strict decode reports the real problem
	}
	if err := exactKeys(top.Columns, nil); err != nil {
		return fmt.Errorf("pack: columns: %w", err)
	}
	var cols map[string]json.RawMessage
	if json.Unmarshal(top.Columns, &cols) != nil {
		return nil
	}
	for name, c := range cols {
		if err := exactKeys(c, reflect.TypeFor[Column]()); err != nil {
			return fmt.Errorf("pack: column %q: %w", name, err)
		}
	}
	return nil
}

// exactKeys walks one JSON object's keys. With a struct type each key must
// match a json tag exactly; with nil only duplicates are refused.
func exactKeys(raw []byte, t reflect.Type) error {
	allowed := map[string]bool{}
	if t != nil {
		for f := range t.Fields() {
			allowed[strings.Split(f.Tag.Get("json"), ",")[0]] = true
		}
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil // not an object; the strict decode reports it
	}
	seen := map[string]bool{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil
		}
		key := tok.(string)
		if seen[key] {
			return fmt.Errorf("duplicate key %q", key)
		}
		seen[key] = true
		if t != nil && !allowed[key] {
			return fmt.Errorf("key %q is not a field name", key)
		}
		var skip json.RawMessage
		if dec.Decode(&skip) != nil {
			return nil
		}
	}
	return nil
}
