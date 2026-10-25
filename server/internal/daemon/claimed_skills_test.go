package daemon

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"lykeion/server/internal/toolroute"
	"lykeion/server/pkg/agent"
)

func skillFolder(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		tw.Write([]byte(body))
	}
	tw.Close()
	return buf.Bytes()
}

// A Team member's own skills are where its CLI reads skills before it starts.
func TestClaimedSkillsArePlacedWhereEachCLIReadsThem(t *testing.T) {
	for _, harness := range []string{"claude", "codex"} {
		dir := t.TempDir()
		skills := []toolroute.RoutedSkill{{Name: "claim-check", Folder: skillFolder(t, map[string]string{"SKILL.md": "---\nname: claim-check\ndescription: Check a claim.\n---\nCheck it.", "notes/how.md": "how"})}}
		if err := placeClaimedSkills(harness, dir, skills); err != nil {
			t.Fatal(err)
		}
		root, err := agent.SkillsDir(harness, dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range []string{"SKILL.md", "notes/how.md"} {
			if _, err := os.Stat(filepath.Join(root, agent.PinnedName("claim-check"), f)); err != nil {
				t.Errorf("%s: %s not placed: %v", harness, f, err)
			}
		}
	}
	if err := placeClaimedSkills("claude", t.TempDir(), []toolroute.RoutedSkill{{Name: "../out", Folder: skillFolder(t, map[string]string{"SKILL.md": "x"})}}); err == nil {
		t.Error("a skill name that leaves the skills folder was placed")
	}
}

// Claude Code watches its skills folder for new skills only when the folder
// exists as it starts, so a run with no skills of its own still gets one:
// skills routed to it later must be loadable.
func TestARunWithNoSkillsStillHasTheFolderItsCLIWatches(t *testing.T) {
	for _, harness := range []string{"claude", "codex"} {
		dir := t.TempDir()
		if err := placeClaimedSkills(harness, dir, nil); err != nil {
			t.Fatal(err)
		}
		root, err := agent.SkillsDir(harness, dir)
		if err != nil {
			t.Fatal(err)
		}
		if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
			t.Errorf("%s: no skills folder before the CLI starts: %v", harness, err)
		}
	}
}

// The CLI lists a staged skill as lyk-<name>, so the run is told which of
// those is which skill its Team names.
func TestTheRunIsToldItsSkillsByTheNamesItsCLIShows(t *testing.T) {
	got := skillsNote([]toolroute.RoutedSkill{{Name: "claim-check"}, {Name: "evidence-brief"}})
	want := "\n\nYour skills are installed under these names: `lyk-claim-check` (the Team calls it claim-check), `lyk-evidence-brief` (the Team calls it evidence-brief). Use one when its task comes up."
	if got != want {
		t.Fatalf("note %q", got)
	}
	if skillsNote(nil) != "" {
		t.Fatal("a run with no skills is told about skills")
	}
}
