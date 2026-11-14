// Package skills holds the role skills the server hands to daemons.
package skills

import (
	"embed"
	"path"
	"strings"
)

//go:embed autoresearch
var FS embed.FS

// Role returns a role's SKILL.md, or "" when the role has none. Parallel
// copies of one role (generator-1, generator-2…) share that role's skill.
func Role(column, role string) string {
	b, err := FS.ReadFile(path.Join(column, role, "SKILL.md"))
	if base := strings.TrimRight(role, "0123456789"); err != nil && base != role && strings.HasSuffix(base, "-") {
		b, err = FS.ReadFile(path.Join(column, strings.TrimSuffix(base, "-"), "SKILL.md"))
	}
	if err != nil {
		return ""
	}
	return string(b)
}
