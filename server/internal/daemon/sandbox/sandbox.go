// Package sandbox confines the processes the door starts: they may write only
// the policy's Write folders, may read nothing under the person's home but the
// policy's Read and Write folders, and keep the network unless NoNetwork is set.
// Children inherit the confinement. macOS uses Seatbelt (sandbox-exec), Linux
// uses bubblewrap.
//
// What the sandbox leaves open:
//   - On macOS, sandboxed code can read other processes' command lines, so
//     no secret may ever be passed in argv.
//   - On macOS, the IORegistry (hardware identifiers) is readable, enough to
//     fingerprint the machine.
//   - On Linux, LoopbackOnly cannot constrain binding without removing the
//     shared network: services may listen on every interface. macOS refuses
//     LoopbackOnly policies because its localhost filter also permits other
//     local IPv4 addresses. Same-user process inspection is a separate gap.
//   - On Linux, with the default network policy, abstract unix sockets stay
//     reachable; closing them would take the network namespace. So does a
//     socket inside a read-only path: a read-only mount doesn't stop a
//     connect.
//   - bwrap binds a folder by path after the policy has checked it, so a link
//     swapped in during that short window would be followed.
//   - On macOS, POSIX shared memory and semaphores are allowed by any name
//     (Seatbelt cannot scope them usefully), so sandboxed code can open
//     segments other processes of the person created under a known name.
package sandbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
)

// Policy names the folders a sandboxed process may use. Write folders are
// also readable, so they are not repeated in Read. Home is the person's home
// folder, hidden apart from those.
type Policy struct {
	// StrictRead exposes only system paths and explicit grants on Linux.
	StrictRead bool
	// ConfineEtc withdraws the system configuration folder (/etc) from the
	// system reads, leaving only the local time zone link and, on Linux, the
	// loader cache. On Linux it requires StrictRead.
	ConfineEtc bool
	// NoNetwork denies all network access, including DNS lookups.
	NoNetwork bool
	// LoopbackOnly requests strict 127.0.0.1 binding. macOS refuses this policy
	// until that boundary can be enforced; Linux retains its documented bind gap.
	LoopbackOnly bool
	Write        []string
	Read         []string
	Home         string
	// HomeRead makes the home readable, apart from Hidden paths.
	HomeRead bool
	// RestrictedRoots are ambient-read exclusions. Only explicit descendant
	// grants are exposed, including when new siblings appear after launch.
	RestrictedRoots []string
	// CarvedRoots opt in to an isolated subtree beneath ambient or ancestor
	// grants. Only explicit descendant grants expose its contents.
	CarvedRoots []string
	// Blocked paths inside Write cannot be changed, even when absent.
	Blocked []string
	// WriteData grants only in-place writes to existing regular files.
	WriteData []string
	// WriteFiles grants lifecycle operations on exact filenames (macOS only).
	WriteFiles []string
	// LockDirs are exact folder paths the process may create, fill and
	// remove, again and again (macOS only). Each may be absent or an existing
	// folder, never a link; nothing beside it opens.
	LockDirs []string
	// WriteSQLite grants numeric-version SQLite stores directly in these folders.
	WriteSQLite []string
	// Keychain allows the macOS Keychain services.
	Keychain bool
	// ReadOnly are files and folders directly inside a Write folder that the
	// process may read but not write, remove, rename or replace. Each must
	// already exist and be no link.
	ReadOnly []string
	// LateReadOnly names direct-child files published by the supervisor before
	// launch. Linux binds them strictly at start and fails if still absent.
	LateReadOnly []string
	// Hidden are files and folders the process may neither read nor write.
	// Explicit paths anywhere are accepted with writable-ancestor protection; absent
	// paths outside writable folders remain protected. Paths must be no link.
	Hidden []string
}

const sandboxExec = "/usr/bin/sandbox-exec"

// ErrLoopbackOnlyUnsupported prevents a broader local-address rule from being
// mistaken for an exact loopback boundary.
var ErrLoopbackOnlyUnsupported = errors.New("macOS cannot enforce service binding only to 127.0.0.1; hidden services are blocked")

// ErrPolicy marks every error from building or starting a sandboxed command,
// so callers can tell a fault of the machine's sandbox from the program's own.
var ErrPolicy = errors.New("cannot build the sandbox")

type policyError struct{ error }

func (e policyError) Unwrap() []error { return []error{e.error, ErrPolicy} }

