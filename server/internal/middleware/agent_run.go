package middleware

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"lykeion/server/internal/auth"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

// Role identifies one run's authority over a card, column and round.
type Role struct {
	AgentRunID, WorkspaceID, CardID uuid.UUID
	Role, Column                    string
	Round                           int32
	Scopes                          []string
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
			role := Role{AgentRunID: row.AgentRunID, WorkspaceID: row.WorkspaceID, CardID: row.CardID, Role: row.Role, Column: row.BoardColumn, Round: row.Round, Scopes: row.Scopes}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), roleKey, role)))
		})
	}
}

// ErrRoleUnauthorized is a run that settled, moved on or lost its token after
// its request was authenticated.
var ErrRoleUnauthorized = errors.New("invalid or finished agent run")

// LockRole protects the workspace before child rows and serializes every role
// write with the gate's card snapshot. The execution mutex prevents opposing
// card/run orders; run precedes token as in settlement. Authority is rechecked
// after these locks, including the current attempt.
func LockRole(ctx context.Context, tx *store.Tx, role Role, token string) error {
	if _, err := tx.Q.LockWorkspaceExecution(ctx, role.WorkspaceID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrRoleUnauthorized
		}
		return err
	}
	if role.CardID != uuid.Nil {
		if _, err := tx.Q.LockCard(ctx, db.LockCardParams{ID: role.CardID, WorkspaceID: role.WorkspaceID}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrRoleUnauthorized
			}
			return err
		}
	}
	run, err := tx.Q.LockAgentRun(ctx, db.LockAgentRunParams{ID: role.AgentRunID, WorkspaceID: role.WorkspaceID})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrRoleUnauthorized
	}
	if err != nil {
		return err
	}
	if run.Status != "running" || cardOf(run.CardID) != role.CardID || run.Role != role.Role || run.BoardColumn != role.Column || run.Round != role.Round {
		return ErrRoleUnauthorized
	}
	tok, err := tx.Q.LockAgentRunToken(ctx, db.LockAgentRunTokenParams{TokenHash: auth.Hash(token), WorkspaceID: role.WorkspaceID, AgentRunID: role.AgentRunID})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrRoleUnauthorized
	}
	if err != nil {
		return err
	}
	if tok.Attempt != run.Attempt || cardOf(tok.CardID) != cardOf(run.CardID) || tok.Role != run.Role {
		return ErrRoleUnauthorized
	}
	return nil
}

// cardOf is a nullable card id as the role sees it.
func cardOf(id *uuid.UUID) uuid.UUID {
	if id == nil {
		return uuid.Nil
	}
	return *id
}

// RequireScope admits only agent run tokens that carry one of the scopes. A card-less
// agent run's token lacks the role scope, so it cannot reach a card's records.
func RequireScope(scopes ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !slices.ContainsFunc(scopes, func(s string) bool { return slices.Contains(RoleFrom(r.Context()).Scopes, s) }) {
				deny(w, 403, "this agent run's token may not do that")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
