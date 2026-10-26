package sandbox

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

var linuxSystemRoots = []string{"/usr", "/bin", "/sbin", "/lib", "/lib64", "/etc", "/sys"}

// bwrapArgs mounts the system read-only and swaps /tmp, /run and the home
// folder for empty tmpfs mounts, which hides the unix sockets of agents and
// daemons kept there; the resolver's stub folder comes back read-only. The
// policy's folders are bound in after the tmpfs mounts, shallow ones first so
// a deeper bind lands on top. --unshare-pid hides every process outside the
// sandbox, and with it their /proc/<pid>/root views of the host;
// --unshare-ipc keeps System V IPC private and --cap-drop ALL leaves even a
// root daemon's code unable to unmount what hides the home folder.
// --die-with-parent ends the sandbox if the door dies.
func (p Policy) bwrapArgs(name string, args ...string) ([]string, error) {
	if len(p.WriteFiles) != 0 || len(p.WriteSQLite) != 0 {
		return nil, fmt.Errorf("exact file lifecycle grants are unsupported on Linux")
	}
	r, err := p.resolve()
	if err != nil {
		return nil, err
	}
	argv := []string{"--ro-bind", "/", "/"}
	if p.StrictRead {
		argv = []string{"--tmpfs", "/"}
		for _, root := range linuxSystemRoots {
			info, err := os.Lstat(root)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return nil, err
			}
			if info.Mode()&os.ModeSymlink != 0 {
				target, err := os.Readlink(root)
				if err != nil {
					return nil, err
				}
				resolved, err := filepath.EvalSymlinks(root)
				if err != nil {
					return nil, err
				}
				if !slices.ContainsFunc(linuxSystemRoots, func(system string) bool { return system != root && isAtOrUnder(resolved, system) }) {
					return nil, fmt.Errorf("unsupported strict system alias %q", root)
				}
				argv = append(argv, "--symlink", target, root)
			} else {
				argv = append(argv, "--ro-bind", root, root)
			}
		}
	}
	argv = append(argv, "--dev", "/dev", "--proc", "/proc", "--tmpfs", "/tmp", "--tmpfs", "/run", "--ro-bind-try", "/run/systemd/resolve", "/run/systemd/resolve")
	if p.HomeRead {
		argv = append(argv, "--ro-bind", r.home, r.home)
	} else {
		argv = append(argv, "--tmpfs", r.home)
	}
	type bind struct{ flag, dir string }
	// A hidden file is covered by /dev/null and a hidden folder by an empty
	// read-only tmpfs.
	var binds []bind
	projected, err := r.hiddenProjections(p.HomeRead, p.StrictRead)
	if err != nil {
		return nil, err
	}
	links := map[string]string{}
	for _, parent := range projected {
		binds = append(binds, bind{"--project", parent})
		entries, err := os.ReadDir(parent)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			path := filepath.Join(parent, entry.Name())
			if slices.Contains(projected, path) || slices.ContainsFunc(r.hidden, func(hidden string) bool { return isAtOrUnder(path, hidden) }) {
				continue
			}
			if entry.Type()&os.ModeSymlink != 0 {
				target, err := os.Readlink(path)
				if err != nil {
					return nil, err
				}
				links[path] = target
				binds = append(binds, bind{"--symlink", path})
			} else {
				binds = append(binds, bind{"--ro-bind", path})
			}
		}
	}
	// An external parent projection may bind an ancestor of the otherwise
	// hidden home. Mask it again in depth order, before approved descendants.
	if !p.HomeRead && slices.ContainsFunc(projected, func(parent string) bool { return isAtOrUnder(r.home, parent) }) {
		binds = append(binds, bind{"--masked-home", r.home})
	}
	for _, root := range r.restricted {
		binds = append(binds, bind{"--restricted", root})
	}
	for _, dir := range r.write {
		binds = append(binds, bind{"--bind", dir})
	}
	for _, file := range r.writeData {
		binds = append(binds, bind{"--bind", file})
	}
	for _, dir := range r.read {
		if slices.Contains(projected, dir) {
			continue
		}
		binds = append(binds, bind{"--ro-bind", dir})
	}
	// A read-only path lies directly inside a write folder, so it binds after
	// that folder, on top of it; as a mount it can't be removed or renamed.
	for _, dir := range slices.Concat(r.readOnly, r.lateReadOnly) {
		binds = append(binds, bind{"--ro-bind", dir})
	}
	for _, path := range r.blocked {
		if !slices.Contains(r.write, filepath.Dir(path)) {
			return nil, fmt.Errorf("sandbox blocked path %q must be directly inside a write folder on Linux", path)
		}
		if _, err := os.Lstat(path); err != nil {
			return nil, fmt.Errorf("sandbox blocked path: %w", err)
		}
		binds = append(binds, bind{"--ro-bind", path})
	}
	for _, dir := range r.hidden {
		// An empty, read-only ancestor already hides this path, including
		// future descendants. A nested mount could require creating a target
		// inside that read-only namespace.
		if slices.ContainsFunc(r.hidden, func(parent string) bool { return parent != dir && isAtOrUnder(dir, parent) }) {
			continue
		}
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			binds = append(binds, bind{"--tmpfs", dir})
		} else {
			binds = append(binds, bind{"--hide", dir})
		}
	}
	if p.HomeRead {
		sockets, err := homeSockets(r.home, append(append([]string{}, r.hidden...), r.write...))
		if err != nil {
			return nil, fmt.Errorf("sandbox home sockets: %w", err)
		}
		for _, socket := range sockets {
			masked := slices.ContainsFunc(r.restricted, func(root string) bool { return isAtOrUnder(socket, root) })
			exposed := slices.ContainsFunc(r.read, func(rd string) bool { return isAtOrUnder(socket, rd) })
			if masked && !exposed {
				continue
			}
			binds = append(binds, bind{"--hide", socket})
		}
	}
	slices.SortStableFunc(binds, func(a, b bind) int {
		return strings.Count(a.dir, "/") - strings.Count(b.dir, "/")
	})
	for _, b := range binds {
		switch b.flag {
		case "--symlink":
			argv = append(argv, "--symlink", links[b.dir], b.dir)
		case "--project", "--restricted", "--masked-home":
			argv = append(argv, "--tmpfs", b.dir)
		case "--tmpfs":
			argv = append(argv, "--tmpfs", b.dir, "--remount-ro", b.dir)
		case "--hide":
			argv = append(argv, "--ro-bind", "/dev/null", b.dir)
		default:
			argv = append(argv, b.flag, b.dir, b.dir)
		}
	}
	for _, root := range slices.Concat(r.restricted, projected) {
		argv = append(argv, "--remount-ro", root)
	}
	// The empty home stays read-only, so a write there fails instead of landing
	// in a tmpfs that vanishes; the bound folders keep their own mounts.
	if p.StrictRead {
		argv = append(argv, "--remount-ro", "/")
	}
	argv = append(argv, "--remount-ro", r.home, "--unshare-pid", "--unshare-ipc", "--cap-drop", "ALL", "--share-net", "--die-with-parent", "--", name)
	return append(argv, args...), nil
}

