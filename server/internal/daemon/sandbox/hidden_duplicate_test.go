package sandbox

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDuplicateHiddenPathsResolveOnce(t *testing.T) {
	for _, exists := range []bool{false, true} {
		t.Run(map[bool]string{false: "absent", true: "existing"}[exists], func(t *testing.T) {
			home := t.TempDir()
			secret := filepath.Join(home, "secret")
			if exists {
				if err := os.WriteFile(secret, []byte("fixture"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			r, err := (Policy{Home: home, Hidden: []string{secret, secret}}).resolve()
			if err != nil {
				t.Fatal(err)
			}
			if len(r.hidden) != 1 {
				t.Fatalf("duplicate hidden path not deduplicated: %v", r.hidden)
			}
		})
	}
}
