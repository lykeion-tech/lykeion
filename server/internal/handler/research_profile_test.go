package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	db "lykeion/server/pkg/db/generated"
)

func TestResearchProfilePublicationLifecycle(t *testing.T) {
	s := newServer(t)
	researcher := s.signIn(t, "researcher@lab.org")
	colleague := s.signIn(t, "colleague@lab.org")
	outsider := s.signIn(t, "outsider@lab.org")
	id := s.userID(t, researcher)
	first := s.workspace(t, colleague, "first-lab")
	second := s.workspace(t, researcher, "second-lab")
	third := s.workspace(t, colleague, "third-lab")
	const account = "/api/me/research-profile"
	share := func(workspace ws) string { return "/api/workspaces/" + workspace.ID + "/research-profile" }
	member := func(workspace ws) string {
		return "/api/workspaces/" + workspace.ID + "/members/" + id + "/research-profile"
	}
	check := func(method, path, token string, body any, status int, dst any) {
		t.Helper()
		if got := s.call(t, method, path, token, "", body, dst); got != status {
			t.Fatalf("%s %s returned %d, want %d", method, path, got, status)
		}
	}
	join := func(workspace ws, owner, token, email string) {
		t.Helper()
		invite := s.invite(t, owner, workspace.ID, email, "member")
		check("POST", "/api/invitations/"+invite+"/accept", token, nil, http.StatusOK, nil)
	}

	checkAccount := func(method, path, token string, body any, draft, published string, count int) accountResearchProfile {
		t.Helper()
		var got accountResearchProfile
		check(method, path, token, body, http.StatusOK, &got)
		checkResearchProfileBackground(t, got.Draft, draft)
		checkResearchProfileBackground(t, got.Published, published)
		if got.WorkspaceIDs == nil || len(got.WorkspaceIDs) != count {
			t.Fatalf("workspace grants = %v, want %d", got.WorkspaceIDs, count)
		}
		return got
	}
	checkMember := func(workspace ws, token, want string) {
		t.Helper()
		var got struct {
			Profile json.RawMessage `json:"profile"`
		}
		check("GET", member(workspace), token, nil, http.StatusOK, &got)
		checkResearchProfileBackground(t, got.Profile, want)
	}

	check("GET", account, "", nil, http.StatusUnauthorized, nil)
	checkAccount("GET", account, researcher, nil, "", "", 0)
	check("POST", account+"/publish", researcher, nil, http.StatusBadRequest, nil)
	missingRevision := strings.Repeat("0", 64)
	audience := func(revision string, workspaces ...ws) map[string]any {
		var ids []string
		for _, workspace := range workspaces {
			ids = append(ids, workspace.ID)
		}
		return map[string]any{"workspace_ids": ids, "draft_revision": revision}
	}
	preview := func(revision string) map[string]string { return map[string]string{"profile_revision": revision} }
	check("POST", account+"/publish", researcher, audience(missingRevision, first), http.StatusConflict, nil)
	check("PUT", share(first), researcher, preview(missingRevision), http.StatusNotFound, nil)
	join(first, colleague, researcher, "researcher@lab.org")
	join(second, researcher, colleague, "colleague@lab.org")
	join(third, colleague, researcher, "researcher@lab.org")
	check("PUT", share(first), researcher, preview(missingRevision), http.StatusConflict, nil)
	var blank accountResearchProfile
	check("PUT", account, researcher, map[string]string{}, http.StatusOK, &blank)
	check("PUT", share(first), researcher, preview(*blank.DraftRevision), http.StatusConflict, nil)
	draft := map[string]any{"background": "  Statistical genetics  ", "topics": "Genomics", "methods": "GWAS", "links": []string{"https://example.org/work"}}
	firstDraft := checkAccount("PUT", account, researcher, draft, "Statistical genetics", "", 0)
	checkAccount("GET", account, colleague, nil, "", "", 0)
	check("POST", account+"/publish", researcher, audience(*firstDraft.DraftRevision, first), http.StatusConflict, nil)
	checkMember(first, colleague, "")
	checkAccount("PUT", account+"/save", researcher, map[string]string{"background": "Still private"}, "Still private", "", 0)
	checkMember(first, colleague, "")
	check("PUT", member(first), colleague, draft, http.StatusMethodNotAllowed, nil)
	firstDraft = checkAccount("PUT", account, researcher, draft, "Statistical genetics", "", 0)
	check("PUT", share(first), colleague, preview(*firstDraft.DraftRevision), http.StatusConflict, nil)
	check("PUT", share(first), researcher, preview(*blank.DraftRevision), http.StatusConflict, nil)
	firstSnapshot := checkAccount("PUT", share(first), researcher, preview(*firstDraft.DraftRevision), "Statistical genetics", "Statistical genetics", 1)
	checkMember(first, colleague, "Statistical genetics")
	checkMember(second, colleague, "")

	draft["background"] = "Ancient DNA"
	secondDraft := checkAccount("PUT", account, researcher, draft, "Ancient DNA", "Statistical genetics", 1)
	checkMember(first, colleague, "Statistical genetics")
	check("PUT", share(second), researcher, preview(*secondDraft.DraftRevision), http.StatusConflict, nil)
	checkAccount("PUT", share(second), researcher, preview(*firstSnapshot.PublishedRevision), "Ancient DNA", "Statistical genetics", 2)
	checkMember(second, colleague, "Statistical genetics")
	checkMember(third, colleague, "")
	check("GET", member(first), outsider, nil, http.StatusNotFound, nil)
	for _, tc := range []struct {
		workspace ws
		token     string
		visible   int
		directory int
	}{
		{first, colleague, 1, 2},
		{third, colleague, 0, 2},
		{first, outsider, 0, 0},
	} {
		t.Run(tc.workspace.Slug+fmt.Sprint(tc.visible, tc.directory), func(t *testing.T) {
			ctx := t.Context()
			tx, err := s.Env.App.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			if _, err = tx.Exec(ctx, `SELECT set_config('app.workspace_id', $1, true), set_config('app.user_id', $2, true)`,
				tc.workspace.ID, s.userID(t, tc.token)); err != nil {
				t.Fatal(err)
			}
			var drafts, snapshots, grants int
			if err := tx.QueryRow(ctx, `SELECT
				(SELECT count(*) FROM research_profile_draft WHERE user_id = $1),
				(SELECT count(*) FROM research_profile_snapshot WHERE user_id = $1),
				(SELECT count(*) FROM research_profile_publication WHERE user_id = $1)`, id).Scan(&drafts, &snapshots, &grants); err != nil {
				t.Fatal(err)
			}
			if drafts != 0 || snapshots != tc.visible || grants != tc.visible {
				t.Fatalf("RLS gives drafts=%d snapshots=%d grants=%d, want 0/%d/%d", drafts, snapshots, grants, tc.visible, tc.visible)
			}
			tag, err := tx.Exec(ctx, `UPDATE research_profile_snapshot SET profile = '{}' WHERE user_id = $1`, id)
			if err != nil || tag.RowsAffected() != 0 {
				t.Fatalf("peer updated snapshot: %s, %v", tag, err)
			}
			query := db.ListResearchDirectoryParams{WorkspaceID: uuid.MustParse(tc.workspace.ID), Limit: 10}
			rows, err := db.New(tx).ListResearchDirectory(ctx, query)
			if err != nil || len(rows) != tc.directory {
				t.Fatalf("directory size=%d, want %d (%v)", len(rows), tc.directory, err)
			}
			query.WorkspaceID = uuid.MustParse(second.ID)
			rows, err = db.New(tx).ListResearchDirectory(ctx, query)
			if err != nil || len(rows) != 0 {
				t.Fatalf("directory visible across workspace scopes: %d (%v)", len(rows), err)
			}
		})
	}
	check("POST", account+"/publish", researcher, audience(*secondDraft.DraftRevision, first), http.StatusConflict, nil)
	check("POST", account+"/publish", researcher, audience(*secondDraft.DraftRevision, first, first), http.StatusBadRequest, nil)
	check("POST", account+"/publish", researcher, audience(*secondDraft.DraftRevision, first, third), http.StatusConflict, nil)
	check("POST", account+"/publish", researcher, audience(*firstDraft.DraftRevision, second, first), http.StatusConflict, nil)
	checkMember(first, colleague, "Statistical genetics")
	checkMember(second, colleague, "Statistical genetics")
	secondSnapshot := checkAccount("POST", account+"/publish", researcher, audience(*secondDraft.DraftRevision, second, first), "Ancient DNA", "Ancient DNA", 2)
	checkMember(first, colleague, "Ancient DNA")
	checkMember(second, colleague, "Ancient DNA")
	checkAccount("DELETE", share(first), researcher, nil, "Ancient DNA", "Ancient DNA", 1)
	checkMember(first, colleague, "")
	checkMember(second, colleague, "Ancient DNA")
	check("PUT", share(first), researcher, preview(*firstSnapshot.PublishedRevision), http.StatusConflict, nil)
	checkAccount("PUT", share(first), researcher, preview(*secondSnapshot.PublishedRevision), "Ancient DNA", "Ancient DNA", 2)

	var profileEvents, unexpectedEvents int
	err := s.Env.Owner.QueryRow(t.Context(), `SELECT count(*),
		count(*) FILTER (WHERE payload <> jsonb_build_object('user_id', $1::text))
		FROM event WHERE type = 'research_profile.updated'`, id).Scan(&profileEvents, &unexpectedEvents)
	if err != nil || profileEvents != 6 || unexpectedEvents != 0 {
		t.Fatalf("profile events=%d unexpected=%d (%v)", profileEvents, unexpectedEvents, err)
	}

	check("DELETE", "/api/workspaces/"+first.ID+"/members/"+id, colleague, nil, http.StatusNoContent, nil)
	check("GET", member(first), colleague, nil, http.StatusNotFound, nil)
	check("GET", member(first), researcher, nil, http.StatusNotFound, nil)
	checkAccount("GET", account, researcher, nil, "Ancient DNA", "Ancient DNA", 1)
	join(first, colleague, researcher, "researcher@lab.org")
	checkMember(first, colleague, "")
	checkMember(second, colleague, "Ancient DNA")
	checkAccount("PUT", share(first), researcher, preview(*secondSnapshot.PublishedRevision), "Ancient DNA", "Ancient DNA", 2)
	checkAccount("PUT", account+"/save", researcher, map[string]string{"background": "Updated expertise"}, "Updated expertise", "Updated expertise", 2)
	checkMember(first, colleague, "Updated expertise")
	checkMember(second, colleague, "Updated expertise")
	checkMember(third, colleague, "")
	checkAccount("DELETE", share(first), researcher, nil, "Updated expertise", "Updated expertise", 1)
	checkAccount("PUT", account+"/save", researcher, map[string]string{"background": "After withdrawal"}, "After withdrawal", "After withdrawal", 1)
	checkMember(first, colleague, "")
	checkMember(second, colleague, "After withdrawal")
}

