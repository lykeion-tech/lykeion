package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrations embed.FS

type Store struct{ Pool *pgxpool.Pool }

func Open(ctx context.Context, url string) (*Store, error) {
	p, e := pgxpool.New(ctx, url)
	if e != nil {
		return nil, e
	}
	if e = p.Ping(ctx); e != nil {
		p.Close()
		return nil, e
	}
	return &Store{p}, nil
}
func (s *Store) Migrate(ctx context.Context) error {
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(735924017)"); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, "CREATE TABLE IF NOT EXISTS schema_migrations(name text PRIMARY KEY, digest text NOT NULL)"); e != nil {
		return e
	}
	files, _ := migrations.ReadDir("migrations")
	for _, f := range files {
		data, _ := migrations.ReadFile("migrations/" + f.Name())
		digest := Hash(string(data))
		var existing string
		e = tx.QueryRow(ctx, "SELECT digest FROM schema_migrations WHERE name=$1", f.Name()).Scan(&existing)
		if e == nil {
			if existing != digest {
				return errors.New("migration checksum changed")
			}
			continue
		}
		if !errors.Is(e, pgx.ErrNoRows) {
			return e
		}
		if _, e = tx.Exec(ctx, string(data)); e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, "INSERT INTO schema_migrations VALUES($1,$2)", f.Name(), digest); e != nil {
			return e
		}
	}
	return tx.Commit(ctx)
}
func ID(prefix string) string {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return prefix + "_" + hex.EncodeToString(b)
}
func Secret() string       { return ID("secret") + ID("proof") }
func Hash(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

// Provision is an explicit administrative CLI operation, never startup seeding.
func (s *Store) Provision(ctx context.Context, name, workspace string) (userID, workspaceID, code string, err error) {
	if name == "" || workspace == "" {
		err = errors.New("owner and workspace names required")
		return
	}
	userID = ID("usr")
	workspaceID = ID("wsp")
	code = Secret()
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		err = e
		return
	}
	defer tx.Rollback(ctx)
	for _, q := range []struct {
		sql  string
		args []any
	}{{"INSERT INTO users(id,display_name) VALUES($1,$2)", []any{userID, name}}, {"INSERT INTO workspaces(id,name) VALUES($1,$2)", []any{workspaceID, workspace}}, {"INSERT INTO memberships(id,workspace_id,user_id,access_role) VALUES($1,$2,$3,'owner')", []any{ID("mem"), workspaceID, userID}}, {"INSERT INTO login_codes VALUES($1,$2,clock_timestamp()+interval '10 minutes')", []any{Hash(code), userID}}} {
		if _, err = tx.Exec(ctx, q.sql, q.args...); err != nil {
			return
		}
	}
	err = tx.Commit(ctx)
	return
}

type Session struct{ UserID, DisplayName, CSRF, ExpiresAt, ClientKind string }

func (s *Store) Exchange(ctx context.Context, code string) (string, Session, error) {
	return s.ExchangeFor(ctx, code, "browser")
}
func (s *Store) ExchangeFor(ctx context.Context, code, kind string) (token string, session Session, err error) {
	if kind != "browser" && kind != "cli" {
		err = errors.New("invalid session kind")
		return
	}
	session.ClientKind = kind
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		err = e
		return
	}
	defer tx.Rollback(ctx)
	err = tx.QueryRow(ctx, "DELETE FROM login_codes WHERE digest=$1 AND expires_at>clock_timestamp() RETURNING user_id", Hash(code)).Scan(&session.UserID)
	if err != nil {
		return
	}
	token = Secret()
	session.CSRF = Secret()
	err = tx.QueryRow(ctx, "INSERT INTO sessions(digest,user_id,csrf_digest,csrf_token,expires_at,client_kind) VALUES($1,$2,$3,$4,clock_timestamp()+interval '8 hours',$5) RETURNING to_char(expires_at AT TIME ZONE 'UTC','YYYY-MM-DD\"T\"HH24:MI:SS\"Z\"')", Hash(token), session.UserID, Hash(session.CSRF), session.CSRF, kind).Scan(&session.ExpiresAt)
	if err != nil {
		return
	}
	err = tx.QueryRow(ctx, "SELECT display_name FROM users WHERE id=$1", session.UserID).Scan(&session.DisplayName)
	if err == nil {
		err = tx.Commit(ctx)
	}
	return
}
func (s *Store) Authenticate(ctx context.Context, token string) (Session, error) {
	var v Session
	e := s.Pool.QueryRow(ctx, "SELECT s.user_id,u.display_name,s.csrf_token,s.client_kind,to_char(s.expires_at AT TIME ZONE 'UTC','YYYY-MM-DD\"T\"HH24:MI:SS\"Z\"') FROM sessions s JOIN users u ON u.id=s.user_id WHERE s.digest=$1 AND s.expires_at>clock_timestamp()", Hash(token)).Scan(&v.UserID, &v.DisplayName, &v.CSRF, &v.ClientKind, &v.ExpiresAt)
	return v, e
}
func (s *Store) Access(ctx context.Context, w, u string) (role string, epoch, sequence, revision int64, err error) {
	err = s.Pool.QueryRow(ctx, "SELECT m.access_role,w.auth_epoch,w.event_sequence,w.revision FROM memberships m JOIN workspaces w ON w.id=m.workspace_id WHERE m.workspace_id=$1 AND m.user_id=$2 AND m.state='active'", w, u).Scan(&role, &epoch, &sequence, &revision)
	return
}
func (s *Store) Close() { s.Pool.Close() }
func RequiredDSN(value string) (string, error) {
	if value == "" {
		return "", fmt.Errorf("LYKEION_DATABASE_URL is required")
	}
	return value, nil
}

// IssueLoginCode requires administrative database access; it is not an HTTP account-creation path.
func (s *Store) IssueLoginCode(ctx context.Context, user string) (string, error) {
	code := Secret()
	result, err := s.Pool.Exec(ctx, "INSERT INTO login_codes(digest,user_id,expires_at) SELECT $1,id,clock_timestamp()+interval '10 minutes' FROM users WHERE id=$2", Hash(code), user)
	if err != nil {
		return "", err
	}
	if result.RowsAffected() != 1 {
		return "", errors.New("account not found")
	}
	return code, nil
}
