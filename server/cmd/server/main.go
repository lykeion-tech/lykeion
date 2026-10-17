// Command lykeion-server is the control service.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"lykeion/server/internal/auth"
	"lykeion/server/internal/autoresearch"
	"lykeion/server/internal/handler"
	"lykeion/server/internal/migrate"
	"lykeion/server/internal/pack/check"
	"lykeion/server/internal/realtime"
	"lykeion/server/internal/round"
	"lykeion/server/internal/seal"
	"lykeion/server/internal/service"
	"lykeion/server/internal/sources"
	"lykeion/server/internal/storage"
	"lykeion/server/internal/store"
	"lykeion/server/migrations"
)

func newStorage(ctx context.Context, getenv func(string) string) (storage.Storage, error) {
	s := getenv("STORAGE")
	switch {
	case strings.HasPrefix(s, "local:"):
		root := strings.TrimPrefix(s, "local:")
		if root == "" || !filepath.IsAbs(root) {
			return nil, errors.New("STORAGE local path must be a nonempty absolute path")
		}
		if err := os.MkdirAll(root, 0o700); err != nil {
			return nil, fmt.Errorf("STORAGE local directory: %w", err)
		}
		if _, err := filepath.EvalSymlinks(root); err != nil {
			return nil, fmt.Errorf("STORAGE local directory: %w", err)
		}
		probe, err := os.CreateTemp(root, ".lyk-storage-*")
		if err != nil {
			return nil, fmt.Errorf("STORAGE local directory is not writable: %w", err)
		}
		if err := errors.Join(probe.Close(), os.Remove(probe.Name())); err != nil {
			return nil, fmt.Errorf("STORAGE local probe: %w", err)
		}
		return storage.Local{Dir: root}, nil
	case s == "s3":
		bucket, region := strings.TrimSpace(getenv("S3_BUCKET")), strings.TrimSpace(getenv("S3_REGION"))
		if bucket == "" || region == "" {
			return nil, errors.New("S3_BUCKET and S3_REGION are required for STORAGE=s3")
		}
		endpoint := getenv("S3_ENDPOINT")
		if endpoint != "" {
			u, err := url.Parse(endpoint)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
				return nil, errors.New("S3_ENDPOINT must be an http or https URL")
			}
		}
		return storage.NewS3(ctx, bucket, endpoint, region)
	default:
		return nil, errors.New("STORAGE must be local:/path or s3")
	}
}

