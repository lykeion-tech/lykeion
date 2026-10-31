package handler

import (
	"encoding/json"
	"lykeion/server/internal/testutil"
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestEmojiAllowlistMatchesPicker(t *testing.T) {
	data, err := os.ReadFile("../../../apps/dashboard/src/data/emoji-data.ts")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(data), "const data:EmojiRecord[]=", 2)
	if len(parts) != 2 {
		t.Fatal("emoji catalogue missing")
	}
	raw := strings.SplitN(parts[1], ";\nexport default", 2)[0]
	var records []struct {
		Emoji string  `json:"emoji"`
		Skins [][]any `json:"skins"`
	}
	if err := json.Unmarshal([]byte(raw), &records); err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		glyphs := []string{record.Emoji}
		for _, skin := range record.Skins {
			glyphs = append(glyphs, skin[1].(string))
		}
		for _, glyph := range glyphs {
			if !validEmojiAvatar("emoji:" + glyph) {
				t.Errorf("picker glyph rejected: %q", glyph)
			}
		}
	}
	picker, err := os.ReadFile("../../../apps/dashboard/src/components/AvatarPicker.vue")
	if err != nil {
		t.Fatal(err)
	}
	parts = strings.SplitN(string(picker), "const quick = [", 2)
	if len(parts) != 2 {
		t.Fatal("quick picker missing")
	}
	quick := strings.SplitN(parts[1], "];", 2)[0]
	for _, match := range regexp.MustCompile(`'([^']+)'`).FindAllStringSubmatch(quick, -1) {
		if !validEmojiAvatar("emoji:" + match[1]) {
			t.Errorf("quick picker glyph rejected: %q", match[1])
		}
	}
}

func TestEmojiAvatarValidation(t *testing.T) {
	for _, s := range []string{"", "emoji:🧪", "emoji:👩🏽‍🔬", "emoji:🇩🇰", "emoji:1️⃣", "emoji:⭐", "emoji:⚡", "emoji:ℹ️"} {
		if !validEmojiAvatar(s) {
			t.Errorf("rejected %q", s)
		}
	}
	for _, s := range []string{"emoji:", "hello", "emoji:hello", "emoji:🐙\n", "emoji:" + strings.Repeat("🐙", 33)} {
		if validEmojiAvatar(s) {
			t.Errorf("accepted %q", s)
		}
	}
}

func TestAgentAndTeamEmojiIcons(t *testing.T) {
	s := newServer(t)
	owner := s.signIn(t, "icons@lab.org")
	w := s.workspace(t, owner, "icons")
	colleague := s.member(t, owner, w.ID, "colleague@lab.org", "member")
	var a map[string]any
	if c := s.call(t, "POST", "/api/agents", owner, w.ID, map[string]any{"name": "Icon agent", "harness": "claude", "visibility": "workspace"}, &a); c != 201 {
		t.Fatalf("create agent: %d", c)
	}
	path := "/api/agents/" + a["id"].(string)
	if c := s.call(t, "PATCH", path, owner, w.ID, map[string]any{"avatar_url": "emoji:🧪"}, &a); c != 200 || a["avatar_url"] != "emoji:🧪" {
		t.Fatalf("save agent icon: %d %+v", c, a)
	}
	var read map[string]any
	if c := s.call(t, "GET", path, colleague, w.ID, nil, &read); c != 200 || read["avatar_url"] != "emoji:🧪" {
		t.Fatalf("read agent icon: %d %+v", c, read)
	}
	if c := s.call(t, "PATCH", path, colleague, w.ID, map[string]any{"avatar_url": "emoji:🐙"}, nil); c != 403 {
		t.Fatalf("nonowner: %d", c)
	}
	if c := s.call(t, "PATCH", path, owner, w.ID, map[string]any{"avatar_url": "emoji:hello"}, nil); c != 400 {
		t.Fatalf("invalid agent icon: %d", c)
	}
	if c := s.call(t, "PATCH", path, owner, w.ID, map[string]any{"avatar_url": ""}, &a); c != 200 || a["avatar_url"] != "" {
		t.Fatalf("reset agent icon: %d %+v", c, a)
	}
	if c := s.call(t, "POST", path+"/archive", owner, w.ID, nil, nil); c != 200 {
		t.Fatalf("archive: %d", c)
	}
	if c := s.call(t, "PATCH", path, owner, w.ID, map[string]any{"avatar_url": "emoji:🐙"}, nil); c != 409 {
		t.Fatalf("archived agent: %d", c)
	}
	var team struct{ ID string }
	s.installPackFixture(t, owner, w.ID, testutil.Pack(t, testutil.PackOpts{Name: "icon-team"}), &team)
	tpath := "/api/teams/" + team.ID
	if c := s.call(t, "PATCH", tpath, owner, w.ID, map[string]any{"avatar_url": "emoji:🧪"}, nil); c != 200 {
		t.Fatalf("save team icon: %d", c)
	}
	if c := s.call(t, "GET", tpath, owner, w.ID, nil, &read); c != 200 || read["avatar_url"] != "emoji:🧪" {
		t.Fatalf("read team icon: %d %+v", c, read)
	}
	var teams []map[string]any
	if c := s.call(t, "GET", "/api/teams", owner, w.ID, nil, &teams); c != 200 || len(teams) != 1 || teams[0]["avatar_url"] != "emoji:🧪" {
		t.Fatalf("list team icon: %d %+v", c, teams)
	}
	if n := s.count(t, "SELECT count(*) FROM event WHERE type = 'team.updated' AND payload = '{}'::jsonb"); n != 1 {
		t.Fatalf("private Team update emitted %d non-identifying events", n)
	}
	if c := s.call(t, "PATCH", tpath, colleague, w.ID, map[string]any{"avatar_url": "emoji:🐙"}, nil); c != 404 {
		t.Fatalf("private team: %d", c)
	}
	if c := s.call(t, "PATCH", tpath, owner, w.ID, map[string]any{"avatar_url": ""}, nil); c != 200 {
		t.Fatalf("reset team icon: %d", c)
	}
	if c := s.call(t, "GET", tpath, owner, w.ID, nil, &read); c != 200 || read["avatar_url"] != "" {
		t.Fatalf("read reset Team icon: %d %+v", c, read)
	}
}
