// Package coresearch manages the shared projects and their explicit file versions.
package coresearch

import (
	"bytes"
	"fmt"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

func CleanPath(p string) (string, error) {
	refuse := func(reason string) (string, error) { return "", fmt.Errorf("path %q is not allowed: %s", p, reason) }
	if p == "" || strings.HasPrefix(p, "/") {
		return refuse("a relative file path is required")
	}
	if strings.ContainsAny(p, "\\\x00") {
		return refuse("backslash and NUL are forbidden")
	}
	if len(p) > 1024 {
		return refuse("paths can be at most 1024 bytes")
	}
	for _, s := range strings.Split(p, "/") {
		if strings.EqualFold(s, ".git") {
			return refuse("git metadata is forbidden")
		}
		if s == ".." {
			return refuse("parent segments are forbidden")
		}
		if len(s) > 255 {
			return refuse("segments can be at most 255 bytes")
		}
	}
	clean := path.Clean(p)
	if clean == "." {
		return refuse("a file path is required")
	}
	switch strings.ToLower(strings.Split(clean, "/")[0]) {
	case ".claude", ".codex", ".cursor", ".agents", ".cli-home", ".mcp.json", ".lyk-mcp.json", ".venv", ".lyk", ".lyk-skills", ".no-lyk-config":
		return refuse("the first segment is reserved")
	}
	return clean, nil
}
func IsText(head []byte) bool {
	if len(head) > 8192 {
		head = head[:8192]
	}
	// A sniffed prefix may end partway through an otherwise valid rune.
	// Treat that incomplete suffix conservatively as text rather than allowing
	// an attachment to overwrite a text file or bypass its smaller size limit.
	for i := max(0, len(head)-utf8.UTFMax+1); i < len(head); i++ {
		if utf8.RuneStart(head[i]) && !utf8.FullRune(head[i:]) {
			head = head[:i]
			break
		}
	}
	return utf8.Valid(head) && !bytes.ContainsRune(head, 0)
}
func BlobKey(ws uuid.UUID, sha string) string { return ws.String() + "/coresearch/" + sha }
