package skill

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"lykeion/server/internal/middleware"
	"lykeion/server/internal/store"
	"lykeion/server/internal/testutil"
	"testing"
)

func TestPinnedSkillRequiresMatchingPreparedScope(t *testing.T) {
	env := testutil.Setup(t)
	s := Service{Store: &store.Store{Pool: env.App}}
	ws, run, card := uuid.New(), uuid.New(), uuid.New()
	scope := Scope{Kind: "card", ID: card}
	a := runAuthority{role: middleware.Role{WorkspaceID: ws, AgentRunID: run, CardID: card}, token: "ltk_request"}
	ctx := WithRunAuthority(context.Background(), a.role, a.token)
	for _, tc := range []struct {
		name  string
		ctx   context.Context
		scope Scope
	}{
		{"missing snapshot", ctx, scope},
		{"different scope", context.WithValue(ctx, routingKey{}, routingSnapshot{workspace: ws, scope: scope, authority: a}), Scope{Kind: "card", ID: uuid.New()}},
		{"different workspace", context.WithValue(ctx, routingKey{}, routingSnapshot{workspace: uuid.New(), scope: scope, authority: a}), scope},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := s.ResolveForScope(tc.ctx, ws, tc.scope, []string{"imported"}); !errors.Is(err, middleware.ErrRoleUnauthorized) {
				t.Fatalf("unbound snapshot accepted: %v", err)
			}
		})
	}
}
