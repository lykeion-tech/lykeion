package science

import (
	"bytes"
	"errors"
	"fmt"

	"go.yaml.in/yaml/v3"
)

// Frontmatter is the part of a SKILL.md header Lykeion reads.
type Frontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	License     string `yaml:"license"`
}

// ParseFrontmatter reads a SKILL.md's YAML header and returns it with the
// body that follows the closing line.
func ParseFrontmatter(b []byte) (Frontmatter, []byte, error) {
	var fm Frontmatter
	rest, ok := bytes.CutPrefix(b, []byte("---\n"))
	if !ok {
		return fm, nil, errors.New("SKILL.md does not start with a frontmatter block")
	}
	i := bytes.Index(rest, []byte("\n---\n"))
	if i < 0 {
		return fm, nil, errors.New("SKILL.md's frontmatter block is never closed")
	}
	if err := yaml.Unmarshal(rest[:i], &fm); err != nil {
		return fm, nil, fmt.Errorf("frontmatter: %w", err)
	}
	return fm, rest[i+len("\n---\n"):], nil
}
