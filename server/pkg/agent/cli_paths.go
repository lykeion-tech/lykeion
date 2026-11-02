package agent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// CLIPaths are the person's own CLI files a run may change.
type CLIPaths struct {
	// ManagedSkills limits Coresearch customizations to this chat skill folder.
	// Discovery probes leave it empty.
	ManagedSkills string
	// Scratch identifies the temporary discovery folder for a sandbox factory.
	Scratch string
	// ConfigHome identifies the account store behind these state grants.
	ConfigHome string
	Write      []string
	WriteData  []string
	// WriteFiles permits creation and replacement of these exact filenames.
	WriteFiles []string
	// LockDirs are exact lock folders the CLI creates and removes.
	LockDirs []string
	// WriteSQLite permits numeric-version SQLite stores and their side files.
	WriteSQLite []string
	Blocked     []string
	// Hidden are the person's own CLI files a study run may not read.
	Hidden   []string
	Keychain bool
}

// ClaudeProjectDir encodes a working folder as Claude's project store name.
func ClaudeProjectDir(home, cwd string) string {
	return filepath.Join(home, "projects", strings.Map(func(r rune) rune {
		if r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			return r
		}
		return '-'
	}, cwd))
}

// personalCodexFiles are the Codex home entries a study run may not read,
// besides the memories stores.
var personalCodexFiles = []string{"AGENTS.md", "AGENTS.override.md", "plugins", "skills", "rules", "hooks", "agents", "history.jsonl", "archived_sessions", "prompts", "transcription-history.jsonl"}

// personalClaudeFiles are the Claude home entries a study run may not read.
// Project memories are not among them: Claude loads only the run folder's
// own, and a person may keep thousands, more than a sandbox profile can list.
var personalClaudeFiles = []string{"CLAUDE.md", "rules", "plugins", "skills", "agents", "commands", "output-styles", "workflows", "routines", "history.jsonl"}

// SandboxPaths selects state paths without creating empty CLI state files.
func SandboxPaths(harness, home, runDir, resume, goos string) (CLIPaths, error) {
	p := CLIPaths{ConfigHome: home, Keychain: goos == "darwin"}
	if goos != "darwin" && goos != "linux" {
		return p, fmt.Errorf("no CLI sandbox paths on %s", goos)
	}
	if !filepath.IsAbs(home) || !filepath.IsAbs(runDir) {
		return p, fmt.Errorf("CLI paths must be absolute")
	}
	switch harness {
	case "claude":
		p.Write = []string{ClaudeProjectDir(home, runDir)}
		if resume != "" {
			if !sessionToken.MatchString(resume) {
				return p, fmt.Errorf("invalid resumed session")
			}
			matches, err := filepath.Glob(filepath.Join(home, "projects", "*", resume+".jsonl"))
			if err != nil {
				return p, err
			}
			if len(matches) == 0 {
				return p, fmt.Errorf("resumed Claude session not found")
			}
			fi, err := os.Lstat(matches[0])
			if err != nil {
				return p, err
			}
			if !fi.Mode().IsRegular() {
				return p, fmt.Errorf("resumed Claude transcript is not a regular file")
			}
			old := filepath.Dir(matches[0])
			if !slices.Contains(p.Write, old) {
				p.Write = append(p.Write, old)
			}
		}
		if goos == "linux" {
			p.WriteData = []string{filepath.Join(home, ".credentials.json")}
		}
		// Claude takes these lock folders before refreshing its sign-in, and
		// without them it keeps the expired token. The second is the legacy
		// lock beside the home, named after it.
		p.LockDirs = []string{filepath.Join(home, ".oauth_refresh.lock"), home + ".lock"}
		// The person's own instructions, customizations and prompt history
		// stay unreadable; the sign-in stays. As for Codex, only existing
		// entries are listed.
		for _, name := range personalClaudeFiles {
			if err := p.hide(filepath.Join(home, name)); err != nil {
				return p, err
			}
		}
	case "codex":
		if goos == "linux" {
			p.Write = []string{home}
			for _, name := range []string{"config.toml", "AGENTS.md", "hooks", "rules", "agents", "skills"} {
				p.Blocked = append(p.Blocked, filepath.Join(home, name))
			}
		} else {
			for _, name := range []string{"sessions", "cache", "tmp", ".tmp", "shell_snapshots", "thread-writer-locks"} {
				p.Write = append(p.Write, filepath.Join(home, name))
			}
			for _, name := range []string{"auth.json", "models_cache.json", "history.jsonl", "session_index.jsonl", "installation_id", ".sqlite-maintenance.lock", "version.json"} {
				p.WriteFiles = append(p.WriteFiles, filepath.Join(home, name))
			}
			p.WriteSQLite = []string{home}
		}
		// The person's own instructions, memories, history and customizations
		// stay unreadable: a run brings its own role, skills and tools. An
		// absent entry holds nothing personal, and the sandbox refuses absent
		// hidden paths beside writable state, so only existing ones are listed.
		entries, err := os.ReadDir(home)
		if err != nil && !os.IsNotExist(err) {
			return p, err
		}
		for _, e := range entries {
			// Codex opens its memories databases even with memories off, and
			// cannot resume a session without them, so they stay readable.
			memories := strings.HasPrefix(e.Name(), "memories") && !strings.Contains(e.Name(), ".sqlite")
			if !memories && !slices.Contains(personalCodexFiles, e.Name()) {
				continue
			}
			// Codex will not start when Seatbelt refuses to list its agents
			// folder, so on macOS the folder stays listable and what is in it
			// is hidden.
			// A link to a folder counts as the folder. If its contents can't
			// be listed they can't be hidden one by one either, and hiding
			// the folder whole would stop Codex, so the run doesn't start.
			if agents := filepath.Join(home, e.Name()); e.Name() == "agents" && goos == "darwin" {
				if e.Type()&os.ModeSymlink != 0 {
					// A link that can't be followed is left to hide below.
					agents, _ = filepath.EvalSymlinks(agents)
				}
				if fi, err := os.Stat(agents); err == nil && fi.IsDir() {
					inside, err := os.ReadDir(agents)
					if err != nil {
						return p, fmt.Errorf("cannot list the Codex agents folder to hide what is in it: %w", err)
					}
					for _, a := range inside {
						if err := p.hide(filepath.Join(agents, a.Name())); err != nil {
							return p, err
						}
					}
					continue
				}
			}
			if err := p.hide(filepath.Join(home, e.Name())); err != nil {
				return p, err
			}
		}
	default:
		return p, fmt.Errorf("unsupported CLI %q", harness)
	}
	for _, dir := range p.Write {
		if err := PrepareCLIStateDir(home, dir); err != nil {
			return p, err
		}
	}
	return p, nil
}

