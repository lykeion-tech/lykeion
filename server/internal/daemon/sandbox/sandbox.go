// Package sandbox confines the processes the door starts: they may write only
// the policy's Write folders, may read nothing under the person's home but the
// policy's Read and Write folders, and keep the network. Children inherit the
// confinement. macOS uses Seatbelt (sandbox-exec), Linux uses bubblewrap.
//
// What the sandbox leaves open:
//   - On macOS, sandboxed code can read other processes' command lines, so
//     no secret may ever be passed in argv.
//   - On macOS, the IORegistry (hardware identifiers) is readable, enough to
//     fingerprint the machine.
//   - Code may listen on every network interface, not only loopback.
//   - On Linux, abstract unix sockets stay reachable; closing them would
//     take the network namespace, and with it the network.
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
)

// Policy names the folders a sandboxed process may use. Write folders are
// also readable, so they are not repeated in Read. Home is the person's home
// folder, hidden apart from those.
type Policy struct {
	Write []string
	Read  []string
	Home  string
}

const sandboxExec = "/usr/bin/sandbox-exec"

// Command returns name with args wrapped in the platform's sandbox. A path
// that cannot be expressed safely fails closed with an error.
//
// The paths are checked and resolved here, once, so callers build the policy
// and the command once per run, after creating its folders. Callers set Dir,
// Env and Cancel as on any command, but keep SysProcAttr: the command already
// starts a new session (no controlling terminal), which also makes its pid
// the process group a kill(-pid) reaches. Setting Setpgid on top of it would
// make the start fail.
func (p Policy) Command(ctx context.Context, name string, args ...string) (*exec.Cmd, error) {
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
	default:
		return nil, fmt.Errorf("no sandbox on %s", runtime.GOOS)
	}
	newSession(cmd)
	return cmd, nil
}

// Check proves the sandbox both starts and confines on this machine: true
// runs under a policy for the person's home, and a file under a stand-in home
// is unreadable. The error names what is missing.
func Check(ctx context.Context) error {
	u, err := user.Current()
	if err != nil {
		return err
	}
	cmd, err := Policy{Home: u.HomeDir}.Command(ctx, "true")
	if err != nil {
		return err
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("sandbox failed to start: %v: %s", err, strings.TrimSpace(string(out)))
	}
	home, err := os.MkdirTemp("", "lyk-sandbox-check")
	if err != nil {
		return err
	}
	defer os.RemoveAll(home)
	probe := filepath.Join(home, "probe")
	if err := os.WriteFile(probe, []byte("x"), 0o600); err != nil {
		return err
	}
	if cmd, err = (Policy{Home: home}).Command(ctx, "cat", probe); err != nil {
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
	home        string
	write, read []string
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
	for _, set := range []struct {
		in  []string
		out *[]string
	}{{p.Write, &r.write}, {p.Read, &r.read}} {
		for _, raw := range set.in {
			c, err := walk(raw, h)
			if err != nil {
				return resolved{}, err
			}
			if h.inside(c) {
				return resolved{}, fmt.Errorf("sandbox path %q would open the whole home folder", raw)
			}
			*set.out = append(*set.out, c)
		}
	}
	// Seatbelt and bwrap disagree on a read folder inside a write folder (or
	// the reverse), so the two never overlap.
	for _, w := range r.write {
		for _, rd := range r.read {
			if isAtOrUnder(w, rd) || isAtOrUnder(rd, w) {
				return resolved{}, fmt.Errorf("sandbox read folder %q overlaps write folder %q", rd, w)
			}
		}
	}
	return r, nil
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
func walk(raw string, h home) (string, error) {
	if err := checkSpelling(raw); err != nil {
		return "", err
	}
	cur := "/"
	for _, part := range strings.Split(raw, "/") {
		if part == "" || part == "." {
			continue
		}
		next := filepath.Join(cur, part)
		fi, err := os.Lstat(next)
		if err != nil {
			return "", fmt.Errorf("sandbox path: %w", err)
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			if h.holds(cur) {
				return "", fmt.Errorf("sandbox path %q passes the link %q in the home folder", raw, next)
			}
			if next, err = filepath.EvalSymlinks(next); err != nil {
				return "", fmt.Errorf("sandbox path: %w", err)
			}
			if !h.is(next) && h.holds(next) {
				return "", fmt.Errorf("sandbox path %q reaches into the home folder through a link", raw)
			}
		}
		cur = next
	}
	c, err := canonical(cur)
	if err != nil {
		return "", fmt.Errorf("sandbox path: %w", err)
	}
	return c, nil
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
