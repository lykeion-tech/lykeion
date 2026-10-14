package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"lykeion/server/internal/auth"
	"lykeion/server/internal/store"
)

// Role identifies one run's authority over a card, column and round.
type Role struct {
	AgentRunID, WorkspaceID, CardID uuid.UUID
	Role, Column                    string
	Round                           int32
}

const roleKey key = 100

func RoleFrom(ctx context.Context) Role { v, _ := ctx.Value(roleKey).(Role); return v }

// AgentRunAuth rejects people credentials and checks the current running token.
// Handlers must revalidate under transaction locks before accessing tenant data.
func AgentRunAuth(st *store.Store) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
			if !ok || !strings.HasPrefix(token, "ltk_") {
				deny(w, 401, "run token required")
				return
			}
			row, err := st.Global().LookupAgentRunToken(r.Context(), auth.Hash(token))
			if err != nil {
				deny(w, 401, "invalid or finished agent run")
				return
			}
			role := Role{AgentRunID: row.AgentRunID, WorkspaceID: row.WorkspaceID, CardID: row.CardID, Role: row.Role, Column: row.BoardColumn, Round: row.Round}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), roleKey, role)))
		})
	}
}
