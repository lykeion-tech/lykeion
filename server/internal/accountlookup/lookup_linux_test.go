//go:build linux

package accountlookup

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestPasswdRecordRequiresOneMatchingSafeHome(t *testing.T) {
	cases := []struct {
		name, value, uid string
		ok               bool
	}{
		{"normal", "user:x:123:456::/home/user:/bin/sh\n", "123", true},
		{"no newline", "user:x:123:456::/home/user:/bin/sh", "123", true},
		{"wrong uid", "user:x:124:456::/home/user:/bin/sh\n", "123", false},
		{"root home", "user:x:123:456::/:/bin/sh\n", "123", false},
		{"relative", "user:x:123:456::home/user:/bin/sh\n", "123", false},
		{"unclean", "user:x:123:456::/home/../home/user:/bin/sh\n", "123", false},
		{"control", "user:x:123:456::/home/u\tser:/bin/sh\n", "123", false},
		{"two records", "user:x:123:456::/home/user:/bin/sh\nother:x:123:456::/tmp:/bin/sh\n", "123", false},
		{"malformed gid", "user:x:123:no::/home/user:/bin/sh\n", "123", false},
		{"compat plus", "+user:x:123:456::/home/user:/bin/sh\n", "123", false},
		{"compat minus", "-user:x:123:456::/home/user:/bin/sh\n", "123", false},
		{"extra fields", "user:x:123:456::/home/user:/bin/sh:extra\n", "123", false},
		{"oversize", strings.Repeat("x", 8193), "123", false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			u, err := parsePasswd(tt.value, tt.uid)
			if (err == nil) != tt.ok {
				t.Fatalf("user=%+v err=%v", u, err)
			}
			if tt.ok && (u.Uid != tt.uid || u.HomeDir != "/home/user") {
				t.Fatalf("unexpected user %+v", u)
			}
		})
	}
}

func TestLookupCurrentUIDUsesSystemNSS(t *testing.T) {
	if mode := os.Getenv("LYK_ACCOUNT_TEST_MODE"); mode == "untrusted" || mode == "files-rejected" {
		t.Skip("fixture deliberately rejects lookup")
	}
	uid := strconv.Itoa(os.Geteuid())
	u, err := LookupId(uid)
	if err != nil {
		t.Fatal(err)
	}
	if u.Uid != uid || u.HomeDir == "" {
		t.Fatalf("unexpected user %+v", u)
	}
}

