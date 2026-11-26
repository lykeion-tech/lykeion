package records

import (
	"io/fs"
	"path"
	"strings"
)

const (
	MaxOutputNameBytes   = 512
	MaxScriptOutputPaths = 16
)

// ValidScriptOutputPath accepts a canonical JSON output path as stored in
// both a script execution manifest and the study's uploaded files.
func ValidScriptOutputPath(name string) bool {
	return len(name) <= MaxOutputNameBytes && fs.ValidPath(name) &&
		strings.HasPrefix(name, "outputs/") && !strings.ContainsAny(name, "\\\x00") &&
		strings.EqualFold(path.Ext(name), ".json")
}
