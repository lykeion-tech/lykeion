// Package skills holds the role skills the server hands to daemons and the
// entry skill `lyk install` copies into a project.
package skills

import (
	"embed"
	"path"
)

//go:embed lyk grounding
var FS embed.FS

// Role returns a role's SKILL.md, or "" when the role has none.
func Role(column, role string) string {
	b, err := FS.ReadFile(path.Join(column, role, "SKILL.md"))
	if err != nil {
		return ""
	}
	return string(b)
}