func checkResearchProfileBackground(t *testing.T, raw json.RawMessage, want string) {
	t.Helper()
	if want == "" {
		if string(raw) != "null" {
			t.Fatalf("profile = %s, want null", raw)
		}
		return
	}
	var profile struct{ Background string }
	if err := json.Unmarshal(raw, &profile); err != nil || profile.Background != want {
		t.Fatalf("profile = %s, want background %q (%v)", raw, want, err)
	}
}

func TestResearchProfileFreeFormIdentity(t *testing.T) {
	s := newServer(t)
	token := s.signIn(t, "identity@lab.org")
	workspace := s.workspace(t, token, "identity-lab")
	const account = "/api/me/research-profile"
	var state accountResearchProfile
	legacy := map[string]any{
		"background": "Field researcher", "topics": "Navigation", "methods": "Observation",
		"collaboration": "Open to review", "links": []string{"https://example.org/work"},
	}
	if code := s.call(t, "PUT", account, token, "", legacy, &state); code != 200 {
		t.Fatalf("save legacy profile: %d", code)
	}
	var profile struct {
		Identity string   `json:"identity"`
		Links    []string `json:"links"`
	}
	if err := json.Unmarshal(state.Draft, &profile); err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{"Field researcher", "Navigation", "Observation", "Open to review"} {
		if !strings.Contains(profile.Identity, part) {
			t.Fatalf("identity lost %q: %q", part, profile.Identity)
		}
	}
	if len(profile.Links) != 1 || profile.Links[0] != "https://example.org/work" {
		t.Fatalf("links not preserved: %v", profile.Links)
	}
	const identity = "I design field studies.\n\nI can help with observation protocols."
	if code := s.call(t, "PUT", account+"/save", token, "", map[string]any{"identity": identity, "links": profile.Links}, &state); code != 200 {
		t.Fatalf("save identity: %d", code)
	}
	if string(state.Published) != "null" || len(state.WorkspaceIDs) != 0 {
		t.Fatal("saving an identity unexpectedly published it")
	}
	if code := s.call(t, "PUT", "/api/workspaces/"+workspace.ID+"/research-profile", token, "", map[string]any{"profile_revision": state.DraftRevision}, &state); code != 200 {
		t.Fatalf("share free-form identity: %d", code)
	}
	if err := json.Unmarshal(state.Published, &profile); err != nil || profile.Identity != identity {
		t.Fatalf("published identity = %q (%v)", profile.Identity, err)
	}
	checkResearchProfileBackground(t, state.Published, identity)
}

