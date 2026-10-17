// Package pack parses and validates Team archives: a manifest and the skills
// each member loads. An archive is untrusted input, so every limit is enforced while reading it.
package pack

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"net/url"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"lykeion/server/internal/board"
	"lykeion/server/internal/connectors"
	"lykeion/server/internal/science"
	"lykeion/server/pkg/agent"
)

const (
	MaxArchive = 5 << 20
	MaxFiles   = 200
	MaxFile    = 1 << 20
	// maxExpanded bounds the bytes all entries inflate to; the headers'
	// declared sizes are not trusted, the reads are counted.
	maxExpanded = MaxArchive * 4
)

// Team limits.
const (
	MaxMembers      = 12
	MaxCriteria     = 10
	MaxCriterionLen = 200
	MaxSteps        = 50
	DefaultSteps    = 12
)

var (
	memberName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	secretEnv  = regexp.MustCompile(`(?i)(key|token|secret|password)`)
)

type AgentDef struct {
	Harness      string `json:"harness"`
	Model        string `json:"model"`
	Instructions string `json:"instructions"`
}

// Connector is exactly one of a catalogue name, an HTTPS MCP URL or a local
// command.
type Connector struct {
	Name      string            `json:"name"`
	Catalogue string            `json:"catalogue,omitempty"`
	URL       string            `json:"url,omitempty"`
	Command   []string          `json:"command,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
}

// Member is a Team agent. Role is the description the leader reads. Each
// entry in Skills is a folder in the archive or a bundled skill.
type Member struct {
	Name       string      `json:"name"`
	Role       string      `json:"role"`
	Agent      AgentDef    `json:"agent"`
	Skills     []string    `json:"skills,omitempty"`
	Connectors []Connector `json:"connectors,omitempty"`
}

type Manifest struct {
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Description string   `json:"description"` // shown when a researcher picks their field
	Domain      string   `json:"domain"`
	Stage       string   `json:"stage"` // the column the Team works, or "" for a set
	Leader      Member   `json:"leader"`
	Auditor     Member   `json:"auditor"`
	Members     []Member `json:"members"`
	Criteria    []string `json:"criteria"`
	Steps       int      `json:"steps"` // per column; 0 means DefaultSteps
}

// MemberFor is the Team agent a run's role names: "leader", "auditor" or a
// member's name. A role the Team does not have names none.
func (m Manifest) MemberFor(role string) (Member, bool) {
	switch role {
	case "leader":
		return m.Leader, true
	case "auditor":
		return m.Auditor, true
	}
	for _, mem := range m.Members {
		if mem.Name == role {
			return mem, true
		}
	}
	return Member{}, false
}

type Pack struct {
	Manifest Manifest
	SHA256   string
	Skills   map[string]map[string][]byte // "<member>/<skill>" → files under skills/<member>/<skill>/, keyed by relative path
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
	}
	if err := exactKeys(raw, reflect.TypeFor[Manifest]()); err != nil {
		return nil, fmt.Errorf("pack: pack.json: %w", err)
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
		case parts[0] == "skills" && len(parts) >= 4 && len(parts[1]) > 0:
			key := parts[1] + "/" + parts[2]
			if p.Skills[key] == nil {
				p.Skills[key] = map[string][]byte{}
			}
			p.Skills[key][strings.Join(parts[3:], "/")] = body
		default:
			return nil, fmt.Errorf("pack: unexpected file %q", name)
		}
	}
	if err := p.validate(); err != nil {
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

func (p *Pack) validate() error {
	m := &p.Manifest
	if m.Name == "" || m.Version == "" {
		return errors.New("pack: name and version are required")
	}
	if m.Stage != "" && !slices.Contains(board.Stages, m.Stage) {
		return fmt.Errorf("pack: stage %q is not a stage", m.Stage)
	}
	if len(m.Members) < 1 || len(m.Members) > MaxMembers {
		return fmt.Errorf("pack: members needs 1 to %d entries", MaxMembers)
	}
	if len(m.Criteria) > MaxCriteria {
		return fmt.Errorf("pack: criteria has more than %d entries", MaxCriteria)
	}
	for _, c := range m.Criteria {
		if utf8.RuneCountInString(c) > MaxCriterionLen {
			return fmt.Errorf("pack: a criterion is over %d characters", MaxCriterionLen)
		}
	}
	if m.Steps < 0 || m.Steps > MaxSteps {
		return fmt.Errorf("pack: steps must be 1 to %d", MaxSteps)
	}
	if m.Steps == 0 {
		m.Steps = DefaultSteps
	}
	seen := map[string]bool{}
	all := slices.Concat([]Member{m.Leader, m.Auditor}, m.Members)
	for _, mem := range all {
		if !memberName.MatchString(mem.Name) {
			return fmt.Errorf("pack: member name %q is not a valid name", mem.Name)
		}
		if seen[mem.Name] {
			return fmt.Errorf("pack: member name %q is repeated", mem.Name)
		}
		seen[mem.Name] = true
	}
	for i, mem := range all {
		where := "members"
		switch i {
		case 0:
			where = "leader"
		case 1:
			where = "auditor"
		}
		if _, ok := agent.Harnesses[mem.Agent.Harness]; !ok {
			return fmt.Errorf("pack: %s %q: harness %q is unknown", where, mem.Name, mem.Agent.Harness)
		}
		for _, s := range mem.Skills {
			_, folder := p.Skills[mem.Name+"/"+s]["SKILL.md"]
			if !folder && !science.Has(s) {
				return fmt.Errorf("pack: %s %q: skill %q is neither a folder in the archive nor a bundled skill", where, mem.Name, s)
			}
		}
		names := map[string]bool{}
		for _, c := range mem.Connectors {
			if err := c.validate(); err != nil {
				return fmt.Errorf("pack: %s %q: connector %q: %w", where, mem.Name, c.Name, err)
			}
			// call_tool names a connector by its name, or a bare catalogue
			// entry by its catalogue name; the door keeps "sources" for itself.
			name := c.Name
			if name == "" {
				name = c.Catalogue
			}
			if name == "sources" {
				return fmt.Errorf("pack: %s %q: the connector name %q is reserved", where, mem.Name, name)
			}
			if names[name] {
				return fmt.Errorf("pack: %s %q: the connector name %q is repeated", where, mem.Name, name)
			}
			names[name] = true
		}
	}
	if err := AuditorDiffers(*m); err != nil {
		return err
	}
	for key := range p.Skills {
		if !seen[strings.SplitN(key, "/", 2)[0]] {
			return fmt.Errorf("pack: skills for unknown member in %q", key)
		}
	}
	return nil
}

// AuditorDiffers refuses a Team whose auditor runs the same harness and model
// as the leader or any member. An empty model is the harness default, so it
// equals another empty model on the same harness.
func AuditorDiffers(m Manifest) error {
	a := m.Auditor.Agent
	for _, mem := range slices.Concat([]Member{m.Leader}, m.Members) {
		if a.Harness == mem.Agent.Harness && a.Model == mem.Agent.Model {
			return fmt.Errorf("pack: auditor shares harness and model with %q", mem.Name)
		}
	}
	return nil
}

func (c Connector) validate() error {
	kinds := 0
	if c.Catalogue != "" {
		kinds++
		if _, ok := connectors.Get(c.Catalogue); !ok {
			return fmt.Errorf("catalogue %q is unknown", c.Catalogue)
		}
	}
	if c.URL != "" {
		kinds++
		if u, err := url.Parse(c.URL); err != nil || u.Scheme != "https" || u.Host == "" {
			return errors.New("url must be https")
		}
	}
	if len(c.Command) > 0 {
		kinds++
		if bin := c.Command[0]; bin == "" || strings.Contains(bin, "/") || strings.Contains(bin, "..") {
			return errors.New("command must name a program, not a path")
		}
	}
	if kinds != 1 {
		return errors.New("exactly one of catalogue, url and command is required")
	}
	for k := range c.Env {
		if secretEnv.MatchString(k) {
			return fmt.Errorf("env %q looks like a secret", k)
		}
	}
	return nil
}

// SkillFiles is the skill's whole folder, keyed by path relative to it.
func (p *Pack) SkillFiles(member, skill string) map[string][]byte {
	return p.Skills[member+"/"+skill]
}

// ManifestJSON is the manifest as pack.json holds it.
func ManifestJSON(m Manifest) ([]byte, error) { return json.MarshalIndent(m, "", "  ") }

// Zip builds the archive Parse reads; skills are keyed "<member>/<skill>".
func Zip(m Manifest, skills map[string]map[string][]byte) ([]byte, error) {
	raw, err := ManifestJSON(m)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	add := func(name string, body []byte) error {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate})
		if err == nil {
			_, err = w.Write(body)
		}
		return err
	}
	if err := add("pack.json", raw); err != nil {
		return nil, err
	}
	for _, key := range slices.Sorted(maps.Keys(skills)) {
		for _, path := range slices.Sorted(maps.Keys(skills[key])) {
			if err := add("skills/"+key+"/"+path, skills[key][path]); err != nil {
				return nil, err
			}
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// exactKeys refuses duplicate keys and keys not spelled exactly as a field,
// at every level of t; encoding/json alone would fold case and let the last
// duplicate win. Shape errors are left to the strict decode.
func exactKeys(raw []byte, t reflect.Type) error {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Slice:
		var items []json.RawMessage
		if json.Unmarshal(raw, &items) != nil {
			return nil
		}
		for _, it := range items {
			if err := exactKeys(it, t.Elem()); err != nil {
				return err
			}
		}
	case reflect.Struct, reflect.Map:
		dec := json.NewDecoder(bytes.NewReader(raw))
		if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
			return nil
		}
		fields := map[string]reflect.Type{}
		if t.Kind() == reflect.Struct {
			for f := range t.Fields() {
				fields[strings.Split(f.Tag.Get("json"), ",")[0]] = f.Type
			}
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
			var ft reflect.Type
			if t.Kind() == reflect.Map {
				ft = t.Elem()
			} else if ft = fields[key]; ft == nil {
				return fmt.Errorf("key %q is not a field name", key)
			}
			var val json.RawMessage
			if dec.Decode(&val) != nil {
				return nil
			}
			if err := exactKeys(val, ft); err != nil {
				return fmt.Errorf("%s: %w", key, err)
			}
		}
	}
	return nil
}