func main() {
	check.MaybeChild()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Getenv, nil); err != nil {
		slog.Error("server stopped", "err", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, getenv func(string) string, ready chan<- string) error {
	secret := getenv("JWT_SECRET")
	if len(secret) < 32 {
		return errors.New("JWT_SECRET must be at least 32 bytes")
	}
	for _, key := range []string{"DATABASE_URL", "MIGRATE_DATABASE_URL", "REDIS_URL"} {
		if getenv(key) == "" {
			return fmt.Errorf("%s is required", key)
		}
	}
	owner, err := pgx.Connect(ctx, getenv("MIGRATE_DATABASE_URL"))
	if err != nil {
		return fmt.Errorf("MIGRATE_DATABASE_URL: %w", err)
	}
	err = migrate.Up(ctx, owner, migrations.FS)
	closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
	closeErr := owner.Close(closeCtx)
	closeCancel()
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	if closeErr != nil {
		return fmt.Errorf("close migration connection: %w", closeErr)
	}
	pool, err := pgxpool.New(ctx, getenv("DATABASE_URL"))
	if err != nil {
		return fmt.Errorf("DATABASE_URL: %w", err)
	}
	defer pool.Close()
	// Validate the effective role before any request can bypass tenant policies.
	var safe bool
	err = pool.QueryRow(ctx, `SELECT current_user = 'lykeion_app'
   AND session_user = 'lykeion_app' AND NOT r.rolsuper AND NOT r.rolbypassrls
   AND NOT EXISTS (SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
     WHERE n.nspname='public' AND c.relkind IN ('r','p')
       AND pg_has_role(current_user,c.relowner,'USAGE'))
   FROM pg_roles r WHERE r.rolname=current_user`).Scan(&safe)
	if err != nil {
		return fmt.Errorf("DATABASE_URL readiness: %w", err)
	}
	if !safe {
		return errors.New("DATABASE_URL must use lykeion_app without superuser, BYPASSRLS or table ownership privileges")
	}
	opt, err := redis.ParseURL(getenv("REDIS_URL"))
	if err != nil {
		return fmt.Errorf("REDIS_URL: %w", err)
	}
	rdb := redis.NewClient(opt)
	defer rdb.Close()
	if err := rdb.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("REDIS_URL readiness: %w", err)
	}
	st := &store.Store{Pool: pool}
	hub := &realtime.Hub{Store: st, Redis: rdb}
	// Hijacked sockets must finish their database work before the pool closes.
	defer hub.Close()
	st.Publish = hub.Publish
	blobs, err := newStorage(ctx, getenv)
	if err != nil {
		return err
	}
	runs := &service.AgentRuns{Store: st, MCPURL: strings.TrimRight(getenv("PUBLIC_URL"), "/") + "/mcp/sources"}
	engine := &round.Engine{Store: st, AgentRuns: runs, Blobs: blobs, JevKey: getenv("TYPESAFE_API_KEY")}
	secretKey, err := seal.ParseKey(getenv("LYK_SECRET_KEY"))
	if err != nil {
		return err
	}
	ap := &autoresearch.Service{Store: st, AgentRuns: runs, Key: secretKey}
	runs.OnSettled = ap.OnSettled(engine.OnSettled)
	hosted := &sources.Hosted{OA: sources.OpenAlex{Base: sources.OpenAlexBase}, EPMC: sources.EuropePMC{Base: sources.EuropePMCBase},
		GEO: sources.GEO{Base: sources.EUtilsBase, MetadataBase: sources.GeoMetadataBase}, Store: st, Blobs: blobs}
	var mail auth.Mailer = auth.LogMailer{}
	if addr := getenv("SMTP_ADDR"); addr != "" {
		mail = auth.SMTPMailer{Addr: addr, From: getenv("SMTP_FROM"), User: getenv("SMTP_USER"), Pass: getenv("SMTP_PASS")}
	}
	h := &handler.H{Store: st, JWT: auth.Issuer{Secret: []byte(secret), TTL: 30 * 24 * time.Hour}, Mail: mail,
		Limit: auth.Limiter{R: rdb}, Secure: getenv("COOKIE_SECURE") != "false", Hub: hub,
		Autoresearch: ap, AgentRuns: runs, Blobs: blobs, Engine: engine, Sources: hosted.Handler(), Kinds: round.Kinds(),
		Google:    auth.GoogleDefaults(getenv("GOOGLE_CLIENT_ID"), getenv("GOOGLE_CLIENT_SECRET"), getenv("PUBLIC_URL")+"/auth/google/callback"),
		PublicURL: strings.TrimRight(getenv("PUBLIC_URL"), "/")}
	if err := runs.RecoverSettlements(ctx); err != nil {
		return fmt.Errorf("recover settlements: %w", err)
	}
	engineCtx, stopEngine := context.WithCancel(ctx)
	engineDone := make(chan struct{})
	go func() {
		defer close(engineDone)
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-engineCtx.Done():
				return
			case <-ticker.C:
				if err := engine.Tick(engineCtx); err != nil && engineCtx.Err() == nil {
					slog.Error("engine tick", "err", err)
				}
				if err := ap.Tick(engineCtx); err != nil && engineCtx.Err() == nil {
					slog.Error("autoresearch tick", "err", err)
				}
			}
		}
	}()
	defer func() { stopEngine(); <-engineDone }()
	sweepCtx, stopSweep := context.WithCancel(ctx)
	sweepDone := make(chan struct{})
	go func() {
		defer close(sweepDone)
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-sweepCtx.Done():
				return
			case <-ticker.C:
				if err := runs.Sweep(sweepCtx); err != nil && sweepCtx.Err() == nil {
					slog.Error("sweep", "err", err)
				}
			}
		}
	}()
	defer func() { stopSweep(); <-sweepDone }()
	addr := getenv("LISTEN_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	defer ln.Close()
	srv := &http.Server{Handler: handler.NewRouter(h), ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout: time.Minute, BaseContext: func(net.Listener) context.Context { return ctx }}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ln) }()
	defer srv.Close()
	if ready != nil {
		timer := time.NewTimer(time.Second)
		defer timer.Stop()
		select {
		case ready <- ln.Addr().String():
		case <-ctx.Done():
			return shutdown(srv, hub)
		case <-timer.C:
			return errors.New("ready channel delivery timed out")
		}
	}
	slog.Info("lykeion-server listening", "addr", ln.Addr().String())
	select {
	case <-ctx.Done():
		return shutdown(srv, hub)
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func shutdown(srv *http.Server, hub *realtime.Hub) error {
	if err := hub.Close(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(ctx)
}