// ponytail: walks the whole home per run; cache per daemon if launch time grows
func homeSockets(home string, skip []string) ([]string, error) {
	var sockets []string
	err := filepath.WalkDir(home, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsPermission(err) {
				return filepath.SkipDir
			}
			return err
		}
		if slices.Contains(skip, path) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&fs.ModeSocket != 0 {
			sockets = append(sockets, path)
		}
		return nil
	})
	return sockets, err
}

// hiddenProjections finds readable directories whose entries must be fixed for
// this run. An absent mount target cannot be masked inside a read-only bind.
// Reconstructing its nearest existing parent in the namespace keeps that name
// hidden without creating placeholders in the account's home. Existing bound
// entries stay live, but replacements and new siblings appear on the next run.
func (r resolved) hiddenProjections(homeRead, strictRead bool) ([]string, error) {
	var parents []string
	for _, hidden := range r.hidden {
		if strictRead && !(homeRead && isAtOrUnder(hidden, r.home)) && !slices.ContainsFunc(slices.Concat(linuxSystemRoots, r.read, r.write), func(root string) bool { return isAtOrUnder(hidden, root) }) {
			continue
		}
		if _, err := os.Lstat(hidden); err == nil {
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}

		// Private paths below replacement namespaces must not restore host
		// siblings unless an existing policy grant already exposes them.
		namespaceMasked := slices.ContainsFunc([]string{"/tmp", "/run", "/dev", "/proc"}, func(root string) bool { return isAtOrUnder(hidden, root) })
		namespaceExposed := isAtOrUnder(hidden, r.home) || isAtOrUnder(hidden, "/run/systemd/resolve") || slices.ContainsFunc(slices.Concat(r.read, r.write), func(grant string) bool { return isAtOrUnder(hidden, grant) })
		if namespaceMasked && !namespaceExposed {
			continue
		}
		// A private path already below the empty home mount needs no parent
		// projection. Reconstructing the home would reveal its other files.
		if !homeRead && isAtOrUnder(hidden, r.home) && !slices.ContainsFunc(slices.Concat(r.read, r.write), func(grant string) bool { return isAtOrUnder(hidden, grant) }) {
			continue
		}
		masked := slices.ContainsFunc(r.restricted, func(root string) bool { return isAtOrUnder(hidden, root) })
		exposed := slices.ContainsFunc(r.read, func(read string) bool { return isAtOrUnder(hidden, read) })
		if masked && !exposed {
			continue
		}
		parent := filepath.Dir(hidden)
		for {
			fi, err := os.Lstat(parent)
			if err == nil {
				if !fi.IsDir() {
					return nil, fmt.Errorf("hidden projection parent %q is not a directory", parent)
				}
				break
			}
			if !errors.Is(err, os.ErrNotExist) {
				return nil, err
			}
			parent = filepath.Dir(parent)
		}
		if parent == "/" {
			return nil, fmt.Errorf("cannot project filesystem root for hidden path %q", hidden)
		}
		if !slices.Contains(parents, parent) {
			parents = append(parents, parent)
		}
	}
	return parents, nil
}
