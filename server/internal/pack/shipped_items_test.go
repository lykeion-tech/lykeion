package pack_test

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"

	"lykeion/server/internal/pack"
	"lykeion/server/internal/review"
)

// The reviewed stage Teams declare typed items instead of carrying criteria
// of their own, and every one-line example in their instructions or skills is
// accepted in the Team's column.
func TestShippedTeamsHaveNoCriteria(t *testing.T) {
	put := regexp.MustCompile(`lyk record put (\w+)(?: --cites \S+)? --content '([^']*)'`)
	kinds := map[string][]string{
		"Grounding Team":       {"paper", "no_qualifying"},
		"Hypotheses Team":      {"hypothesis"},
		"Experimentation Team": {"outputs"},
	}
	for name, want := range kinds {
		_, p, err := pack.ReadShipped(pack.Shipped(), name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		m := p.Manifest
		if len(m.Criteria) != 0 {
			t.Errorf("%s still has criteria: %v", name, m.Criteria)
		}
		text := m.Leader.Agent.Instructions
		for _, mem := range m.Members {
			text += "\n" + mem.Agent.Instructions
		}
		// The skills the Team ships with are read by the same agents.
		var skills string
		err = fs.WalkDir(pack.Shipped(), name+"/skills", func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			body, err := fs.ReadFile(pack.Shipped(), path)
			skills += "\n" + string(body)
			return err
		})
		if err != nil {
			t.Fatalf("%s skills: %v", name, err)
		}
		for _, k := range want {
			if !strings.Contains(text, "lyk record put "+k) {
				t.Errorf("%s never tells an agent to write %s", name, k)
			}
		}
		for _, ex := range put.FindAllStringSubmatch(text+skills, -1) {
			if err := review.Validate(ex[1], m.Stage, []byte(ex[2])); err != nil {
				t.Errorf("%s: the example %s does not validate: %v", name, ex[0], err)
			}
		}
	}
}
