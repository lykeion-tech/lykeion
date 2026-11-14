package main

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"lykeion/server/internal/testutil"
)

func provisionV2Role(t *testing.T, env *testutil.Env) string {
	t.Helper()
	if _, err := env.Owner.Exec(t.Context(), `CREATE ROLE lykeion_app_v2 LOGIN PASSWORD 'task-only-v2'`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := env.Owner.Exec(context.Background(), `DROP ROLE lykeion_app_v2`); err != nil {
			t.Errorf("drop v2 role: %v", err)
		}
	})
	if _, err := env.Owner.Exec(t.Context(), `GRANT lykeion_app TO lykeion_app_v2 WITH INHERIT TRUE, SET FALSE, ADMIN FALSE`); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(env.AppURL)
	if err != nil {
		t.Fatal(err)
	}
	u.User = url.UserPassword("lykeion_app_v2", "task-only-v2")
	return u.String()
}

func TestRestrictedV2RolePreservesWorkspaceRLS(t *testing.T) {
	env := testutil.Setup(t)
	url := provisionV2Role(t, env)
	user, first, second := uuid.New(), uuid.New(), uuid.New()
	ctx := t.Context()
	if _, err := env.Owner.Exec(ctx, `INSERT INTO public."user" (id,email) VALUES ($1,'v2-rls@example.invalid')`, user); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Owner.Exec(ctx, `INSERT INTO public.workspace (id,slug,name) VALUES ($1,'v2-rls-a','A'),($2,'v2-rls-b','B')`, first, second); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Owner.Exec(ctx, `INSERT INTO public.card (workspace_id,number,title,question,owner_id,board_column) VALUES ($1,1,'A','A',$3,'grounding'),($2,1,'B','B',$3,'grounding')`, first, second, user); err != nil {
		t.Fatal(err)
	}
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err := tx.Exec(ctx, `SELECT set_config('app.workspace_id',$1,true), set_config('app.user_id',$2,true)`, first.String(), user.String()); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM public.card`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("workspace isolation: %d %v", n, err)
	}
	if _, err := tx.Exec(ctx, `SELECT * FROM public.schema_migrations`); err == nil {
		t.Fatal("v2 can read owner-only migration ledger")
	}
}

func TestRestrictedV2RoleRejectsEscalation(t *testing.T) {
	for _, scenario := range []string{"superuser", "bypassrls", "createrole", "extra membership", "set role", "schema create", "direct ledger grant", "table ownership", "collation ownership"} {
		t.Run(scenario, func(t *testing.T) {
			env := testutil.Setup(t)
			values := config(t, env)
			values["DATABASE_URL"] = provisionV2Role(t, env)
			values["DATABASE_ROLE"], values["MIGRATIONS_MODE"] = "lykeion_app_v2", "external"
			delete(values, "MIGRATE_DATABASE_URL")
			var change, undo string
			switch scenario {
			case "superuser":
				change = `ALTER ROLE lykeion_app_v2 SUPERUSER`
			case "bypassrls":
				change = `ALTER ROLE lykeion_app_v2 BYPASSRLS`
			case "createrole":
				change = `ALTER ROLE lykeion_app_v2 CREATEROLE`
			case "extra membership":
				change = `GRANT pg_read_all_data TO lykeion_app_v2`
			case "set role":
				change = `GRANT lykeion_app TO lykeion_app_v2 WITH SET TRUE`
			case "schema create":
				change, undo = `GRANT CREATE ON SCHEMA public TO lykeion_app_v2`, `REVOKE CREATE ON SCHEMA public FROM lykeion_app_v2`
			case "direct ledger grant":
				change, undo = `GRANT SELECT ON public.schema_migrations TO lykeion_app_v2`, `REVOKE SELECT ON public.schema_migrations FROM lykeion_app_v2`
			case "table ownership":
				change, undo = `ALTER TABLE public.card OWNER TO lykeion_app_v2`, `ALTER TABLE public.card OWNER TO owner`
			case "collation ownership":
				if _, err := env.Owner.Exec(t.Context(), `CREATE COLLATION public.v2_test_collation (provider=libc, locale='C')`); err != nil {
					t.Fatal(err)
				}
				change, undo = `ALTER COLLATION public.v2_test_collation OWNER TO lykeion_app_v2`, `DROP COLLATION public.v2_test_collation`
			}
			if _, err := env.Owner.Exec(t.Context(), change); err != nil {
				t.Fatal(err)
			}
			if undo != "" {
				t.Cleanup(func() {
					if _, err := env.Owner.Exec(context.Background(), undo); err != nil {
						t.Errorf("undo role fixture: %v", err)
					}
				})
			}
			ready := make(chan string, 1)
			err := run(t.Context(), func(k string) string { return values[k] }, ready)
			if err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
				t.Fatalf("unsafe role accepted: %v", err)
			}
			select {
			case addr := <-ready:
				t.Fatalf("announced ready: %s", addr)
			default:
			}
		})
	}
}

func TestRestrictedV2RoleRejectsUnsafeGrantFromSecondGrantor(t *testing.T) {
	env := testutil.Setup(t)
	values := config(t, env)
	values["DATABASE_URL"] = provisionV2Role(t, env)
	values["DATABASE_ROLE"], values["MIGRATIONS_MODE"] = "lykeion_app_v2", "external"
	delete(values, "MIGRATE_DATABASE_URL")
	if _, err := env.Owner.Exec(t.Context(), `CREATE ROLE v2_test_grantor`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := env.Owner.Exec(context.Background(), `DROP ROLE v2_test_grantor`); err != nil {
			t.Errorf("drop grantor: %v", err)
		}
	})
	if _, err := env.Owner.Exec(t.Context(), `GRANT lykeion_app TO v2_test_grantor WITH ADMIN TRUE`); err != nil {
		t.Fatal(err)
	}
	conn, err := env.Owner.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(t.Context(), `SET ROLE v2_test_grantor`); err == nil {
		_, err = conn.Exec(t.Context(), `GRANT lykeion_app TO lykeion_app_v2 WITH SET TRUE`)
	}
	_, resetErr := conn.Exec(context.Background(), `RESET ROLE`)
	conn.Release()
	if err != nil || resetErr != nil {
		t.Fatalf("second grantor: %v %v", err, resetErr)
	}
	t.Cleanup(func() {
		if _, err := env.Owner.Exec(context.Background(), `REVOKE lykeion_app FROM lykeion_app_v2 GRANTED BY v2_test_grantor`); err != nil {
			t.Errorf("revoke second grant: %v", err)
		}
	})
	var grants int
	if err := env.Owner.QueryRow(t.Context(), `SELECT count(*) FROM pg_auth_members m JOIN pg_roles p ON p.oid=m.roleid JOIN pg_roles c ON c.oid=m.member WHERE p.rolname='lykeion_app' AND c.rolname='lykeion_app_v2'`).Scan(&grants); err != nil || grants != 2 {
		t.Fatalf("grant fixture: %d %v", grants, err)
	}
	ready := make(chan string, 1)
	if err := run(t.Context(), func(k string) string { return values[k] }, ready); err == nil {
		t.Fatal("unsafe second grant accepted")
	}
	select {
	case addr := <-ready:
		t.Fatalf("announced ready: %s", addr)
	default:
	}
}

func TestV2RoleRequiresExternalMigrations(t *testing.T) {
	values := map[string]string{"JWT_SECRET": "0123456789abcdef0123456789abcdef", "DATABASE_ROLE": "lykeion_app_v2"}
	if err := run(t.Context(), func(k string) string { return values[k] }, nil); err == nil || !strings.Contains(err.Error(), "MIGRATIONS_MODE") {
		t.Fatalf("v2 auto mode: %v", err)
	}
	values["DATABASE_ROLE"] = "unexpected"
	if err := run(t.Context(), func(k string) string { return values[k] }, nil); err == nil || !strings.Contains(err.Error(), "DATABASE_ROLE") {
		t.Fatalf("unknown role: %v", err)
	}
}

func TestExternalServerAcceptsRestrictedV2Role(t *testing.T) {
	env := testutil.Setup(t)
	values := config(t, env)
	values["DATABASE_URL"] = provisionV2Role(t, env)
	values["DATABASE_ROLE"] = "lykeion_app_v2"
	values["MIGRATIONS_MODE"] = "external"
	delete(values, "MIGRATE_DATABASE_URL")
	_, cancel, done := start(t, values)
	cancel()
	stopped(t, done)
}
