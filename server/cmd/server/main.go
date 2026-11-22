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

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"lykeion/server/internal/auth"
	"lykeion/server/internal/autoresearch"
	"lykeion/server/internal/connectors"
	"lykeion/server/internal/coresearch"
	"lykeion/server/internal/embed"
	"lykeion/server/internal/flow"
	"lykeion/server/internal/github"
	"lykeion/server/internal/handler"
	"lykeion/server/internal/jev"
	"lykeion/server/internal/labindex"
	"lykeion/server/internal/labmemory"
	"lykeion/server/internal/migrate"
	"lykeion/server/internal/pack"
	"lykeion/server/internal/realtime"
	"lykeion/server/internal/seal"
	"lykeion/server/internal/service"
	"lykeion/server/internal/slack"
	"lykeion/server/internal/sources"
	"lykeion/server/internal/storage"
	"lykeion/server/internal/store"
	"lykeion/server/internal/team"
	"lykeion/server/migrations"
	db "lykeion/server/pkg/db/generated"
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
	mode := getenv("MIGRATIONS_MODE")
	if mode != "" && mode != "auto" && mode != "external" {
		return errors.New("MIGRATIONS_MODE must be auto or external")
	}
	databaseRole := getenv("DATABASE_ROLE")
	if databaseRole == "" {
		databaseRole = "lykeion_app"
	}
	if databaseRole != "lykeion_app" && databaseRole != "lykeion_app_v2" {
		return errors.New("DATABASE_ROLE must be lykeion_app or lykeion_app_v2")
	}
	if databaseRole == "lykeion_app_v2" && mode != "external" {
		return errors.New("DATABASE_ROLE lykeion_app_v2 requires MIGRATIONS_MODE=external")
	}
	for _, key := range []string{"DATABASE_URL", "REDIS_URL"} {
		if getenv(key) == "" {
			return fmt.Errorf("%s is required", key)
		}
	}
	if mode != "external" {
		if getenv("MIGRATE_DATABASE_URL") == "" {
			return errors.New("MIGRATE_DATABASE_URL is required")
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
	}
	pool, err := pgxpool.New(ctx, getenv("DATABASE_URL"))
	if err != nil {
		return fmt.Errorf("DATABASE_URL: %w", err)
	}
	defer pool.Close()
	// Validate the effective role before any request can bypass tenant policies.
	var safe bool
	roleCheck := `SELECT current_user = 'lykeion_app'
   AND session_user = 'lykeion_app' AND NOT r.rolsuper AND NOT r.rolbypassrls
   AND NOT EXISTS (SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
     WHERE n.nspname='public' AND c.relkind IN ('r','p')
       AND pg_has_role(current_user,c.relowner,'USAGE'))
   FROM pg_roles r WHERE r.rolname=current_user`
	if databaseRole == "lykeion_app_v2" {
		roleCheck = restrictedV2RoleCheck
	}
	err = pool.QueryRow(ctx, roleCheck).Scan(&safe)
	if err != nil {
		return fmt.Errorf("DATABASE_URL readiness: %w", err)
	}
	if !safe {
		return fmt.Errorf("DATABASE_URL does not meet restricted %s role requirements", databaseRole)
	}
	if mode == "external" {
		versions, err := migrate.Versions(migrations.FS)
		if err != nil {
			return fmt.Errorf("migration versions unavailable: %w", err)
		}
		if len(versions) == 0 {
			return errors.New("migration versions unavailable: no embedded migrations")
		}
		tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
		if err != nil {
			return fmt.Errorf("migration compatibility transaction: %w", err)
		}
		defer func() {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = tx.Rollback(cleanupCtx)
		}()
		var locked bool
		if err := tx.QueryRow(ctx, `SELECT pg_catalog.pg_try_advisory_xact_lock_shared($1::bigint)`, migrate.LockKey).Scan(&locked); err != nil {
			return fmt.Errorf("migration compatibility lock: %w", err)
		}
		if !locked {
			return errors.New("migration compatibility check failed: a migration is in progress")
		}
		var compatible bool
		if err := tx.QueryRow(ctx, `SELECT public.lykeion_schema_compatible($1::integer[])`, versions).Scan(&compatible); err != nil {
			return fmt.Errorf("migration compatibility check: %w", err)
		}
		if !compatible {
			return errors.New("migration compatibility check failed: apply the migrations for this release before starting the server")
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("migration compatibility transaction: %w", err)
		}
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
	ids, err := st.Global().AllWorkspaceIDs(ctx)
	if err != nil {
		return fmt.Errorf("list workspaces: %w", err)
	}
	for _, id := range ids {
		err := st.InWorkspace(ctx, id, uuid.Nil, func(tx *store.Tx) error {
			if err := pack.InstallShipped(ctx, tx, blobs, pack.Shipped()); err != nil {
				return err
			}
			return team.InstallLaboratory(ctx, tx)
		})
		if err != nil {
			return fmt.Errorf("install shipped Teams and laboratory in %s: %w", id, err)
		}
	}
	publicURL := strings.TrimRight(getenv("PUBLIC_URL"), "/")
	runs := &service.AgentRuns{Store: st, Blobs: blobs, MCPURL: publicURL + "/mcp/sources", LabMCPURL: publicURL + "/mcp/lab"}
	engine := &flow.Engine{Store: st, AgentRuns: runs, Blobs: blobs}
	// Optional local embeddings: live record tools remain usable without Search.
	var labIndex *labindex.Index
	if endpoint := getenv("EMBEDDINGS_URL"); endpoint != "" {
		candidate := labindex.New(rdb, embed.TEI{URL: endpoint}, embed.Model)
		labIndex = candidate // Keep retrying optional Search after startup outages.
		readyCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := candidate.Ensure(readyCtx)
		if err == nil {
			labIndex = candidate
			_, err = (embed.TEI{URL: endpoint}).Embed(readyCtx, []string{"readiness"}, false)
		}
		cancel()
		if err != nil {
			slog.Warn("lab search unavailable; using live record tools")
		}
	}
	labTools := labmemory.Tools{Store: st, Blobs: blobs}
	if labIndex != nil {
		labTools.Index = labIndex
	}
	labIndexer := &labindex.Indexer{Store: st, Index: labIndex}
	indexCtx, stopIndex := context.WithCancel(ctx)
	indexDone := make(chan struct{})
	go func() {
		defer close(indexDone)
		if labIndex == nil {
			return
		}
		labIndexer.Run(indexCtx, 5*time.Second, func(error) { slog.Warn("lab index tick failed; will retry") })
	}()
	defer func() { stopIndex(); <-indexDone }()

	secretKey, err := seal.ParseKey(getenv("LYK_SECRET_KEY"))
	if err != nil {
		return err
	}
	// Jev reviews a study only under its owner's own TypeSafe key.
	engine.JevKey = func(ctx context.Context, user uuid.UUID) (string, error) {
		sealed, err := st.Global().GetUserTypesafeKey(ctx, user)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		if err != nil {
			return "", err
		}
		return jev.OpenKey(secretKey, user, sealed)
	}
	ap := &autoresearch.Service{Store: st, AgentRuns: runs, Engine: engine, Key: secretKey}
	runs.OnSettlement = (&coresearch.Service{Store: st, Blobs: blobs}).OnSettlement((labmemory.Answers{Store: st}).SettlementHook(ap.OnSettlement(engine.OnSettlement)))
	conn := &connectors.Client{HTTP: connectors.Guarded(), Secret: secretKey, Contact: getenv("CONTACT_EMAIL")}
	runs.Keys = conn
	hosted := &sources.Hosted{Conn: conn, OA: sources.OpenAlex{Base: sources.OpenAlexBase, Mailto: conn.Contact, Key: getenv("OPENALEX_API_KEY")}, EPMC: sources.EuropePMC{Base: sources.EuropePMCBase}, AX: &sources.Arxiv{Base: sources.ArxivBase},
		GEO: sources.GEO{Base: sources.EUtilsBase, MetadataBase: sources.GeoMetadataBase}, Store: st, Blobs: blobs}
	if path := getenv("SOURCES_EXCLUDE_FILE"); path != "" {
		if hosted.Exclude, err = sources.LoadExclusions(path); err != nil {
			return fmt.Errorf("SOURCES_EXCLUDE_FILE: %w", err)
		}
	}
	engine.Sources = sources.Lookup{OpenAlex: hosted.OA, Arxiv: hosted.AX, EuropePMC: hosted.EPMC, GEO: hosted.GEO, Exclude: hosted.Exclude}
	var mail auth.Mailer = auth.LogMailer{}
	if addr := getenv("SMTP_ADDR"); addr != "" {
		mail = auth.SMTPMailer{Addr: addr, From: getenv("SMTP_FROM"), User: getenv("SMTP_USER"), Pass: getenv("SMTP_PASS")}
	}
	h := &handler.H{Store: st, JWT: auth.Issuer{Secret: []byte(secret), TTL: 30 * 24 * time.Hour}, Mail: mail,
		Limit: auth.Limiter{R: rdb}, Secure: getenv("COOKIE_SECURE") != "false", Hub: hub,
		Autoresearch: ap, AgentRuns: runs, Blobs: blobs, Engine: engine, Sources: hosted.Handler(), Lab: labTools.Handler(), Connectors: conn,
		Google:    auth.GoogleDefaults(getenv("GOOGLE_CLIENT_ID"), getenv("GOOGLE_CLIENT_SECRET"), getenv("PUBLIC_URL")+"/auth/google/callback"),
		PublicURL: strings.TrimRight(getenv("PUBLIC_URL"), "/"), Shipped: pack.Shipped(),
		TrustedProxies: handler.ParseTrustedProxies(getenv("TRUSTED_PROXIES"))}
	githubConfig, configErr := github.ConfigFromEnv(getenv)
	if configErr != nil {
		slog.Warn("GitHub App configuration invalid", "err", configErr)
		githubConfig = github.Config{WebhookSecret: getenv("GITHUB_WEBHOOK_SECRET")}
	}
	h.GitHub = &github.Service{Background: ctx, Store: st, Config: githubConfig, PublicURL: h.PublicURL, StateSecret: h.JWT.Secret}
	if githubConfig.CanConnect() {
		go h.GitHub.ResumeImports(ctx)
	}
	h.Slack = &slack.Service{Store: st, Key: secretKey, Redis: rdb, AgentRuns: runs, Drafter: h, PublicURL: h.PublicURL,
		DeleteChat: func(ctx context.Context, tx *store.Tx, user uuid.UUID, chat db.LabChat) error {
			return handler.DeleteLabChatInTx(ctx, tx, runs, user, chat)
		}}
	if err := runs.CancelUnavailableLabRuns(ctx); err != nil {
		return fmt.Errorf("cancel unavailable lab runs: %w", err)
	}
	if err := runs.RecoverSettlements(ctx); err != nil {
		return fmt.Errorf("recover settlements: %w", err)
	}
	if secretKey != nil { // Slack tokens are sealed: no key, no connections
		slackCtx, stopSlack := context.WithCancel(ctx)
		slackDone := make(chan struct{})
		go func() { defer close(slackDone); h.Slack.Run(slackCtx) }()
		defer func() { stopSlack(); <-slackDone }()
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
	skillSweeper := service.NewRuntimeSkillSweeper(st)
	skillCtx, stopSkill := context.WithCancel(ctx)
	skillDone := make(chan struct{})
	go func() {
		defer close(skillDone)
		timer := time.NewTimer(30 * time.Second)
		defer timer.Stop()
		for {
			select {
			case <-skillCtx.Done():
				return
			case <-timer.C:
				more, err := skillSweeper.SweepMore(skillCtx)
				delay := runtimeSkillSweepDelay(more, err)
				if err != nil && skillCtx.Err() == nil {
					slog.Error("runtime skill sweep", "err", err)
				}
				timer.Reset(delay)
			}
		}
	}()
	defer func() { stopSkill(); <-skillDone }()
	sweepCtx, stopSweep := context.WithCancel(ctx)
	sweepDone := make(chan struct{})
	go func() {
		defer close(sweepDone)
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		if err := coresearch.SweepBlobCleanup(sweepCtx, st, blobs); err != nil && sweepCtx.Err() == nil {
			slog.Error("Coresearch blob cleanup", "err", err)
		}
		for {
			select {
			case <-sweepCtx.Done():
				return
			case <-ticker.C:
				if err := coresearch.SweepBlobCleanup(sweepCtx, st, blobs); err != nil && sweepCtx.Err() == nil {
					slog.Error("Coresearch blob cleanup", "err", err)
				}
				if err := coresearch.Sweep(sweepCtx, st); err != nil && sweepCtx.Err() == nil {
					slog.Error("Coresearch sweep", "err", err)
				}
				if err := runs.CancelUnavailableLabRuns(sweepCtx); err != nil && sweepCtx.Err() == nil {
					slog.Error("cancel unavailable lab runs", "err", err)
				}
				if err := runs.Sweep(sweepCtx); err != nil && sweepCtx.Err() == nil {
					slog.Error("sweep", "err", err)
				}
			}
		}
	}()
	defer func() { stopSweep(); <-sweepDone }()
	attachCtx, stopAttach := context.WithCancel(ctx)
	attachDone := make(chan struct{})
	go func() {
		defer close(attachDone)
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			if err := handler.SweepAttachments(attachCtx, st, blobs, time.Now); err != nil && attachCtx.Err() == nil {
				slog.Error("attachment sweep", "err", err)
			}
			select {
			case <-attachCtx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	defer func() { stopAttach(); <-attachDone }()
	addr := getenv("LISTEN_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	defer ln.Close()
	router := handler.NewRouter(h)
	defer h.MCPBroker.Close()
	srv := &http.Server{Handler: router, ReadHeaderTimeout: 10 * time.Second,
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

// Errors back off even when a pass also exhausted its budget.
func runtimeSkillSweepDelay(more bool, err error) time.Duration {
	if more && err == nil {
		return time.Second
	}
	return 30 * time.Second
}
