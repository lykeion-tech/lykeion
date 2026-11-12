//go:build linux

// Package accountlookup resolves account identity independently of HOME.
package accountlookup

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"
)

const maxRecord = 8192

var errLookup = errors.New("cannot resolve account through system NSS")

// Current resolves the effective user through the system NSS database.
func Current() (*user.User, error) { return LookupId(strconv.Itoa(os.Geteuid())) }

// LookupId resolves through the system NSS database, including non-file users.
func LookupId(uid string) (*user.User, error) {
	id, err := strconv.ParseUint(uid, 10, 32)
	if err != nil || strconv.FormatUint(id, 10) != uid {
		return nil, errLookup
	}
	bin, err := trustedSystemPath("/usr/bin/getent", true)
	if errors.Is(err, os.ErrNotExist) {
		return lookupFilesOnly(uid)
	}
	if err != nil {
		return nil, errLookup
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "passwd", uid)
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C"}
	cmd.Stderr = io.Discard
	cmd.WaitDelay = time.Second
	out := &limitedOutput{}
	cmd.Stdout = out
	if err := cmd.Run(); err != nil || out.exceeded || ctx.Err() != nil {
		return nil, errLookup
	}
	return parsePasswd(out.value.String(), uid)
}

type limitedOutput struct {
	value    strings.Builder
	exceeded bool
}

func (w *limitedOutput) Write(p []byte) (int, error) {
	if len(p) > maxRecord-w.value.Len() {
		w.exceeded = true
		return 0, errLookup
	}
	return w.value.Write(p)
}

func parsePasswd(record, uid string) (*user.User, error) {
	if len(record) > maxRecord || !utf8.ValidString(record) {
		return nil, errLookup
	}
	record = strings.TrimSuffix(record, "\n")
	if strings.ContainsAny(record, "\r\n\x00") {
		return nil, errLookup
	}
	parts := strings.Split(record, ":")
	if len(parts) != 7 || parts[0] == "" || strings.HasPrefix(parts[0], "+") || strings.HasPrefix(parts[0], "-") || strings.ContainsFunc(parts[0], unicode.IsControl) || parts[2] != uid {
		return nil, errLookup
	}
	gid, err := strconv.ParseUint(parts[3], 10, 32)
	if err != nil || strconv.FormatUint(gid, 10) != parts[3] {
		return nil, errLookup
	}
	home := parts[5]
	if !filepath.IsAbs(home) || home == "/" || filepath.Clean(home) != home || strings.ContainsFunc(home, unicode.IsControl) {
		return nil, errLookup
	}
	return &user.User{Uid: uid, Gid: parts[3], Username: parts[0], Name: parts[4], HomeDir: home}, nil
}

// Use local passwd data only when NSS explicitly names no other source.
func lookupFilesOnly(uid string) (*user.User, error) {
	config, err := readTrusted("/etc/nsswitch.conf", 64<<10)
	if err != nil || !filesOnly(config) {
		return nil, errLookup
	}
	data, err := readTrusted("/etc/passwd", 4<<20)
	if err != nil {
		return nil, errLookup
	}
	return parsePasswdFile(data, uid)
}

func parsePasswdFile(data, uid string) (*user.User, error) {
	var found *user.User
	for _, record := range strings.Split(data, "\n") {
		if record == "" || strings.HasPrefix(record, "#") {
			continue
		}
		fields := strings.Split(record, ":")
		if len(fields) != 7 {
			return nil, errLookup
		}
		id, err := strconv.ParseUint(fields[2], 10, 32)
		if err != nil || strconv.FormatUint(id, 10) != fields[2] {
			return nil, errLookup
		}
		if fields[2] != uid {
			continue
		}
		account, err := parsePasswd(record, uid)
		if err != nil || found != nil {
			return nil, errLookup
		}
		found = account
	}
	if found == nil {
		return nil, errLookup
	}
	return found, nil
}

func filesOnly(config string) bool {
	scanner := bufio.NewScanner(strings.NewReader(config))
	count := 0
	for scanner.Scan() {
		line, _, _ := strings.Cut(scanner.Text(), "#")
		name, value, ok := strings.Cut(line, ":")
		if ok && strings.TrimSpace(name) == "passwd" {
			count++
			if fields := strings.Fields(value); len(fields) != 1 || fields[0] != "files" {
				return false
			}
		}
	}
	return scanner.Err() == nil && count == 1
}

func readTrusted(name string, max int64) (string, error) {
	path, err := trustedSystemPath(name, false)
	if err != nil {
		return "", errLookup
	}
	f, err := os.Open(path)
	if err != nil {
		return "", errLookup
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil || int64(len(data)) > max {
		return "", errLookup
	}
	return string(data), nil
}

// Restrict access to root-owned, non-writable system files and ancestry.
func trustedSystemPath(name string, executable bool) (string, error) {
	path, err := filepath.EvalSymlinks(name)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(path) {
		return "", errLookup
	}
	for p := path; ; p = filepath.Dir(p) {
		fi, err := os.Lstat(p)
		if err != nil || fi.Mode()&os.ModeSymlink != 0 || fi.Mode().Perm()&0022 != 0 {
			return "", errLookup
		}
		st, ok := fi.Sys().(*syscall.Stat_t)
		if !ok || st.Uid != 0 {
			return "", errLookup
		}
		if p == path {
			if !fi.Mode().IsRegular() || (executable && (fi.Mode().Perm()&0111 == 0 || fi.Mode()&(os.ModeSetuid|os.ModeSetgid) != 0)) {
				return "", errLookup
			}
		} else if !fi.IsDir() {
			return "", errLookup
		}
		if p == "/" {
			break
		}
	}
	return path, nil
}
