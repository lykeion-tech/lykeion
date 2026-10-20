package sandbox

import (
	"os"
	"slices"
	"strings"
)

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
	r, err := p.resolve()
	if err != nil {
		return nil, err
	}
	argv := []string{
		"--ro-bind", "/", "/", "--dev", "/dev", "--proc", "/proc",
		"--tmpfs", "/tmp", "--tmpfs", "/run",
		"--ro-bind-try", "/run/systemd/resolve", "/run/systemd/resolve",
		"--tmpfs", r.home,
	}
	type bind struct{ flag, dir string }
	// A hidden file is covered by /dev/null and a hidden folder by an empty
	// read-only tmpfs.
	var binds []bind
	for _, dir := range r.write {
		binds = append(binds, bind{"--bind", dir})
	}
	for _, dir := range r.read {
		binds = append(binds, bind{"--ro-bind", dir})
	}
	// A read-only path lies directly inside a write folder, so it binds after
	// that folder, on top of it; as a mount it can't be removed or renamed.
	for _, dir := range r.readOnly {
		binds = append(binds, bind{"--ro-bind", dir})
	}
	for _, dir := range r.hidden {
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			binds = append(binds, bind{"--tmpfs", dir})
		} else {
			binds = append(binds, bind{"--hide", dir})
		}
	}
	slices.SortStableFunc(binds, func(a, b bind) int {
		return strings.Count(a.dir, "/") - strings.Count(b.dir, "/")
	})
	for _, b := range binds {
		switch b.flag {
		case "--tmpfs":
			argv = append(argv, "--tmpfs", b.dir, "--remount-ro", b.dir)
		case "--hide":
			argv = append(argv, "--ro-bind", "/dev/null", b.dir)
		default:
			argv = append(argv, b.flag, b.dir, b.dir)
		}
	}
	// The empty home stays read-only, so a write there fails instead of landing
	// in a tmpfs that vanishes; the bound folders keep their own mounts.
	argv = append(argv, "--remount-ro", r.home, "--unshare-pid", "--unshare-ipc", "--cap-drop", "ALL", "--share-net", "--die-with-parent", "--", name)
	return append(argv, args...), nil
}
