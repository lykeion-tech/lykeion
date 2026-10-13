package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/lykeion-ia/lykeion-redesign/backend/internal/control"
	"github.com/lykeion-ia/lykeion-redesign/backend/internal/store"
)

func TestCLIAuthenticatedCommandLifecycle(t *testing.T) {
	dsn := os.Getenv("LYKEION_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated PostgreSQL required")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	schema := store.ID("cli_test")
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	db, err := store.Open(ctx, dsn+sep+"search_path="+schema)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	user, workspace, code, err := db.Provision(ctx, "CLI synthetic scientist", "CLI isolated workspace")
	if err != nil {
		t.Fatal(err)
	}
	api, err := control.New(db, []byte(strings.Repeat("c", 32)), []string{"http://127.0.0.1:1430"}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer api.Close()
	server := httptest.NewServer(api.Handler())
	defer server.Close()
	dir := t.TempDir()
	codeFile := filepath.Join(dir, "code")
	credentials := filepath.Join(dir, "session")
	bodyFile := filepath.Join(dir, "command")
	if err = os.WriteFile(codeFile, []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	call := func(args ...string) {
		t.Helper()
		if err := run(args); err != nil {
			t.Fatal(err)
		}
	}
	call("login", "--endpoint", server.URL, "--code-file", codeFile, "--credentials", credentials)
	raw, err := privateRead(credentials)
	if err != nil {
		t.Fatal(err)
	}
	var cfg deviceConfig
	if err = json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	call("get", "--credentials", credentials, "--path", "/v1/workspaces")
	body := fmt.Sprintf(`{"schema_version":"1","target":{"kind":"workspace","id":%q},"expected_versions":[{"kind":"workspace","id":%q,"revision":"1"}],"payload":{"preference_kind":"theme","value":"dark"}}`, workspace, workspace)
	if err = os.WriteFile(bodyFile, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{"command", "--credentials", credentials, "--workspace", workspace, "--name", "preference.update", "--body-file", bodyFile, "--key", "cli-stable-retry"}
	call(args...)
	call(args...)
	var count int
	if err = db.Pool.QueryRow(ctx, "SELECT count(*) FROM operations WHERE workspace_id=$1 AND user_id=$2", workspace, user).Scan(&count); err != nil || count != 1 {
		t.Fatalf("retry duplicated effect: %d %v", count, err)
	}
	if err = os.WriteFile(bodyFile, []byte(strings.Replace(body, "dark", "light", 1)), 0600); err != nil {
		t.Fatal(err)
	}
	if run(args) == nil {
		t.Fatal("changed retry accepted")
	}
	call("logout", "--credentials", credentials)
	if _, err = os.Stat(credentials); !os.IsNotExist(err) {
		t.Fatal("logout retained credential")
	}
	if _, err = apiCall(cfg, "GET", "/v1/workspaces", nil, ""); err == nil {
		t.Fatal("logout retained API authority")
	}
}
