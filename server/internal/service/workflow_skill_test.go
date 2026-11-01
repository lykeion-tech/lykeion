package service

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
)

func claimWorkflowRun(t *testing.T, slug string) []Claimed {
	t.Helper()
	f := setup(t)
	ctx := context.Background()
	if slug != "" {
		if _, err := f.Env.Owner.Exec(ctx, "UPDATE card SET workflow = $1 WHERE id = $2", slug, f.Card); err != nil {
			t.Fatal(err)
		}
	}
	f.enqueue(t, 1, "claude")
	ref := f.runtime(t, f.Owner, "wf-runtime", "claude")
	got, err := f.T.Claim(ctx, f.Owner, []RuntimeRef{ref}, 1)
	if err != nil || len(got) != 1 {
		t.Fatalf("claim: %v, got %d runs", err, len(got))
	}
	return got
}

func workflowSkills(c Claimed) (names []string, folders [][]byte) {
	for _, s := range c.Skills {
		if strings.HasPrefix(s.Name, "workflow-") {
			names = append(names, s.Name)
			folders = append(folders, s.Folder)
		}
	}
	return
}

func TestAWorkflowStudysRunGetsItsSkill(t *testing.T) {
	got := claimWorkflowRun(t, "mendota-ice-phenology")
	names, folders := workflowSkills(got[0])
	if len(names) != 1 || names[0] != "workflow-mendota-ice-phenology" {
		t.Fatalf("workflow skills: %v", names)
	}
	tr := tar.NewReader(bytes.NewReader(folders[0]))
	hasSkill := false
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasSuffix(h.Name, "SKILL.md") {
			hasSkill = true
		}
		if strings.Contains(h.Name, "tests/") {
			t.Fatalf("tar holds %s", h.Name)
		}
	}
	if !hasSkill {
		t.Fatal("no SKILL.md in the skill folder")
	}
}

func TestAStudyWithoutAWorkflowGetsNone(t *testing.T) {
	if names, _ := workflowSkills(claimWorkflowRun(t, "")[0]); len(names) != 0 {
		t.Fatalf("workflow skills: %v", names)
	}
}

func TestADroppedWorkflowIsLeftOut(t *testing.T) {
	if names, _ := workflowSkills(claimWorkflowRun(t, "no-longer-released")[0]); len(names) != 0 {
		t.Fatalf("workflow skills: %v", names)
	}
}
