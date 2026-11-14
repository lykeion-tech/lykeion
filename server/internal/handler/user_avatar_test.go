package handler

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func TestUserAvatarVisibleOnlyToWorkspaceColleagues(t *testing.T) {
	s := newServer(t)
	a, b, outsider := s.signIn(t, "avatar-a@lab.org"), s.signIn(t, "avatar-b@lab.org"), s.signIn(t, "avatar-c@lab.org")
	w := s.workspace(t, a, "avatar-lab")
	invitation := s.invite(t, a, w.ID, "avatar-b@lab.org", "member")
	if c := s.call(t, "POST", "/api/invitations/"+invitation+"/accept", b, "", nil, nil); c != 200 {
		t.Fatalf("accept: %d", c)
	}
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	avatar := "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
	if c := s.call(t, "PATCH", "/api/me", a, "", map[string]string{"avatar_url": avatar}, nil); c != 200 {
		t.Fatalf("update: %d", c)
	}
	var members []struct {
		UserID string `json:"user_id"`
		Avatar string `json:"avatar_url"`
	}
	if c := s.call(t, "GET", "/api/workspaces/"+w.ID+"/members", b, "", nil, &members); c != 200 {
		t.Fatalf("members: %d", c)
	}
	id := s.userID(t, a)
	found := false
	for _, m := range members {
		if m.UserID == id {
			found = m.Avatar == avatar
		}
	}
	if !found {
		t.Fatal("colleague did not receive avatar")
	}
	if c := s.call(t, "GET", "/api/workspaces/"+w.ID+"/members", outsider, "", nil, nil); c != 404 && c != 403 {
		t.Fatalf("outsider: %d", c)
	}
	for _, invalid := range []string{"https://example.test/photo.png", "data:image/svg+xml;base64,PHN2Zz4=", "data:image/png;base64,SGVsbG8="} {
		if c := s.call(t, "PATCH", "/api/me", a, "", map[string]string{"avatar_url": invalid}, nil); c != 400 {
			t.Fatalf("invalid avatar accepted: %d", c)
		}
	}
}