// PolicyError marks err as a sandbox fault while keeping its text.
func PolicyError(err error) error {
	if err == nil {
		return nil
	}
	return policyError{err}
}

// Command returns name with args wrapped in the platform's sandbox. A path
// that cannot be expressed safely fails closed with an error.
//
// The paths are checked and resolved here, once, so callers build the policy
// and the command once per run, after creating its folders. Callers set Dir,
// Env and Cancel as on any command, but keep SysProcAttr: the command already
// starts a new session (no controlling terminal), which also makes its pid
// the process group a kill(-pid) reaches. Setting Setpgid on top of it would
// make the start fail.
// A successful command must be paired with defer CloseCommand(cmd).
func (p Policy) Command(ctx context.Context, name string, args ...string) (*exec.Cmd, error) {
	cmd, err := p.command(ctx, name, args...)
	return cmd, PolicyError(err)
}

func (p Policy) command(ctx context.Context, name string, args ...string) (*exec.Cmd, error) {
	if err := p.validateNetwork(); err != nil {
		return nil, err
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		prof, err := p.seatbeltProfile()
		if err != nil {
			return nil, err
		}
		cmd = exec.CommandContext(ctx, sandboxExec, append([]string{"-p", prof, "--", name}, args...)...)
	case "linux":
		bwrap, err := exec.LookPath("bwrap")
		if err != nil {
			return nil, errors.New("install bubblewrap")
		}
		argv, err := p.bwrapArgs(name, args...)
		if err != nil {
			return nil, err
		}
		cmd = exec.CommandContext(ctx, bwrap, argv...)
		if p.NoNetwork {
			if err := attachNoNetworkFilter(cmd); err != nil {
				return nil, err
			}
		}
	default:
		return nil, fmt.Errorf("no sandbox on %s", runtime.GOOS)
	}
	newSession(cmd)
	return cmd, nil
}

func (p Policy) validateNetwork() error {
	if p.NoNetwork && p.LoopbackOnly {
		return errors.New("sandbox: NoNetwork and LoopbackOnly are exclusive")
	}
	return nil
}

// Check proves the sandbox both starts and confines on this machine: true
// runs under a policy for the person's home, and a file under a stand-in home
// is unreadable. The error names what is missing.
func Check(ctx context.Context) error { return check(ctx, Policy.Command) }