// hide lists an existing personal entry as hidden. The sandbox refuses links
// in the home, and a read through a link reaches what it leads to, so a link
// is hidden by its target, wherever that lies. A link that leads nowhere
// holds nothing to hide.
func (p *CLIPaths) hide(path string) error {
	fi, err := os.Lstat(path)
	if err == nil && fi.Mode()&os.ModeSymlink != 0 {
		path, err = filepath.EvalSymlinks(path)
	}
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !slices.Contains(p.Hidden, path) {
		p.Hidden = append(p.Hidden, path)
	}
	return nil
}

// PlaceBlocked creates inert Linux bind targets without replacing existing data.
func PlaceBlocked(p CLIPaths) error {
	for _, path := range p.Blocked {
		if _, err := os.Lstat(path); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return err
		}
		switch filepath.Base(path) {
		case "config.toml", "AGENTS.md":
			f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if os.IsExist(err) {
				continue
			}
			if err != nil {
				return err
			}
			if err = f.Close(); err != nil {
				return err
			}
		case "hooks", "rules", "agents", "skills":
			if err := os.Mkdir(path, 0700); err != nil && !os.IsExist(err) {
				return err
			}
		default:
			return fmt.Errorf("unsupported blocked placeholder %q", path)
		}
	}
	return nil
}

// PrepareCLIStateDir refuses redirected state directories before creating children.
func PrepareCLIStateDir(home, dir string) error {
	if strings.ContainsFunc(home, func(r rune) bool { return r < 0x20 || r == 0x7f }) || slices.Contains(strings.Split(home, string(filepath.Separator)), "..") {
		return fmt.Errorf("invalid CLI home")
	}
	if err := os.MkdirAll(home, 0700); err != nil {
		return err
	}
	rel, err := filepath.Rel(home, dir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
		return fmt.Errorf("CLI state directory escapes home")
	}
	current := home
	parts := []string{""}
	if rel != "." {
		parts = append(parts, strings.Split(rel, string(filepath.Separator))...)
	}
	for _, part := range parts {
		current = filepath.Join(current, part)
		fi, err := os.Lstat(current)
		if os.IsNotExist(err) {
			if err = os.Mkdir(current, 0700); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("CLI state path %q is not a real directory", current)
		}
	}
	return nil
}
