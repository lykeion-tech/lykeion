package sandbox

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// systemReads are what interpreters, compilers and their libraries load.
// /private/var/select/sh picks the shell /bin/sh runs; timezone data backs
// /etc/localtime. /opt/homebrew is added when present.
var systemReads = []string{
	`(literal "/")`,
	`(subpath "/usr")`, `(subpath "/bin")`, `(subpath "/sbin")`,
	`(subpath "/System")`, `(subpath "/Library")`, `(subpath "/private/etc")`,
	`(subpath "/private/var/db/timezone")`, `(literal "/private/var/select/sh")`,
	`(literal "/dev/null")`, `(literal "/dev/zero")`, `(literal "/dev/random")`, `(literal "/dev/urandom")`,
	`(literal "/dev/tty")`, `(literal "/dev/dtracehelper")`, `(subpath "/dev/fd")`,
}

// machServices are the only system services code may talk to: user and group
// lookups, logging, change notifications, network configuration (proxies)
// and certificate trust. Every other service is refused, among them
// LaunchServices (which starts apps outside the sandbox), the pasteboard and
// the keychain.
var machServices = []string{
	"com.apple.system.opendirectoryd.libinfo",
	"com.apple.system.opendirectoryd.membership",
	"com.apple.logd",
	"com.apple.system.notification_center",
	"com.apple.SystemConfiguration.configd",
	"com.apple.trustd.agent",
}