func TestResearchProfileValidatesDraft(t *testing.T) {
	s := newServer(t)
	token := s.signIn(t, "profile-validation@lab.org")
	for i, body := range []any{
		json.RawMessage("null"),
		map[string]any{"background": "Text", "user_id": uuid.New().String()},
		map[string]any{"background": strings.Repeat("x", 10_000)},
		map[string]any{"identity": strings.Repeat("x", 4001)},
		map[string]any{"identity": "text\x00text"},
		map[string]any{"methods": "a\u0000b"},
		map[string]any{"links": []string{"javascript:alert(1)"}},
		map[string]any{"links": []string{"//example.org/work"}},
		map[string]any{"links": []string{"https://user:password@example.org/work"}},
		map[string]any{"links": []string{"https://example.org/work", " "}},
	} {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			if got := s.call(t, "PUT", "/api/me/research-profile", token, "", body, nil); got != http.StatusBadRequest {
				t.Fatalf("HTTP status = %d, want %d", got, http.StatusBadRequest)
			}
		})
	}
}

func TestResearchProfileAudienceConcurrentShare(t *testing.T) {
	s := newServer(t)
	token := s.signIn(t, "profile-race@lab.org")
	first := s.workspace(t, token, "profile-race-first")
	second := s.workspace(t, token, "profile-race-second")
	const account = "/api/me/research-profile"
	share := func(workspace ws, revision string) int {
		return s.call(t, "PUT", "/api/workspaces/"+workspace.ID+"/research-profile", token, "", map[string]string{"profile_revision": revision}, nil)
	}
	save := func(background string) accountResearchProfile {
		t.Helper()
		var result accountResearchProfile
		if got := s.call(t, "PUT", account, token, "", map[string]string{"background": background}, &result); got != http.StatusOK {
			t.Fatalf("save: %d", got)
		}
		return result
	}
	initial := save("Published")
	if got := share(first, *initial.DraftRevision); got != http.StatusOK {
		t.Fatalf("initial share: %d", got)
	}
	edited := save("Edited")
	status := raceCalls(t, func() int {
		return s.call(t, "POST", account+"/publish", token, "", map[string]any{
			"workspace_ids": []string{first.ID}, "draft_revision": edited.DraftRevision,
		}, nil)
	}, func() int {
		return share(second, *edited.PublishedRevision)
	})
	want, workspaces := "Published", 2
	if status[0] == http.StatusOK && status[1] == http.StatusConflict {
		want, workspaces = "Edited", 1
	} else if status[0] != http.StatusConflict || status[1] != http.StatusOK {
		t.Fatalf("publish and share statuses: %v", status)
	}
	var result accountResearchProfile
	if got := s.call(t, "GET", account, token, "", nil, &result); got != http.StatusOK || len(result.WorkspaceIDs) != workspaces {
		t.Fatalf("account: %d, workspaces: %v", got, result.WorkspaceIDs)
	}
	checkResearchProfileBackground(t, result.Published, want)
}
