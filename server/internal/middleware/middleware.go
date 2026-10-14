// Package middleware resolves who is calling and in which workspace.
package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"lykeion/server/internal/auth"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

const SessionCookie = "lyk_session"

type Identity struct {
	UserID uuid.UUID
	Email  string
}

type Workspace struct {
	ID   uuid.UUID
	Role string
}

type key int

const (
	identityKey key = iota
	workspaceKey
	transactionKey
)

func IdentityFrom(ctx context.Context) Identity { v, _ := ctx.Value(identityKey).(Identity); return v }
func WorkspaceFrom(ctx context.Context) Workspace {
	v, _ := ctx.Value(workspaceKey).(Workspace)
	return v
}

func deny(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// Auth accepts a session JWT (cookie or bearer) or a lyk_ personal token.
func Auth(st *store.Store, iss auth.Issuer) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tok := ""
			if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
				tok = strings.TrimPrefix(h, "Bearer ")
			} else if c, err := r.Cookie(SessionCookie); err == nil {
				tok = c.Value
			}
			var id Identity
			switch {
			case tok == "":
				deny(w, 401, "sign in required")
				return
			case strings.HasPrefix(tok, auth.PersonalPrefix):
				row, err := st.Global().TokenByHash(r.Context(), auth.Hash(tok))
				if err != nil {
					if !errors.Is(err, pgx.ErrNoRows) {
						slog.Error("token lookup failed", "err", err)
						deny(w, 503, "authentication unavailable")
						return
					}
					deny(w, 401, "invalid token")
					return
				}
				id = Identity{UserID: row.UserID, Email: row.Email}
			default:
				u, email, err := iss.Parse(tok)
				if err != nil {
					deny(w, 401, "invalid session")
					return
				}
				id = Identity{UserID: u, Email: email}
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), identityKey, id)))
		})
	}
}

func FromHeader(r *http.Request) string { return r.Header.Get("X-Workspace-ID") }

func FromURL(param string) func(*http.Request) string {
	return func(r *http.Request) string { return chi.URLParam(r, param) }
}

// Scope admits members of the workspace named by from; anyone else gets 404,
// so a workspace's existence is not revealed.
func Scope(st *store.Store, from func(*http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw := from(r)
			if raw == "" {
				deny(w, 400, "X-Workspace-ID required")
				return
			}
			ws, err := uuid.Parse(raw)
			if err != nil {
				deny(w, 404, "workspace not found")
				return
			}
			id := IdentityFrom(r.Context())
			response := &bufferedResponse{header: make(http.Header)}
			err = st.InWorkspace(r.Context(), ws, id.UserID, func(tx *store.Tx) error {
				// Workspace mutations take this lock first, before locking any membership.
				if r.Method != http.MethodGet && r.Method != http.MethodHead {
					if _, err := tx.Q.LockWorkspaceForMutation(r.Context(), ws); err != nil {
						return err
					}
				}
				m, err := tx.Q.LockMember(r.Context(), db.LockMemberParams{WorkspaceID: ws, UserID: id.UserID})
				if err != nil {
					return err
				}
				ctx := context.WithValue(r.Context(), workspaceKey, Workspace{ID: ws, Role: m.Role})
				ctx = context.WithValue(ctx, transactionKey, tx)
				next.ServeHTTP(response, r.WithContext(ctx))
				if response.status >= 400 {
					return errResponse
				}
				return nil
			})
			if err != nil && !errors.Is(err, errResponse) {
				if errors.Is(err, pgx.ErrNoRows) {
					deny(w, 404, "workspace not found")
				} else {
					slog.Error("workspace transaction failed", "err", err)
					deny(w, 500, "internal error")
				}
				return
			}
			for k, v := range response.header {
				w.Header()[k] = v
			}
			status := response.status
			if status == 0 {
				status = 200
			}
			w.WriteHeader(status)
			w.Write(response.body.Bytes())
		})
	}
}

var errResponse = errors.New("handler refused request")
var ErrForbidden = errors.New("insufficient workspace role")

// TxFrom returns the transaction opened by Scope, which remains live through
// the handler and commits before its buffered response is sent.
func TxFrom(ctx context.Context) *store.Tx { tx, _ := ctx.Value(transactionKey).(*store.Tx); return tx }

// InWorkspace reuses the scoped transaction and its locked authorization.
// Unscoped callers acquire the workspace lock before the membership lock;
// every membership mutation must use that same order to avoid lock upgrades.
// Missing membership returns pgx.ErrNoRows; insufficient role returns ErrForbidden.
func InWorkspace(ctx context.Context, st *store.Store, minRole string, fn func(*store.Tx) error) error {
	ws := WorkspaceFrom(ctx)
	id := IdentityFrom(ctx)
	if tx := TxFrom(ctx); tx != nil {
		if tx.WS != ws.ID {
			return errors.New("workspace transaction mismatch")
		}
		if !hasRole(ws.Role, minRole) {
			return ErrForbidden
		}
		return fn(tx)
	}
	if ws.ID == uuid.Nil || id.UserID == uuid.Nil {
		return pgx.ErrNoRows
	}
	return st.InWorkspace(ctx, ws.ID, id.UserID, func(tx *store.Tx) error {
		if _, err := tx.Q.LockWorkspaceForMutation(ctx, ws.ID); err != nil {
			return err
		}
		m, err := tx.Q.LockMember(ctx, db.LockMemberParams{WorkspaceID: ws.ID, UserID: id.UserID})
		if err != nil {
			return err
		}
		if !hasRole(m.Role, minRole) {
			return ErrForbidden
		}
		return fn(tx)
	})
}
func hasRole(role, minRole string) bool {
	switch minRole {
	case "", "member":
		return role == "member" || role == "admin" || role == "owner"
	case "admin":
		return role == "admin" || role == "owner"
	case "owner":
		return role == "owner"
	default:
		return false
	}
}

type bufferedResponse struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func (b *bufferedResponse) Header() http.Header { return b.header }
func (b *bufferedResponse) WriteHeader(status int) {
	if b.status == 0 {
		b.status = status
	}
}
func (b *bufferedResponse) Write(p []byte) (int, error) {
	if b.status == 0 {
		b.status = 200
	}
	return b.body.Write(p)
}

func RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if role := WorkspaceFrom(r.Context()).Role; role != "owner" && role != "admin" {
			deny(w, 403, "admins only")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func RequireOwner(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if WorkspaceFrom(r.Context()).Role != "owner" {
			deny(w, 403, "owners only")
			return
		}
		next.ServeHTTP(w, r)
	})
}