// seatbeltProfile renders the policy as SBPL. Everything is denied unless a
// rule below allows it. Paths must be resolved: a (subpath "/tmp/x") never
// matches /private/tmp/x.
func (p Policy) seatbeltProfile() (string, error) {
	if err := p.validateNetwork(); err != nil {
		return "", err
	}
	if p.LoopbackOnly {
		return "", ErrLoopbackOnlyUnsupported
	}
	r, err := p.resolve()
	if err != nil {
		return "", err
	}
	var b strings.Builder
	rule := func(parts ...string) { b.WriteString("(" + strings.Join(parts, " ") + ")\n") }
	b.WriteString("(version 1)\n(deny default)\n")
	rule("allow process-fork process-exec")
	rule("allow signal (target same-sandbox)")
	rule("allow sysctl-read")
	reads := systemReads
	if _, err := os.Stat("/opt/homebrew"); err == nil {
		reads = append(reads[:len(reads):len(reads)], `(subpath "/opt/homebrew")`)
	}
	ambientRead := func(filters ...string) {
		if len(r.restricted) == 0 {
			rule(append([]string{"allow file-read*"}, filters...)...)
			return
		}
		terms := []string{"(require-any " + strings.Join(filters, " ") + ")"}
		for _, root := range r.restricted {
			terms = append(terms, "(require-not (subpath "+sbplString(root)+"))")
		}
		rule("allow file-read* (require-all " + strings.Join(terms, " ") + ")")
	}
	ambientRead(reads...)
	rule(`allow file-ioctl (literal "/dev/dtracehelper")`)

	// Resolving a path (realpath, a venv's interpreter link) stats every
	// folder above it, so the folders above the system locations and the
	// policy's folders are stat-able but not listable, as are the /etc, /tmp
	// and /var links themselves.
	meta := []string{"allow file-read-metadata", `(literal "/etc")`, `(literal "/tmp")`, `(literal "/var")`}
	seen := map[string]bool{}
	dirs := []string{"/opt/homebrew", "/private/var/db/timezone", "/private/var/select/sh"}
	paths := slices.Concat(dirs, r.read, r.write, r.writeData, r.writeFiles, r.writeSQLite)
	if p.HomeRead {
		paths = append(paths, r.home)
	}
	for _, dir := range paths {
		for a := filepath.Dir(dir); a != "/" && !seen[a]; a = filepath.Dir(a) {
			seen[a] = true
			meta = append(meta, "(literal "+sbplString(a)+")")
		}
	}
	rule(meta...)
	if p.HomeRead {
		ambientRead("(subpath " + sbplString(r.home) + ")")
	}
	for _, dir := range r.read {
		rule("allow file-read* (subpath " + sbplString(dir) + ")")
	}
	for _, dir := range r.write {
		rule("allow file-read* file-write* (subpath " + sbplString(dir) + ")")
	}
	// Regex literals preserve regex backslashes; ordinary SBPL string quoting does not.
	for _, dir := range r.writeSQLite {
		pattern := "^" + regexp.QuoteMeta(dir) + "/[A-Za-z][A-Za-z0-9_]*_[0-9]+[.]sqlite(-wal|-shm|-journal)?$"
		rule("allow file-read* file-write* (regex #" + `"` + pattern + `"` + ")")
	}
	for _, file := range r.writeFiles {
		rule("allow file-read* file-write* (literal " + sbplString(file) + ")")
	}
	for _, file := range r.writeData {
		rule("allow file-read* (literal " + sbplString(file) + ")")
		rule("allow file-write-data (literal " + sbplString(file) + ")")
	}
	// The package managers keep the person's own data (databases, service
	// state) under var; the rest of their trees stays readable.
	rule(`deny file-read* (subpath "/opt/homebrew/var") (subpath "/usr/local/var")`)
	rule(`allow file-write* (literal "/dev/null") (literal "/dev/tty") (subpath "/dev/fd") (literal "/dev/dtracehelper")`)

	// Semaphores and shared memory back Python's multiprocessing (locks,
	// process pools, shared_memory).
	rule("allow ipc-posix-sem")
	rule("allow ipc-posix-shm*")

	services := []string{"allow mach-lookup"}
	allowedServices := machServices
	if p.Keychain {
		allowedServices = slices.Concat(allowedServices, []string{"com.apple.SecurityServer", "com.apple.securityd.xpc"})
	}
	for _, s := range allowedServices {
		services = append(services, "(global-name "+sbplString(s)+")")
	}
	rule(services...)

	// Unless disabled, the network is open over IP. Unix sockets are reachable
	// only for name lookups and inside the write folders: anywhere else they lead to
	// agents and daemons running as the person (ssh-agent, docker).
	if p.NoNetwork {
		rule("deny network*")
	} else {
		rule("allow system-socket")
		rule("allow network-outbound (remote ip)")
		rule("allow network-inbound network-bind (local ip)")
		rule(`allow network-outbound (remote unix-socket (path-literal "/private/var/run/mDNSResponder"))`)
		for _, dir := range r.write {
			rule("allow network-outbound (remote unix-socket (subpath " + sbplString(dir) + "))")
			rule("allow network-inbound network-bind (local unix-socket (subpath " + sbplString(dir) + "))")
		}
	}
	// Read-only paths inside the write folders come last, so their denials
	// win. A denied write covers removing, renaming and replacing the path,
	// whatever case it is spelled in, as Seatbelt matches the stored name.
	for _, c := range slices.Concat(r.readOnly, r.lateReadOnly) {
		rule("deny file-write* (subpath " + sbplString(c) + ")")
		rule("deny network-outbound (remote unix-socket (subpath " + sbplString(c) + "))")
		rule("deny network-inbound network-bind (local unix-socket (subpath " + sbplString(c) + "))")
	}
	protected := r.blocked
	if p.HomeRead {
		protected = slices.Concat(protected, r.hidden)
	}
	for _, c := range protected {
		rule("deny file-write* (subpath " + sbplString(c) + ")")
	}
	// No writable ancestor, including a granted root, may carry protected
	// children into another write grant and change their policy paths.
	guarded := map[string]bool{}
	for _, c := range slices.Concat(r.readOnly, r.lateReadOnly, r.blocked, r.hidden) {
		for a := filepath.Dir(c); a != "/"; a = filepath.Dir(a) {
			if !guarded[a] && slices.ContainsFunc(r.write, func(w string) bool { return isAtOrUnder(a, w) }) {
				rule("deny file-write-unlink (literal " + sbplString(a) + ")")
				guarded[a] = true
			}
		}
	}

	// Hidden paths come last of all: the code may neither read nor write them.
	for _, c := range r.hidden {
		rule("deny file-read* file-write* (subpath " + sbplString(c) + ")")
		rule("deny network-outbound (remote unix-socket (subpath " + sbplString(c) + "))")
		rule("deny network-inbound network-bind (local unix-socket (subpath " + sbplString(c) + "))")
	}
	return b.String(), nil
}

// sbplString quotes s as an SBPL string literal. resolve has already refused
// control characters, so only the backslash and the quote need escaping.
func sbplString(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}
