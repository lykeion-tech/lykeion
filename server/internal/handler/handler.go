// Package handler serves the HTTP API. Handlers call sqlc queries directly,
// always inside a store transaction.
package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/netip"
	"time"

	"github.com/google/uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"lykeion/server/internal/auth"
	"lykeion/server/internal/autoresearch"
	"lykeion/server/internal/connectors"
	"lykeion/server/internal/flow"
	"lykeion/server/internal/middleware"
	"lykeion/server/internal/realtime"
	"lykeion/server/internal/service"
	"lykeion/server/internal/slack"
	"lykeion/server/internal/storage"
	"lykeion/server/internal/store"
	"lykeion/server/internal/workspacemcp"
)

type H struct {
	MCPBroker    *workspacemcp.Broker
	Autoresearch *autoresearch.Service
	Store        *store.Store
	JWT          auth.Issuer
	Mail         auth.Mailer
	Limit        auth.Limiter
	Google       auth.Google
	Secure       bool // cookies only over HTTPS
	Hub          *realtime.Hub
	Connectors   *connectors.Client // seals connector keys; Contact decides whether contact-bound connectors are ready
	Engine       *flow.Engine
	Slack        *slack.Service
	AgentRuns    *service.AgentRuns
	Blobs        storage.Storage
	Sources      http.Handler
	Lab          http.Handler
	PublicURL    string // where people reach the dashboard, without a trailing slash
	// TrustedProxies may forward the client address in X-Forwarded-For or
	// X-Real-IP; with none, rate limits key on the peer address.
	TrustedProxies []netip.Prefix
	Shipped        fs.FS // the packs Lykeion ships, one folder per name; nil ships none
	// ExtractText reads an upload's text, records.ExtractText when nil, within
	// TextDeadline, textDeadline when zero.
	ExtractText  func(name, contentType string, f io.ReaderAt, size int64) (*io.SectionReader, bool, error)
	TextDeadline time.Duration
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		json.NewEncoder(w).Encode(v)
	}
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func readJSON(r *http.Request, v any) error {
	limit := middleware.JSONBodyLimit(r)
	data, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil {
		return err
	}
	if int64(len(data)) > limit {
		return errors.New("request body exceeds endpoint limit")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return errors.New("expected one JSON value")
	}
	return nil
}

func isUnique(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "23505"
}

// fail answers 404 for a missing row and 500 for anything else.
func fail(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, pgx.ErrNoRows) {
		writeErr(w, 404, "not found")
		return
	}
	slog.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
	writeErr(w, 500, "internal error")
}

func (h *H) scope(r *http.Request) (uuid.UUID, uuid.UUID) {
	return middleware.WorkspaceFrom(r.Context()).ID, middleware.IdentityFrom(r.Context()).UserID
}
