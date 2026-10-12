package main

import (
	"context"
	"github.com/lykeion-ia/lykeion-redesign/backend/internal/control"
	"github.com/lykeion-ia/lykeion-redesign/backend/internal/store"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	dsn, e := store.RequiredDSN(os.Getenv("LYKEION_DATABASE_URL"))
	if e != nil {
		log.Fatal(e)
	}
	db, e := store.Open(ctx, dsn)
	if e != nil {
		log.Fatal("database unavailable")
	}
	defer db.Close()
	if e = db.Migrate(ctx); e != nil {
		log.Fatal(e)
	}
	key, e := os.ReadFile(os.Getenv("LYKEION_SIGNING_KEY_FILE"))
	if e != nil {
		log.Fatal("signing key file required")
	}
	api, e := control.New(db, key, strings.Split(os.Getenv("LYKEION_ALLOWED_ORIGINS"), ","), os.Getenv("LYKEION_ALLOW_LOOPBACK_HTTP") != "1")
	if e != nil {
		log.Fatal(e)
	}
	api.ArtifactRoot = os.Getenv("LYKEION_ARTIFACT_ROOT")
	defer api.Close()
	addr := os.Getenv("LYKEION_LISTEN")
	if addr == "" {
		addr = "127.0.0.1:18431"
	}
	if os.Getenv("LYKEION_ALLOW_LOOPBACK_HTTP") == "1" && !strings.HasPrefix(addr, "127.0.0.1:") {
		log.Fatal("development HTTP must bind loopback")
	}
	server := &http.Server{Addr: addr, Handler: api.Handler(), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16384}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		server.Shutdown(shutdown)
	}()
	log.Printf("Lykeion control listening on %s", addr)
	if e = server.ListenAndServe(); e != nil && e != http.ErrServerClosed {
		log.Fatal(e)
	}
}