func check(ctx context.Context, command func(Policy, context.Context, string, ...string) (*exec.Cmd, error)) error {
	u, err := user.Current()
	if err != nil {
		return err
	}
	cmd, err := command(Policy{Home: u.HomeDir}, ctx, "true")
	if err != nil {
		return err
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("sandbox failed to start: %v: %s", err, strings.TrimSpace(string(out)))
	}
	// The stand-in home lies in the person's own, where nothing but the home
	// rule hides it: the temp folders are hidden on every platform anyway.
	home, err := os.MkdirTemp(u.HomeDir, ".lyk-sandbox-check-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(home)
	probe := filepath.Join(home, "probe")
	if err := os.WriteFile(probe, []byte("x"), 0o600); err != nil {
		return err
	}
	if cmd, err = command(Policy{Home: home}, ctx, "cat", probe); err != nil {
		return err
	}
	if cmd.Run() == nil {
		return errors.New("sandbox started but does not confine reads")
	}
	return nil
}

// resolved is a policy with every path absolute, symlink-free and free of
// characters a profile cannot carry.
type resolved struct {
	home                           string
	write, read, readOnly, hidden  []string
	blocked, writeData, writeFiles []string
	writeSQLite, lockDirs          []string
	restricted                     []string
	carved                         []string
	lateReadOnly                   []string
}

func (p Policy) resolve() (resolved, error) {
	if p.Home == "" {
		return resolved{}, errors.New("sandbox policy has no home folder")
	}
	if err := checkSpelling(p.Home); err != nil {
		return resolved{}, err
	}
	h, err := findHome(p.Home)
	if err != nil {
		return resolved{}, fmt.Errorf("sandbox path: %w", err)
	}
	r := resolved{home: h.path}
	// linkDirs are the folders in which a policy path passed a link.
	var linkDirs []string
	for _, set := range []struct {
		in  []string
		out *[]string
	}{{p.Write, &r.write}, {p.Read, &r.read}} {
		for _, raw := range set.in {
			c, links, err := walk(raw, h)
			if err != nil {
				return resolved{}, err
			}
			linkDirs = append(linkDirs, links...)
			if h.inside(c) {
				return resolved{}, fmt.Errorf("sandbox path %q would open the whole home folder", raw)
			}
			*set.out = append(*set.out, c)
		}
	}
	// The code may have made any link in a write folder, so no policy path
	// may pass one, wherever it leads.
	for _, w := range r.write {
		fi, err := os.Stat(w)
		if err != nil {
			return resolved{}, fmt.Errorf("sandbox path: %w", err)
		}
		folder := home{w, fi}
		for _, dir := range linkDirs {
			// by identity: a link's folder may be spelled unlike the write
			// folder it lies in
			if folder.holds(dir) {
				return resolved{}, fmt.Errorf("sandbox path passes a link in the write folder %q", w)
			}
		}
	}

	for _, raw := range p.CarvedRoots {
		if fi, err := os.Lstat(raw); err == nil && fi.Mode()&os.ModeSymlink != 0 {
			return resolved{}, fmt.Errorf("carved root %q is a link", raw)
		}
		c, err := r.policyPath(raw, h)
		if err != nil {
			return resolved{}, err
		}
		fi, err := os.Lstat(c)
		if err != nil {
			return resolved{}, err
		}
		if !fi.IsDir() || h.inside(c) {
			return resolved{}, fmt.Errorf("sandbox carved root %q is not a proper directory", raw)
		}
		if slices.Contains(r.write, c) || slices.Contains(r.read, c) {
			return resolved{}, fmt.Errorf("carved root %q cannot itself be an explicit grant", raw)
		}
		if !slices.Contains(r.carved, c) {
			r.carved = append(r.carved, c)
			r.restricted = append(r.restricted, c)
		}
	}
	// Seatbelt and bwrap disagree on a read folder inside a write folder (or
	// the reverse), so the two never overlap.
	for _, w := range r.write {
		for _, rd := range r.read {
			if isAtOrUnder(w, rd) || isAtOrUnder(rd, w) {
				if slices.ContainsFunc(r.carved, func(root string) bool {
					return (isAtOrUnder(root, rd) && isAtOrUnder(w, root)) || (isAtOrUnder(root, w) && isAtOrUnder(rd, root))
				}) {
					continue
				}
				return resolved{}, fmt.Errorf("sandbox read folder %q overlaps write folder %q", rd, w)
			}
		}
	}
	for _, raw := range p.RestrictedRoots {
		if fi, err := os.Lstat(raw); err == nil && fi.Mode()&os.ModeSymlink != 0 {
			return resolved{}, fmt.Errorf("restricted root %q is a link", raw)
		}
		c, err := r.policyPath(raw, h)
		if err != nil {
			return resolved{}, err
		}
		fi, err := os.Lstat(c)
		if err != nil {
			return resolved{}, err
		}
		if !fi.IsDir() || h.inside(c) {
			return resolved{}, fmt.Errorf("sandbox restricted root %q is not a proper directory outside the home root", raw)
		}
		for _, grant := range slices.Concat(r.read, r.write, p.WriteData, p.WriteFiles, p.WriteSQLite, p.LockDirs) {
			// File grants are resolved below; spelling checks still run there.
			if isAtOrUnder(c, grant) {
				return resolved{}, fmt.Errorf("sandbox grant %q would reopen restricted root %q", grant, raw)
			}
		}
		if !slices.Contains(r.restricted, c) {
			r.restricted = append(r.restricted, c)
		}
	}
	for _, raw := range p.ReadOnly {
		c, err := r.carve(raw, h, r.write)
		if err != nil {
			return resolved{}, err
		}
		r.readOnly = append(r.readOnly, c)
	}
	for _, raw := range p.LateReadOnly {
		c, err := r.blockedPath(raw, h)
		if err != nil {
			return resolved{}, err
		}
		if !slices.Contains(r.write, filepath.Dir(c)) {
			return resolved{}, fmt.Errorf("late read-only path %q is not directly inside a write folder", raw)
		}
		if fi, err := os.Lstat(c); err == nil {
			if !fi.Mode().IsRegular() {
				return resolved{}, fmt.Errorf("late read-only path %q is not a regular file", raw)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return resolved{}, err
		}
		for _, w := range r.write {
			if isAtOrUnder(w, c) {
				return resolved{}, fmt.Errorf("late read-only path %q holds a write folder", raw)
			}
		}
		r.lateReadOnly = append(r.lateReadOnly, c)
	}
	for _, raw := range p.Blocked {
		c, err := r.blockedPath(raw, h)
		if err != nil {
			return resolved{}, err
		}
		inside := false
		for _, w := range r.write {
			if c != w && isAtOrUnder(c, w) {
				inside = true
			}
		}
		if !inside {
			return resolved{}, fmt.Errorf("sandbox blocked path %q is not inside a write folder", raw)
		}
		for _, w := range r.write {
			if isAtOrUnder(w, c) {
				return resolved{}, fmt.Errorf("sandbox blocked path %q holds a write folder", raw)
			}
		}
		r.blocked = append(r.blocked, c)
	}
	for _, raw := range p.WriteSQLite {
		c, err := r.policyPath(raw, h)
		if err != nil {
			return resolved{}, err
		}
		if strings.Contains(c, `"`) {
			return resolved{}, fmt.Errorf("sandbox SQLite parent %q contains a quote unsupported by Seatbelt regex", raw)
		}
		fi, err := os.Stat(c)
		if err != nil {
			return resolved{}, err
		}
		if !fi.IsDir() {
			return resolved{}, fmt.Errorf("sandbox SQLite parent %q is not a directory", raw)
		}
		r.writeSQLite = append(r.writeSQLite, c)
	}
	for _, raw := range p.WriteFiles {
		parent, err := r.policyPath(filepath.Dir(raw), h)
		if err != nil {
			return resolved{}, err
		}
		if err := checkSpelling(raw); err != nil {
			return resolved{}, err
		}
		c := filepath.Join(parent, filepath.Base(raw))
		fi, err := os.Lstat(c)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return resolved{}, err
		}
		if err == nil {
			if !fi.Mode().IsRegular() {
				return resolved{}, fmt.Errorf("sandbox write-file path %q is not a regular file", raw)
			}
			c, err = r.policyPath(c, h)
			if err != nil {
				return resolved{}, err
			}
		}
		for _, protected := range slices.Concat(r.blocked, r.readOnly, r.lateReadOnly) {
			if isAtOrUnder(c, protected) {
				return resolved{}, fmt.Errorf("sandbox write-file path %q is protected", raw)
			}
		}
		r.writeFiles = append(r.writeFiles, c)
	}
	// A lock folder is never created here: one that exists looks held.
	for _, raw := range p.LockDirs {
		parent, err := r.policyPath(filepath.Dir(raw), h)
		if err != nil {
			return resolved{}, err
		}
		if err := checkSpelling(raw); err != nil {
			return resolved{}, err
		}
		c := filepath.Join(parent, filepath.Base(raw))
		fi, err := os.Lstat(c)
		// A file or link where the lock folder belongs can only be a leftover
		// a sandboxed run planted; removing it (never a link's target) keeps
		// it from blocking every later launch. Unlink never removes a folder,
		// so a lock taken since the look stays, and one gone since is fine.
		if err == nil && !fi.IsDir() {
			if err := syscall.Unlink(c); err != nil && !errors.Is(err, os.ErrNotExist) {
				return resolved{}, fmt.Errorf("sandbox lock path %q: %w", raw, err)
			}
		} else if err == nil && h.inside(c) {
			return resolved{}, fmt.Errorf("sandbox lock path %q is not a lock folder", raw)
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return resolved{}, err
		}
		for _, protected := range slices.Concat(r.blocked, r.readOnly, r.lateReadOnly) {
			if isAtOrUnder(c, protected) {
				return resolved{}, fmt.Errorf("sandbox lock path %q is protected", raw)
			}
		}
		r.lockDirs = append(r.lockDirs, c)
	}
	for _, raw := range p.WriteData {
		c, err := r.policyPath(raw, h)
		if err != nil {
			return resolved{}, err
		}
		fi, err := os.Stat(c)
		if err != nil {
			return resolved{}, err
		}
		if !fi.Mode().IsRegular() {
			return resolved{}, fmt.Errorf("sandbox write-data path %q is not a regular file", raw)
		}
		for _, blocked := range r.blocked {
			if isAtOrUnder(c, blocked) {
				return resolved{}, fmt.Errorf("sandbox write-data path %q is inside blocked path %q", raw, blocked)
			}
		}
		r.writeData = append(r.writeData, c)
	}
	for _, root := range r.restricted {
		if slices.Contains(r.carved, root) {
			continue
		}
		for _, grant := range slices.Concat(r.writeData, r.writeFiles, r.writeSQLite, r.lockDirs) {
			if isAtOrUnder(root, grant) {
				return resolved{}, fmt.Errorf("sandbox grant %q would reopen restricted root %q", grant, root)
			}
		}
	}
	for _, raw := range p.Hidden {
		c, err := r.policyPath(raw, h)
		if errors.Is(err, os.ErrNotExist) {
			missing, missingErr := r.blockedPath(raw, h)
			if missingErr != nil {
				return resolved{}, missingErr
			}
			if missing != h.path {
				for _, w := range r.write {
					if isAtOrUnder(missing, w) {
						return resolved{}, fmt.Errorf("sandbox missing hidden path %q is inside a write folder", raw)
					}
				}
				for _, file := range slices.Concat(r.writeFiles, r.lockDirs) {
					if isAtOrUnder(file, missing) {
						return resolved{}, fmt.Errorf("sandbox missing hidden path %q holds a writable file", raw)
					}
				}
				for _, dir := range r.writeSQLite {
					if isAtOrUnder(dir, missing) || filepath.Dir(missing) == dir {
						return resolved{}, fmt.Errorf("sandbox missing hidden path %q overlaps SQLite state", raw)
					}
				}
				r.hidden = append(r.hidden, missing)
				continue
			}
		}
		if err != nil {
			return resolved{}, err
		}
		if c != h.path {
			for _, w := range slices.Concat(r.write, r.writeData, r.writeFiles, r.writeSQLite, r.lockDirs) {
				if isAtOrUnder(w, c) {
					return resolved{}, fmt.Errorf("sandbox hidden path %q holds a write path", raw)
				}
			}
			for _, w := range r.write {
				if isAtOrUnder(c, w) {
					if _, err := r.carve(raw, h, slices.Concat(r.write, r.readOnly)); err != nil {
						return resolved{}, err
					}
					break
				}
			}
			r.hidden = append(r.hidden, c)
			continue
		}
		c, err = r.carve(raw, h, slices.Concat(r.write, r.readOnly))
		if err != nil {
			return resolved{}, err
		}
		r.hidden = append(r.hidden, c)
	}
	return r, nil
}

// policyPath also refuses links the sandboxed process could replace.
func (r resolved) policyPath(raw string, h home) (string, error) {
	c, links, err := walk(raw, h)
	if err != nil {
		return "", err
	}
	for _, w := range r.write {
		fi, err := os.Stat(w)
		if err != nil {
			return "", err
		}
		folder := home{w, fi}
		for _, dir := range links {
			if folder.holds(dir) {
				return "", fmt.Errorf("sandbox path passes a link in the write folder %q", w)
			}
		}
	}
	return c, nil
}

// blockedPath resolves existing components without following links in the home.
// The remaining suffix may be absent: Seatbelt can deny a future path.
func (r resolved) blockedPath(raw string, h home) (string, error) {
	if err := checkSpelling(raw); err != nil {
		return "", err
	}
	current := raw
	var tail []string
	for {
		c, err := r.policyPath(current, h)
		if err == nil {
			for i := len(tail) - 1; i >= 0; i-- {
				c = filepath.Join(c, tail[i])
			}
			return c, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", err
		}
		tail = append(tail, filepath.Base(current))
		current = parent
	}
}

// carve resolves a read-only or hidden path: an existing file or folder, no
// link, directly inside one of parents and holding no write folder. Kept
// this shallow, it can't be moved away by renaming a folder above it, which
// is a write folder's own mount or path, or itself read-only.
func (r resolved) carve(raw string, h home, parents []string) (string, error) {
	if err := checkSpelling(raw); err != nil {
		return "", err
	}
	parent, _, err := walk(filepath.Dir(raw), h)
	if err != nil {
		return "", err
	}
	if !slices.Contains(parents, parent) {
		return "", fmt.Errorf("sandbox path %q is not directly inside a write folder or a read-only path", raw)
	}
	fi, err := os.Lstat(filepath.Join(parent, filepath.Base(raw)))
	if err != nil {
		return "", fmt.Errorf("sandbox path: %w", err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("sandbox path %q is a link", raw)
	}
	c, err := canonical(filepath.Join(parent, filepath.Base(raw)))
	if err != nil {
		return "", fmt.Errorf("sandbox path: %w", err)
	}
	for _, w := range r.write {
		if isAtOrUnder(w, c) {
			return "", fmt.Errorf("sandbox path %q holds the write folder %q", raw, w)
		}
	}
	return c, nil
}

// home is the person's home folder, known by its file identity: a case- or
// normalisation-insensitive file system answers to many spellings of it.
type home struct {
	path string
	fi   os.FileInfo
}

func findHome(raw string) (home, error) {
	p, err := filepath.EvalSymlinks(raw)
	if err != nil {
		return home{}, err
	}
	if p, err = canonical(p); err != nil {
		return home{}, err
	}
	fi, err := os.Stat(p)
	return home{p, fi}, err
}

func (h home) is(dir string) bool {
	fi, err := os.Stat(dir)
	return err == nil && os.SameFile(fi, h.fi)
}

// holds reports whether dir, a link-free path, is the home folder or lies
// inside it. A folder above dir that cannot be examined counts as the home
// folder, so the answer fails closed.
func (h home) holds(dir string) bool {
	for {
		fi, err := os.Stat(dir)
		if err != nil || os.SameFile(fi, h.fi) {
			return true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
}

// inside reports whether the home folder is dir or lies inside it, so that
// granting dir would grant the whole home folder.
func (h home) inside(dir string) bool {
	fi, err := os.Stat(dir)
	if err != nil {
		return true
	}
	for a := h.path; ; a = filepath.Dir(a) {
		if afi, err := os.Stat(a); err != nil || os.SameFile(afi, fi) {
			return true
		}
		if a == "/" {
			return false
		}
	}
}

// walk resolves raw one component at a time from the root. Links above the
// home folder are followed, so aliases (/var for /private/var, a link to the
// home folder) still work. Inside the home folder no link is followed: a link
// planted there (~/.lyk-envs -> ~/.ssh, or one leading out to another
// folder) must not carry a policy path to its target. Nor may a link outside
// lead into the home folder below its top. The result is spelled as the file
// system keeps it, which is what Seatbelt matches against.
func walk(raw string, h home) (string, []string, error) {
	if err := checkSpelling(raw); err != nil {
		return "", nil, err
	}
	var linkDirs []string
	cur := "/"
	for _, part := range strings.Split(raw, "/") {
		if part == "" || part == "." {
			continue
		}
		next := filepath.Join(cur, part)
		fi, err := os.Lstat(next)
		if err != nil {
			return "", nil, fmt.Errorf("sandbox path: %w", err)
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			linkDirs = append(linkDirs, cur)
			if h.holds(cur) {
				return "", nil, fmt.Errorf("sandbox path %q passes the link %q in the home folder", raw, next)
			}
			if next, err = filepath.EvalSymlinks(next); err != nil {
				return "", nil, fmt.Errorf("sandbox path: %w", err)
			}
			if !h.is(next) && h.holds(next) {
				return "", nil, fmt.Errorf("sandbox path %q reaches into the home folder through a link", raw)
			}
		}
		cur = next
	}
	c, err := canonical(cur)
	if err != nil {
		return "", nil, fmt.Errorf("sandbox path: %w", err)
	}
	return c, linkDirs, nil
}

// checkSpelling refuses what a profile cannot carry and what a step-by-step
// walk would read differently from the kernel ("..", which climbs out of a
// link's target rather than the spelled folder).
func checkSpelling(p string) error {
	if strings.ContainsFunc(p, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return fmt.Errorf("sandbox path %q has a control character", p)
	}
	if !filepath.IsAbs(p) {
		return fmt.Errorf("sandbox path %q is not absolute", p)
	}
	if slices.Contains(strings.Split(p, "/"), "..") {
		return fmt.Errorf("sandbox path %q climbs with ..", p)
	}
	return nil
}

// isAtOrUnder reports whether child is parent or lies inside it.
func isAtOrUnder(child, parent string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, "../")
}

// PrepareRestrictedRoot creates only missing directory components for an
// app-owned mask target. Existing content and permissions are preserved.
func PrepareRestrictedRoot(homeDir, raw string) error {
	if fi, err := os.Lstat(raw); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("restricted root %q is a link", raw)
	}
	h, err := findHome(homeDir)
	if err != nil {
		return err
	}
	c, err := (resolved{}).blockedPath(raw, h)
	if err != nil {
		return err
	}
	if isAtOrUnder(h.path, c) {
		return fmt.Errorf("restricted root %q would cover the home folder", raw)
	}
	if err = os.MkdirAll(c, 0700); err != nil {
		return err
	}
	c, _, err = walk(raw, h)
	if err != nil {
		return err
	}
	fi, err := os.Lstat(c)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("restricted root %q is not a directory", raw)
	}
	return nil
}