// The test binary can act as a root-owned getent fixture inside an isolated
// container; this emulates an account absent from /etc/passwd.
func TestMain(m *testing.M) {
	if len(os.Args) == 3 && os.Args[1] == "passwd" {
		if len(os.Environ()) != 2 || os.Getenv("LC_ALL") != "C" || os.Getenv("PATH") != "/usr/sbin:/usr/bin:/sbin:/bin" {
			os.Exit(7)
		}
		switch os.Args[2] {
		case "40001":
			fmt.Print(strings.Repeat("x", maxRecord+1))
			os.Exit(0)
		case "40002":
			time.Sleep(10 * time.Second)
			os.Exit(0)
		case "40003":
			fmt.Print("nss-only:x:40004:999::/nss-only-home:/bin/sh\n")
			os.Exit(0)
		}
		fmt.Printf("nss-only:x:%s:999::/nss-only-home:/bin/sh\n", os.Args[2])
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestLookupIgnoresEnvironmentForNSS(t *testing.T) {
	if mode := os.Getenv("LYK_ACCOUNT_TEST_MODE"); mode == "untrusted" || mode == "files-rejected" {
		t.Skip("fixture deliberately rejects lookup")
	}
	t.Setenv("HOME", "/attacker-home")
	t.Setenv("XDG_CONFIG_HOME", "/attacker-config")
	t.Setenv("PATH", "/attacker-bin")
	t.Setenv("LD_PRELOAD", "/attacker-library")
	u, err := Current()
	if err != nil || u.Uid != strconv.Itoa(os.Geteuid()) || u.HomeDir == "/attacker-home" {
		t.Fatalf("unexpected account %+v: %v", u, err)
	}
	if (os.Getenv("LYK_ACCOUNT_TEST_MODE") == "nss-fixture" || os.Getenv("LYK_ACCOUNT_TEST_MODE") == "files") && u.HomeDir != "/nss-only-home" {
		t.Fatalf("synthetic NSS home missing: %+v", u)
	}
}

func TestSyntheticNSSFailuresAreBounded(t *testing.T) {
	if os.Getenv("LYK_ACCOUNT_TEST_MODE") != "nss-fixture" {
		t.Skip("synthetic getent fixture requires isolated container")
	}
	for _, uid := range []string{"40001", "40002", "40003"} {
		t.Run(uid, func(t *testing.T) {
			started := time.Now()
			if user, err := LookupId(uid); err == nil || user != nil {
				t.Fatalf("accepted malformed or stalled response: %+v %v", user, err)
			}
			if time.Since(started) > 5*time.Second {
				t.Fatal("NSS lookup exceeded deadline")
			}
		})
	}
}

func TestUntrustedGetentFailsClosed(t *testing.T) {
	if os.Getenv("LYK_ACCOUNT_TEST_MODE") != "untrusted" && os.Getenv("LYK_ACCOUNT_TEST_MODE") != "files-rejected" {
		t.Skip("untrusted getent fixture requires isolated container")
	}
	if u, err := Current(); err == nil || u != nil {
		t.Fatalf("untrusted executable accepted: %+v %v", u, err)
	}
}

func TestFilesOnlyNSSMustBeExplicitAndUnambiguous(t *testing.T) {
	for _, tt := range []struct {
		name, config string
		want         bool
	}{
		{"plain", "passwd: files\n", true},
		{"comments", "# comment\npasswd: files # only local\nhosts: files dns\n", true},
		{"missing", "hosts: files dns\n", false},
		{"network", "passwd: files sss\n", false},
		{"criteria", "passwd: files [NOTFOUND=return]\n", false},
		{"duplicate", "passwd: files\npasswd: files\n", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := filesOnly(tt.config); got != tt.want {
				t.Fatalf("filesOnly=%v want %v", got, tt.want)
			}
		})
	}
}

func TestPasswdFileRejectsAmbiguousIdentity(t *testing.T) {
	const good = "plain:x:34567:34567::/nss-only-home:/bin/sh\n"
	for _, tt := range []struct {
		name, records string
		ok            bool
	}{
		{"canonical", good, true},
		{"duplicate", good + good, false},
		{"leading-zero before canonical", "leading:x:00034567:34567::/leading:/bin/sh\n" + good, false},
		{"signed UID", "leading:x:+34567:34567::/leading:/bin/sh\n" + good, false},
		{"spaced UID", "leading:x: 34567:34567::/leading:/bin/sh\n" + good, false},
		{"compat plus", "+legacy:x:34567:34567::/nss-only-home:/bin/sh\n", false},
		{"compat minus", "-legacy:x:34567:34567::/nss-only-home:/bin/sh\n", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			u, err := parsePasswdFile(tt.records, "34567")
			if (err == nil) != tt.ok {
				t.Fatalf("user=%+v err=%v", u, err)
			}
		})
	}
}

// This control is used only in a disposable Linux container whose /etc/passwd
// contains a leading-zero record before a canonical duplicate.
func TestLibcNormalizesLeadingZeroUID(t *testing.T) {
	if os.Getenv("LYK_ACCOUNT_TEST_MODE") != "libc-control" {
		t.Skip("requires isolated passwd fixture")
	}
	cmd := exec.Command("/usr/bin/getent", "passwd", "34567")
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C"}
	output, err := cmd.Output()
	if err != nil || !strings.Contains(string(output), ":34567:34567::/leading:") {
		t.Fatalf("getent control: %q %v", output, err)
	}
}
