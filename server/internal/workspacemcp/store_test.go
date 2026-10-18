package workspacemcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"lykeion/server/internal/connectors"
	"lykeion/server/internal/pack"
	"lykeion/server/internal/store"
	"lykeion/server/internal/testutil"
)

func TestWorkspaceMCPSealedConfigUsesStableResourceIdentity(t *testing.T) {
	ws, id := uuid.New(), uuid.New()
	s := Service{Secrets: &connectors.Client{Secret: bytes.Repeat([]byte{7}, 32)}}
	config := Config{URL: "https://example.org/mcp?token=private-secret", Headers: map[string]string{"Authorization": "Bearer private-secret"}}
	sealed, err := s.seal(ws, id, config)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte("private-secret")) {
		t.Fatal("plaintext in storage")
	}
	raw, err := s.Secrets.Open(ws, resource(id), sealed)
	if err != nil {
		t.Fatal(err)
	}
	var got Config
	if err := json.Unmarshal([]byte(raw), &got); err != nil || got.URL != config.URL || got.Headers["Authorization"] != config.Headers["Authorization"] {
		t.Fatalf("roundtrip: %+v %v", got, err)
	}
	for _, pair := range [][2]uuid.UUID{{uuid.New(), id}, {ws, uuid.New()}} {
		if _, err := s.Secrets.Open(pair[0], resource(pair[1]), sealed); err == nil {
			t.Fatal("ciphertext opened with wrong tenant or resource")
		}
	}
}
func TestWorkspaceMCPReusesTeamValidation(t *testing.T) {
	for _, c := range []Config{
		{URL: "https://example.org/mcp"}, {URL: "http://example.org/mcp"},
		{Command: "npx", Args: []string{"-y", "server"}, Env: map[string]string{"MODE": "fast"}},
		{Command: "/bin/server"}, {Command: "../server"}, {Command: "x", Env: map[string]string{"API_KEY": "value"}},
	} {
		pc := pack.Connector{URL: c.URL, Env: c.Env}
		if c.Command != "" {
			pc.Command = append([]string{c.Command}, c.Args...)
		}
		_, err := Validate("lab-mcp", c)
		teamErr := pack.ValidateConnector(pc)
		if (err == nil) != (teamErr == nil) {
			t.Fatalf("library and Team differ for input: %v / %v", err, teamErr)
		}
	}
	// The approved policy leaves non-secret argument and environment values to the owner.
	c := Config{Command: "npx", Args: []string{"server", "--mode=fast path"}, Env: map[string]string{"MODE": "value"}}
	if _, err := Validate("lab-mcp", c); err != nil {
		t.Fatal(err)
	}
	if c.Args[1] != "--mode=fast path" {
		t.Fatal("arguments normalized")
	}
}

func TestWorkspaceMCPServiceCurrentPermissionsAndAtomicCAS(t *testing.T) {
	env := testutil.Setup(t)
	ctx := context.Background()
	ws, user, memberID := uuid.New(), uuid.New(), uuid.New()
	for _, seed := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO "user" (id,email) VALUES ($1,'owner@lab.org'),($2,'member@lab.org')`, []any{user, memberID}},
		{`INSERT INTO workspace (id,slug,name) VALUES ($1,'lab','Lab')`, []any{ws}},
		{`INSERT INTO member (workspace_id,user_id,role) VALUES ($1,$2,'owner'),($1,$3,'member')`, []any{ws, user, memberID}},
	} {
		if _, err := env.Owner.Exec(ctx, seed.sql, seed.args...); err != nil {
			t.Fatal(err)
		}
	}
	s := Service{Store: &store.Store{Pool: env.App}, Secrets: &connectors.Client{Secret: bytes.Repeat([]byte{7}, 32)}}
	config := Config{URL: "https://example.org/mcp"}
	if _, err := s.Create(ctx, ws, memberID, "member-entry", config); !errors.Is(err, ErrForbidden) {
		t.Fatalf("service member create: %v", err)
	}
	got, err := s.Create(ctx, ws, user, "owner-entry", config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Update(ctx, ws, memberID, got.ID, 1, "mutated", config); !errors.Is(err, ErrForbidden) {
		t.Fatalf("service member update: %v", err)
	}
	if err := s.Delete(ctx, ws, memberID, got.ID, 1); !errors.Is(err, ErrForbidden) {
		t.Fatalf("service member delete: %v", err)
	}
	results := make(chan error, 2)
	for range 2 {
		go func() { _, err := s.Update(ctx, ws, user, got.ID, 1, "owner-entry", config); results <- err }()
	}
	successes, conflicts := 0, 0
	for range 2 {
		err := <-results
		var stale *StaleRevision
		switch {
		case err == nil:
			successes++
		case errors.As(err, &stale) && stale.Revision == 2:
			conflicts++
		default:
			t.Fatalf("concurrent result: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("CAS: %d successes, %d conflicts", successes, conflicts)
	}
	var count int
	if err := env.Owner.QueryRow(ctx, `SELECT count(*) FROM event WHERE type='connector.updated' AND payload->>'revision'='2'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("event atomicity: %d %v", count, err)
	}
	if _, err := env.Owner.Exec(ctx, `UPDATE member SET role='member' WHERE workspace_id=$1 AND user_id=$2`, ws, user); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Update(ctx, ws, user, got.ID, 2, "after-demotion", config); !errors.Is(err, ErrForbidden) {
		t.Fatalf("service demotion: %v", err)
	}
}
