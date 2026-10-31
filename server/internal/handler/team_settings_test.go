package handler

import (
	"errors"
	"github.com/google/uuid"
	"lykeion/server/internal/store"
	"lykeion/server/internal/team"
	"lykeion/server/internal/testutil"
	"testing"
)

func TestTeamSettingsProfileAndWorkspaceAccess(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "settings-owner@lab.org")
	w := s.workspace(t, a, "lab")
	b := s.member(t, a, w.ID, "settings-member@lab.org", "member")
	var up struct{ ID string }
	s.installPackFixture(t, a, w.ID, testutil.Pack(t, testutil.PackOpts{Name: "settings-team", Stage: "grounding"}), &up)
	path := "/api/teams/" + up.ID
	if c := s.call(t, "PATCH", path, a, w.ID, map[string]any{"name": "Renamed Team", "description": "A new description", "visibility": "workspace"}, nil); c != 200 {
		t.Fatalf("save: %d", c)
	}
	var detail struct {
		Manifest   struct{ Name, Description string }
		Visibility string
	}
	if c := s.call(t, "GET", path, b, w.ID, nil, &detail); c != 200 || detail.Manifest.Name != "Renamed Team" || detail.Manifest.Description != "A new description" || detail.Visibility != "workspace" {
		t.Fatalf("shared profile: %d %+v", c, detail)
	}
	found := false
	for _, row := range s.storeList(t, b, w.ID, "") {
		if row.ID == up.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("shared Team absent from list")
	}
	if c := s.call(t, "POST", path+"/add", b, w.ID, nil, nil); c != 200 {
		t.Fatalf("member add: %d", c)
	}
	if c := s.call(t, "PATCH", path, b, w.ID, map[string]string{"name": "stolen"}, nil); c != 403 {
		t.Fatalf("member edit: %d", c)
	}
	if c := s.call(t, "POST", path+"/skills", b, w.ID, map[string]any{"member": "pi", "skills": []string{}}, nil); c != 403 {
		t.Fatalf("member edits skills: %d", c)
	}
	if c := s.call(t, "PATCH", path, a, w.ID, map[string]string{"visibility": "public"}, nil); c != 400 {
		t.Fatalf("invalid visibility: %d", c)
	}
	if c := s.call(t, "DELETE", path, b, w.ID, nil, nil); c != 403 {
		t.Fatalf("member delete: %d", c)
	}
	if c := s.call(t, "PATCH", path, a, w.ID, map[string]string{"visibility": "private"}, nil); c != 200 {
		t.Fatalf("unshare: %d", c)
	}
	if c := s.call(t, "GET", path, b, w.ID, nil, nil); c != 404 {
		t.Fatalf("private Team visible: %d", c)
	}
	ws, memberID, teamID := uuid.MustParse(w.ID), uuid.MustParse(s.userID(t, b)), uuid.MustParse(up.ID)
	err := s.H.Store.InWorkspace(t.Context(), ws, memberID, func(tx *store.Tx) error { return team.Ensure(t.Context(), tx, memberID, teamID) })
	if !errors.Is(err, team.ErrNotAdded) {
		t.Fatalf("unshared Team remains usable: %v", err)
	}
}
