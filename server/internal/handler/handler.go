// Package handler serves the HTTP API. Handlers call sqlc queries directly,
// always inside a store transaction.
package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"lykeion/server/internal/auth"
	"lykeion/server/internal/middleware"
	"lykeion/server/internal/realtime"
	"lykeion/server/internal/service"
	"lykeion/server/internal/storage"
	"lykeion/server/internal/store"
)

type H struct {
	Store     *store.Store
	JWT       auth.Issuer
	Mail      auth.Mailer
	Limit     auth.Limiter
	Google    auth.Google
	Secure    bool // cookies only over HTTPS
	Hub       *realtime.Hub
	AgentRuns *service.AgentRuns
	Blobs     storage.Storage
	Kinds     map[string]service.Kind
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
	data, err := io.ReadAll(io.LimitReader(r.Body, middleware.MaxBodyBytes+1))
	if err != nil {
		return err
	}
	if len(data) > middleware.MaxBodyBytes {
		return errors.New("request body exceeds 1 MiB")
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
