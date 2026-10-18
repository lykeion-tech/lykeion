// Package workspacemcp stores the workspace's write-only MCP library.
package workspacemcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"lykeion/server/internal/availability"
	"lykeion/server/internal/connectors"
	"lykeion/server/internal/pack"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

type Config struct {
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}
type Summary struct {
	availability.Policy
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Transport string    `json:"transport"`
	Revision  int64     `json:"revision"`
	CreatedBy uuid.UUID `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
type Service struct {
	Store   *store.Store
	Secrets *connectors.Client
}

var (
	ErrInvalid   = errors.New("invalid MCP configuration")
	ErrForbidden = errors.New("only workspace owners and admins may modify MCP servers")
	ErrConflict  = errors.New("an MCP server with this name already exists")
)

type StaleRevision struct{ Revision int64 }

func (e *StaleRevision) Error() string { return "connector changed; reload its saved version" }

func Validate(name string, c Config) (string, error) {
	if name == "" {
		return "", ErrInvalid
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return "", ErrInvalid
		}
	}
	pc := pack.Connector{URL: c.URL, Env: c.Env}
	transport := "url"
	if c.Command != "" {
		pc.Command = append([]string{c.Command}, c.Args...)
		transport = "command"
	}
	if (c.URL != "" && (len(c.Args) > 0 || len(c.Env) > 0)) || (c.Command != "" && len(c.Headers) > 0) {
		return "", ErrInvalid
	}
	if err := pack.ValidateConnector(pc); err != nil {
		return "", ErrInvalid
	}
	return transport, nil
}
func resource(id uuid.UUID) string { return "mcp-servers/" + id.String() }
func (s *Service) seal(ws, id uuid.UUID, c Config) ([]byte, error) {
	raw, err := json.Marshal(c)
	if err != nil {
		return nil, ErrInvalid
	}
	return s.Secrets.Seal(ws, resource(id), string(raw))
}
func member(ctx context.Context, tx *store.Tx, user uuid.UUID, write bool) error {
	if write {
		if _, err := tx.Q.LockWorkspaceExecution(ctx, tx.WS); err != nil {
			return err
		}
		if err := tx.Q.LockWorkspaceEvents(ctx, tx.WS); err != nil {
			return err
		}
	}
	m, err := tx.Q.LockMember(ctx, db.LockMemberParams{WorkspaceID: tx.WS, UserID: user})
	if err != nil {
		return err
	}
	if write && m.Role != "owner" && m.Role != "admin" {
		return ErrForbidden
	}
	return nil
}
func safeError(err error) error {
	if err == nil || errors.Is(err, pgx.ErrNoRows) || errors.Is(err, ErrForbidden) {
		return err
	}
	var p *pgconn.PgError
	if errors.As(err, &p) && p.Code == "23505" {
		return ErrConflict
	}
	// Database diagnostics may contain submitted values. Keep them out of logs.
	return errors.New("workspace MCP storage failed")
}
func (s *Service) List(ctx context.Context, ws, user uuid.UUID) ([]Summary, error) {
	out := []Summary{}
	err := s.Store.InWorkspace(ctx, ws, user, func(tx *store.Tx) error {
		if err := member(ctx, tx, user, false); err != nil {
			return err
		}
		rows, err := tx.Q.ListWorkspaceMCPServers(ctx, ws)
		if err != nil {
			return err
		}
		for _, r := range rows {
			policy, err := availability.Get(ctx, tx, "mcp", r.ID.String())
			if err != nil {
				return err
			}
			out = append(out, Summary{policy, r.ID, r.Name, r.Transport, r.Revision, r.CreatedBy, r.CreatedAt, r.UpdatedAt})
		}
		return nil
	})
	return out, safeError(err)
}
func (s *Service) Create(ctx context.Context, ws, user uuid.UUID, name string, c Config) (Summary, error) {
	var out Summary
	name = strings.TrimSpace(name)
	transport, err := Validate(name, c)
	if err != nil {
		return out, err
	}
	id := uuid.New()
	sealed, err := s.seal(ws, id, c)
	if err != nil {
		return out, err
	}
	err = s.Store.InWorkspace(ctx, ws, user, func(tx *store.Tx) error {
		if err := member(ctx, tx, user, true); err != nil {
			return err
		}
		r, err := tx.Q.InsertWorkspaceMCPServer(ctx, db.InsertWorkspaceMCPServerParams{WorkspaceID: ws, ID: id, Name: name, Transport: transport, ConfigCiphertext: sealed, CreatedBy: user})
		if err != nil {
			return err
		}
		out = Summary{availability.Policy{Enabled: true}, r.ID, r.Name, r.Transport, r.Revision, r.CreatedBy, r.CreatedAt, r.UpdatedAt}
		return emit(ctx, tx, "connector.created", user, out)
	})
	return out, safeError(err)
}
func (s *Service) Update(ctx context.Context, ws, user, id uuid.UUID, expected int64, name string, c Config) (Summary, error) {
	var out Summary
	if expected < 1 {
		return out, ErrInvalid
	}
	name = strings.TrimSpace(name)
	transport, err := Validate(name, c)
	if err != nil {
		return out, err
	}
	sealed, err := s.seal(ws, id, c)
	if err != nil {
		return out, err
	}
	err = s.Store.InWorkspace(ctx, ws, user, func(tx *store.Tx) error {
		if err := member(ctx, tx, user, true); err != nil {
			return err
		}
		locked, err := tx.Q.LockWorkspaceMCPServer(ctx, db.LockWorkspaceMCPServerParams{WorkspaceID: ws, ID: id})
		if err != nil {
			return err
		}
		if locked.Revision != expected {
			return &StaleRevision{locked.Revision}
		}
		r, err := tx.Q.UpdateWorkspaceMCPServer(ctx, db.UpdateWorkspaceMCPServerParams{WorkspaceID: ws, ID: id, ExpectedRevision: expected, Name: name, Transport: transport, ConfigCiphertext: sealed})
		if err != nil {
			return err
		}
		out = Summary{availability.Policy{Enabled: true}, r.ID, r.Name, r.Transport, r.Revision, r.CreatedBy, r.CreatedAt, r.UpdatedAt}
		out.Policy, err = availability.Get(ctx, tx, "mcp", id.String())
		if err != nil {
			return err
		}
		return emit(ctx, tx, "connector.updated", user, out)
	})
	var stale *StaleRevision
	if errors.As(err, &stale) {
		return out, err
	}
	return out, safeError(err)
}
func (s *Service) Delete(ctx context.Context, ws, user, id uuid.UUID, expected int64) error {
	if expected < 1 {
		return ErrInvalid
	}
	err := s.Store.InWorkspace(ctx, ws, user, func(tx *store.Tx) error {
		if err := member(ctx, tx, user, true); err != nil {
			return err
		}
		locked, err := tx.Q.LockWorkspaceMCPServer(ctx, db.LockWorkspaceMCPServerParams{WorkspaceID: ws, ID: id})
		if err != nil {
			return err
		}
		if locked.Revision != expected {
			return &StaleRevision{locked.Revision}
		}
		r, err := tx.Q.DeleteWorkspaceMCPServer(ctx, db.DeleteWorkspaceMCPServerParams{WorkspaceID: ws, ID: id, ExpectedRevision: expected})
		if err != nil {
			return err
		}
		return emit(ctx, tx, "connector.deleted", user, Summary{availability.Policy{Enabled: true}, r.ID, r.Name, r.Transport, r.Revision, r.CreatedBy, r.CreatedAt, r.UpdatedAt})
	})
	var stale *StaleRevision
	if errors.As(err, &stale) {
		return err
	}
	return safeError(err)
}
func emit(ctx context.Context, tx *store.Tx, typ string, user uuid.UUID, s Summary) error {
	return tx.Emit(ctx, typ, nil, store.User(user), map[string]any{"connector_id": s.ID, "name": s.Name, "revision": s.Revision, "origin": "custom"})
}
