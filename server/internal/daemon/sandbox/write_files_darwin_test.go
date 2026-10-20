package sandbox

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSQLiteAndExactFilesConfineForReal(t *testing.T) {
	h := tempDir(t)
	dir := mkdir(t, filepath.Join(h, `codex.[x]+(state)with\slash`))
	p := Policy{Home: h, HomeRead: true, WriteSQLite: []string{dir}, WriteFiles: []string{filepath.Join(dir, "auth.json")}}
	for _, name := range []string{"auth.json", "state_5.sqlite", "state_5.sqlite-wal", "state_5.sqlite-shm", "state_5.sqlite-journal", "new_store_921.sqlite-wal"} {
		target := filepath.Join(dir, name)
		script := "printf x > " + shq(target) + " && rm " + shq(target) + " && printf y > " + shq(target)
		if out, err := sandboxed(t, p, script); err != nil {
			t.Fatalf("allowed %s: %v %s", name, err, out)
		}
		if b, err := os.ReadFile(target); err != nil || string(b) != "y" {
			t.Fatal(name, string(b), err)
		}
	}
	for _, name := range []string{"config.toml", "other.json", "state.sqlite", "state_x.sqlite", "state_5.sqlite.bak", "state_5.sqlite-wal-extra", "AGENTS.md"} {
		if out, err := sandboxed(t, p, "printf x > "+shq(filepath.Join(dir, name))); err == nil {
			t.Fatalf("unexpected write %s: %s", name, out)
		}
	}
	other := mkdir(t, filepath.Join(h, "codexX[x]+(state)"))
	if out, err := sandboxed(t, p, "printf x > "+shq(filepath.Join(other, "state_5.sqlite"))); err == nil {
		t.Fatalf("regex metachar escaped incorrectly: %s", out)
	}
	nested := mkdir(t, filepath.Join(dir, "nested"))
	if out, err := sandboxed(t, p, "printf x > "+shq(filepath.Join(nested, "state_5.sqlite"))); err == nil {
		t.Fatalf("nested match: %s", out)
	}
}
