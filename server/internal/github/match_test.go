package github

import (
	"slices"
	"testing"
)

func TestMatch(t *testing.T) {
	for _, tc := range []struct {
		name, branch, title, body string
		want                      []string
	}{
		{name: "branch", branch: "lyk-12-fix", want: []string{"LYK-12"}},
		{name: "title", title: "LYK-3 and lyk-4", want: []string{"LYK-3", "LYK-4"}},
		{name: "left alphanumeric", branch: "blyk-12"},
		{name: "right alphanumeric", title: "LYK-12a"},
		{name: "leading zero", title: "LYK-012"},
		{name: "zero", title: "LYK-0"},
		{name: "out of range", title: "LYK-2147483648 LYK-999999999999999999999"},
		{name: "bare body reference", body: "Related to LYK-5"},
		{name: "closes", body: "Closes LYK-5", want: []string{"LYK-5"}},
		{name: "fixes with colon", body: "fixes: lyk-5", want: []string{"LYK-5"}},
		{name: "resolved", body: "RESOLVED LYK-5", want: []string{"LYK-5"}},
		{name: "keyword suffix", body: "unresolved LYK-5"},
		{name: "different prefix", branch: "ENG-1"},
		{name: "order and duplicates", branch: "lyk-12/lyk-4", title: "LYK-4,LYK-2", body: "Fixes LYK-1. Closes LYK-12.", want: []string{"LYK-12", "LYK-4", "LYK-2", "LYK-1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Match("LYK", tc.branch, tc.title, tc.body); !slices.Equal(got, tc.want) {
				t.Fatalf("Match = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestVerifySignature(t *testing.T) {
	const secret = "It's a Secret to Everybody"
	const header = "sha256=757107ea0eb2509fc211221cce984b8a37570b6d7586c22c46f4379c8b043e17"
	if !VerifySignature(secret, header, []byte("Hello, World!")) {
		t.Fatal("valid signature was rejected")
	}
	if VerifySignature(secret, header, []byte("Hello, World?")) ||
		VerifySignature("", header, []byte("Hello, World!")) ||
		VerifySignature(secret, header[len("sha256="):], []byte("Hello, World!")) {
		t.Fatal("invalid signature was accepted")
	}
}

func TestParsePRURL(t *testing.T) {
	for _, tc := range []struct {
		raw string
		ok  bool
	}{
		{"https://github.com/Acme/Repo/pull/7", true},
		{"https://github.com/Acme/Repo/pull/7/files", true},
		{"https://github.com/Acme/Repo/pull/7/", true},
		{"https://github.com/Acme/Repo/issues/7", false},
		{"https://github.example/Acme/Repo/pull/7", false},
		{"http://github.com/Acme/Repo/pull/7", false},
		{"https://someone@github.com/Acme/Repo/pull/7", false},
		{"https://github.com/Acme/Repo/pull/0", false},
		{"https://github.com/Acme/Repo/pull/+7", false},
		{"https://github.com/Acme/Repo/pull/2147483648", false},
		{"https://github.com/Acme/Repo/pull/x", false},
		{"https://github.com/Acme%2FRepo%2Fpull%2F7", false},
		{"https://github.com/Acme/../pull/7", false},
		{"https://github.com/Acme/Repo/pull/7/../../issues/3", false},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			owner, repo, n, ok := ParsePRURL("https://github.com", tc.raw)
			if ok != tc.ok || (ok && (owner != "acme" || repo != "repo" || n != 7)) {
				t.Fatalf("ParsePRURL = (%q, %q, %d, %v), want ok=%v", owner, repo, n, ok, tc.ok)
			}
		})
	}
	if _, _, _, ok := ParsePRURL("https://configured.example", "https://configured.example/Acme/Repo/pull/7"); !ok {
		t.Fatal("configured web origin was rejected")
	}
}
