package handler

import (
	"net/http"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// TestEveryWorkspaceRouteHidesOtherWorkspaces audits every registered API
// route that operates on workspace data. Workspace routes use A's workspace
// URL; card and inbox routes use B's scope with A's resource identifiers.
func TestWorkspaceRouteIsolation(t *testing.T) {
	s := newServer(t)
	a, b := s.signIn(t, "a@lab.org"), s.signIn(t, "b@else.org")
	wa := s.workspace(t, a, "lab-a")
	wb := s.workspace(t, b, "lab-b")
	var c card
	s.call(t, "POST", "/api/cards", a, wa.ID, map[string]any{"question": "q"}, &c)
	var item string
	if err := s.Env.Owner.QueryRow(t.Context(), `INSERT INTO inbox_item (workspace_id, card_id, user_id, kind, text)
		VALUES ($1, $2, $3, 'escalated', 'x') RETURNING id`, wa.ID, c.ID, s.userID(t, a)).Scan(&item); err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{"{id}": c.ID, "{user}": s.userID(t, a), "{inv}": c.ID, "{token}": "unknown-token"}

	checked := 0
	err := chi.Walk(NewRouter(s.H).(chi.Routes), func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if !strings.HasPrefix(route, "/api/") || strings.HasPrefix(route, "/api/tokens") ||
			route == "/api/me" || route == "/api/cli-token" || route == "/api/workspaces" ||
			strings.HasPrefix(route, "/api/invitations") || strings.HasPrefix(route, "/api/daemon") {
			return nil
		}

		path := route
		header := wb.ID
		if strings.HasPrefix(route, "/api/workspaces/{id}") {
			path = strings.Replace(path, "{id}", wa.ID, 1)
			header = ""
		} else if route == "/api/cards" || route == "/api/inbox" || route == "/api/runtimes" {
			// Collection routes have no resource id to probe, so scope B to A.
			header = wa.ID
		}
		if route == "/api/inbox/{id}/read" {
			path = strings.Replace(path, "{id}", item, 1)
		} else {
			for k, v := range ids {
				path = strings.ReplaceAll(path, k, v)
			}
		}
		path = strings.TrimSuffix(path, "/")
		var body any
		switch {
		case route == "/api/cards":
			body = map[string]any{"question": "outsider card"}
		case strings.HasSuffix(route, "/comments") && method == http.MethodPost:
			body = map[string]string{"body": "outsider comment"}
		case strings.HasSuffix(route, "/") && method == http.MethodPatch:
			body = map[string]string{"name": "outsider rename"}
		case route == "/api/runtimes/{id}" && method == http.MethodPatch:
			body = map[string]any{"max_concurrent": 2}
		case strings.HasSuffix(route, "/members/{user}") && method == http.MethodPatch:
			body = map[string]string{"role": "member"}
		case route == "/api/workspaces/{id}/invitations" && method == http.MethodPost:
			body = map[string]any{"email": "invite@example.org", "role": "member"}
		}
		code := s.call(t, method, path, b, header, body, nil)
		if code != http.StatusNotFound {
			t.Errorf("%s %s as an outsider returned %d, want 404", method, route, code)
		}
		checked++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked < 15 {
		t.Fatalf("only %d workspace routes checked", checked)
	}
	t.Logf("checked %d registered workspace routes", checked)

	// A member of B must also be unable to address A's card or inbox item while
	// supplying B's workspace scope. These checks catch ID-only lookups.
	for _, route := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/api/cards/" + c.ID, nil},
		{http.MethodGet, "/api/cards/" + c.ID + "/comments", nil},
		{http.MethodPost, "/api/cards/" + c.ID + "/comments", map[string]string{"body": "probe"}},
		{http.MethodGet, "/api/cards/" + c.ID + "/events", nil},
		{http.MethodPost, "/api/inbox/" + item + "/read", nil},
	} {
		if code := s.call(t, route.method, route.path, b, wb.ID, route.body, nil); code != http.StatusNotFound {
			t.Errorf("%s %s under B returned %d, want 404", route.method, route.path, code)
		}
	}

	var cards []card
	s.call(t, "GET", "/api/cards", a, wa.ID, nil, &cards)
	if len(cards) != 1 {
		t.Fatalf("an outsider changed workspace A: %d cards", len(cards))
	}
	var comments int
	if err := s.Env.Owner.QueryRow(t.Context(), `SELECT count(*) FROM comment WHERE workspace_id = $1 AND card_id = $2`, wa.ID, c.ID).Scan(&comments); err != nil {
		t.Fatal(err)
	}
	if comments != 0 {
		t.Fatalf("an outsider added %d comments to workspace A", comments)
	}
}
