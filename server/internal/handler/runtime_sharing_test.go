package handler

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestSharingIsCheckedAndCleaned(t *testing.T) {
	f := runtimeSkillSetup(t)
	if _, err := f.s.Env.Owner.Exec(context.Background(), "UPDATE runtime SET os='linux' WHERE id=$1", f.rt); err != nil {
		t.Fatal(err)
	}
	path := "/api/runtimes/" + f.rt + "/sharing"
	for _, tc := range []struct {
		v string
		s []string
		c int
	}{{"private", []string{uuid.NewString()}, 400}, {"workspace", []string{uuid.NewString()}, 400}, {"private", nil, 200}, {"workspace", nil, 200}} {
		var body map[string]any
		if got := f.s.call(t, "PUT", path, f.jwt, f.ws, map[string]any{"visibility": tc.v, "shared_with": tc.s}, &body); got != tc.c {
			t.Fatalf("sharing %s: %d %+v", tc.v, got, body)
		}
		if tc.v == "workspace" && len(tc.s) > 0 && body["error"] != "A machine shared with everyone has no list of colleagues" {
			t.Fatalf("message: %+v", body)
		}
	}
}
func TestRuntimeListShowsSharing(t *testing.T) {
	f := runtimeSkillSetup(t)
	var rows []map[string]any
	if got := f.s.call(t, "GET", "/api/runtimes", f.jwt, f.ws, nil, &rows); got != 200 || len(rows) != 1 {
		t.Fatalf("list %d %+v", got, rows)
	}
	if rows[0]["visibility"] != "private" || rows[0]["shared_with"] == nil {
		t.Fatalf("sharing fields %+v", rows)
	}
}
func TestOnlyTheOwnerShares(t *testing.T) {
	f := runtimeSkillSetup(t)
	for _, role := range []string{"admin", "member"} {
		jwt := f.s.signIn(t, role+"-sharing@lab.org")
		invite := f.s.invite(t, f.jwt, f.ws, role+"-sharing@lab.org", role)
		if got := f.s.call(t, "POST", "/api/invitations/"+invite+"/accept", jwt, "", nil, nil); got != 200 {
			t.Fatal(got)
		}
		if got := f.s.call(t, "PUT", "/api/runtimes/"+f.rt+"/sharing", jwt, f.ws, map[string]any{"visibility": "invalid"}, nil); got != 404 {
			t.Fatalf("%s: %d", role, got)
		}
	}
}

func TestOnlyLinuxSharingHasPlatformError(t *testing.T) {
	f := runtimeSkillSetup(t)
	for _, platform := range []string{"darwin", "", "windows", "linux"} {
		if _, err := f.s.Env.Owner.Exec(t.Context(), "UPDATE runtime SET os=$1 WHERE id=$2", platform, f.rt); err != nil {
			t.Fatal(err)
		}
		var body map[string]any
		got := f.s.call(t, "PUT", "/api/runtimes/"+f.rt+"/sharing", f.jwt, f.ws, map[string]any{"visibility": "workspace"}, &body)
		want := 400
		if platform == "linux" {
			want = 200
		}
		if got != want || (want == 400 && body["error"] != "Shared runs require Linux. On a Mac, register a Linux VM as a separate machine.") {
			t.Fatalf("%s: %d %+v", platform, got, body)
		}
	}
}
