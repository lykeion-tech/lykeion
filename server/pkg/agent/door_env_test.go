package agent

import (
	"slices"
	"testing"
)

// The person's own LYK_DOOR and LYK_DOOR_TOKEN never reach a run; the ones
// the daemon gives the run do.
func TestChildEnvDropsAnInheritedDoor(t *testing.T) {
	t.Setenv("LYK_DOOR", "http://127.0.0.1:1/mcp")
	t.Setenv("LYK_DOOR_TOKEN", "inherited")
	env := childEnv(ExecOptions{}, "")
	for _, e := range env {
		if e == "LYK_DOOR=http://127.0.0.1:1/mcp" || e == "LYK_DOOR_TOKEN=inherited" {
			t.Fatalf("inherited %s", e)
		}
	}
	env = childEnv(ExecOptions{Env: []string{"LYK_DOOR_TOKEN=run"}}, "")
	if !slices.Contains(env, "LYK_DOOR_TOKEN=run") {
		t.Fatalf("the run's door token was dropped: %v", env)
	}
}
