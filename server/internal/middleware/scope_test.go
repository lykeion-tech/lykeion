package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequireScopeAdmitsAnyOfItsScopes(t *testing.T) {
	h := RequireScope("card:role", "report:write")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	for _, tc := range []struct {
		scopes []string
		want   int
	}{{[]string{"card:role"}, 204}, {[]string{"card:create", "report:write"}, 204}, {[]string{"card:create"}, 403}, {nil, 403}} {
		req := httptest.NewRequest("POST", "/mcp/sources", nil)
		req = req.WithContext(context.WithValue(req.Context(), roleKey, Role{Scopes: tc.scopes}))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != tc.want {
			t.Errorf("%v: %d, want %d", tc.scopes, w.Code, tc.want)
		}
	}
}
