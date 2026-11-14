//go:build unix

package e2e

import (
	"context"
	"net/http"
	"os"
	"sync"
	"testing"

	"lykeion/server/internal/daemon"
)

// This controller starts and joins a real disposable daemon. Registration,
// heartbeat expiry and browser reads keep their normal product paths.
func uiAvailabilityFixture(t *testing.T, mux *http.ServeMux, cfg daemon.Config, authorized func(http.ResponseWriter, *http.Request) bool) {
	t.Helper()
	state, err := os.MkdirTemp("/tmp", "u2-availability-")
	must(t, err)
	cfg.StateDir, cfg.Home, cfg.Device = state, t.TempDir(), "ui2-availability"
	var mu sync.Mutex
	var cancel context.CancelFunc
	var done chan error
	stop := func() error {
		if cancel == nil {
			return nil
		}
		cancel()
		err := <-done
		cancel = nil
		return err
	}
	t.Cleanup(func() {
		mu.Lock()
		defer mu.Unlock()
		if err := stop(); err != nil {
			t.Error(err)
		}
		must(t, os.RemoveAll(state))
	})
	mux.HandleFunc("POST /lab/availability-daemon/{action}", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(w, r) {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		switch r.PathValue("action") {
		case "start":
			if cancel != nil {
				http.Error(w, "fixture daemon already running", 409)
				return
			}
			var ctx context.Context
			ctx, cancel = context.WithCancel(context.Background())
			done = make(chan error, 1)
			go func() { done <- daemon.Run(ctx, cfg) }()
		case "stop":
			if err := stop(); err != nil {
				http.Error(w, "fixture daemon shutdown failed", 500)
				return
			}
		default:
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(204)
	})
}
