package handler

import (
	"reflect"
	"slices"
	"testing"

	"lykeion/server/internal/pack"
	"lykeion/server/internal/testutil"
)

func TestTeamSkillsCreatePrivateVersionAndPreserveOriginal(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "skill-editor@lab.org")
	w := s.workspace(t, a, "skills")
	b := s.member(t, a, w.ID, "colleague@lab.org", "member")
	var source struct{ ID string }
	s.installPackFixture(t, a, w.ID, testutil.Pack(t, testutil.PackOpts{Name: "editable-team"}), &source)
	var made struct{ ID, Version string }
	if c := s.call(t, "POST", "/api/teams/"+source.ID+"/skills", a, w.ID, map[string]any{"member": "pi", "skills": []string{"aeon"}}, &made); c != 201 {
		t.Fatalf("save skills: %d", c)
	}
	if made.ID == source.ID || made.Version != "2" {
		t.Fatalf("version: %+v", made)
	}
	var old, updated struct{ Manifest pack.Manifest }
	s.call(t, "GET", "/api/teams/"+source.ID, a, w.ID, nil, &old)
	s.call(t, "GET", "/api/teams/"+made.ID, a, w.ID, nil, &updated)
	if slices.Contains(old.Manifest.Leader.Skills, "aeon") || !slices.Equal(updated.Manifest.Leader.Skills, []string{"aeon"}) {
		t.Fatal("original changed or selection not saved")
	}
	if !reflect.DeepEqual(updated.Manifest.Auditor.Agent, old.Manifest.Auditor.Agent) || !reflect.DeepEqual(updated.Manifest.Leader.Agent, old.Manifest.Leader.Agent) {
		t.Fatal("agent definitions changed")
	}
	if c := s.call(t, "GET", "/api/teams/"+made.ID, b, w.ID, nil, nil); c != 404 {
		t.Fatalf("private version visible: %d", c)
	}
	if got := s.storeList(t, a, w.ID, ""); len(got) != 1 || got[0].ID != made.ID || !got[0].Added {
		t.Fatalf("new version not added: %+v", got)
	}
	if c := s.call(t, "POST", "/api/teams/"+source.ID+"/skills", a, w.ID, map[string]any{"member": "pi", "skills": []string{}}, nil); c != 409 {
		t.Fatalf("stale edit: %d", c)
	}
	if c := s.call(t, "POST", "/api/teams/"+made.ID+"/skills", a, w.ID, map[string]any{"member": "pi", "skills": []string{"nonexistent-skill"}}, nil); c != 400 {
		t.Fatalf("unavailable skill: %d", c)
	}
	if c := s.call(t, "PUT", "/api/availability/skill/bundle:anndata", a, w.ID, map[string]any{"enabled": false, "revision": 0}, nil); c != 200 {
		t.Fatalf("disable skill: %d", c)
	}
	if c := s.call(t, "POST", "/api/teams/"+made.ID+"/skills", a, w.ID, map[string]any{"member": "pi", "skills": []string{"anndata"}}, nil); c != 409 {
		t.Fatalf("disabled skill: %d", c)
	}
	if c := s.call(t, "POST", "/api/teams/"+made.ID+"/skills", a, w.ID, map[string]any{"member": "unknown", "skills": []string{}}, nil); c != 400 {
		t.Fatalf("unknown member: %d", c)
	}
}
