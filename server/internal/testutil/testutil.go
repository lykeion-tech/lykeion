// Package testutil gives each test its own migrated Postgres database, cloned
// from a template, and a flushed Redis.
package testutil

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"

	"lykeion/server/internal/migrate"
	"lykeion/server/migrations"
)

var (
	once     sync.Once
	ownerURL string
	redisURL string
	template = fmt.Sprintf("lyk_template_%d", os.Getpid())
	setupErr error
)

func start() {
	ctx := context.Background()
	ownerURL, redisURL = os.Getenv("LYK_TEST_PG"), os.Getenv("LYK_TEST_REDIS")
	if ownerURL == "" {
		pg, err := tcpostgres.Run(ctx, "postgres:17-alpine",
			tcpostgres.WithDatabase("postgres"), tcpostgres.WithUsername("owner"), tcpostgres.WithPassword("owner"),
			tcpostgres.BasicWaitStrategies())
		if err != nil {
			setupErr = err
			return
		}
		if ownerURL, setupErr = pg.ConnectionString(ctx, "sslmode=disable"); setupErr != nil {
			return
		}
	}
	if redisURL == "" {
		rd, err := tcredis.Run(ctx, "redis:7-alpine")
		if err != nil {
			setupErr = err
			return
		}
		if redisURL, setupErr = rd.ConnectionString(ctx); setupErr != nil {
			return
		}
	}
	admin, err := pgx.Connect(ctx, ownerURL)
	if err != nil {
		setupErr = err
		return
	}
	defer admin.Close(ctx)
	for _, q := range []string{"DROP DATABASE IF EXISTS " + template, "CREATE DATABASE " + template} {
		if _, setupErr = admin.Exec(ctx, q); setupErr != nil {
			return
		}
	}
	conn, err := pgx.Connect(ctx, WithDB(ownerURL, template))
	if err != nil {
		setupErr = err
		return
	}
	defer conn.Close(ctx)
	if setupErr = migrate.Up(ctx, conn, migrations.FS); setupErr != nil {
		return
	}
	_, setupErr = conn.Exec(ctx, "ALTER ROLE lykeion_app LOGIN PASSWORD 'app'")
}

type Env struct {
	OwnerURL string // the owning role: bypasses row-level security, for seeding
	AppURL   string // lykeion_app: what the server uses
	Owner    *pgxpool.Pool
	App      *pgxpool.Pool
	Redis    *redis.Client
	RedisURL string
}

func Setup(t *testing.T) *Env {
	t.Helper()
	once.Do(start)
	if setupErr != nil {
		t.Fatalf("test databases: %v (run Docker, or set LYK_TEST_PG and LYK_TEST_REDIS)", setupErr)
	}
	ctx := context.Background()
	name := "t_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	admin, err := pgx.Connect(ctx, ownerURL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name+" TEMPLATE "+template); err != nil {
		t.Fatal(err)
	}
	admin.Close(ctx)
	t.Cleanup(func() {
		c, err := pgx.Connect(ctx, ownerURL)
		if err != nil {
			t.Errorf("cleanup database: %v", err)
			return
		}
		defer c.Close(ctx)
		if _, err := c.Exec(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)"); err != nil {
			t.Errorf("cleanup database: %v", err)
		}
	})

	env := &Env{OwnerURL: WithDB(ownerURL, name), RedisURL: redisURL}
	env.AppURL = withUser(env.OwnerURL, "lykeion_app", "app")
	if env.Owner, err = pgxpool.New(ctx, env.OwnerURL); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(env.Owner.Close)
	if err := env.Owner.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	if env.App, err = pgxpool.New(ctx, env.AppURL); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(env.App.Close)
	if err := env.App.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatal(err)
	}
	env.Redis = redis.NewClient(opt)
	t.Cleanup(func() {
		if err := env.Redis.Close(); err != nil {
			t.Errorf("close redis: %v", err)
		}
	})
	if err := env.Redis.FlushAll(ctx).Err(); err != nil {
		t.Fatal(err)
	}

	return env
}

func WithDB(u, db string) string {
	p, _ := url.Parse(u)
	p.Path = "/" + db
	return p.String()
}

func withUser(u, user, pass string) string {
	p, _ := url.Parse(u)
	p.User = url.UserPassword(user, pass)
	return p.String()
}
