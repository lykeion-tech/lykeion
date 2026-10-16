package handler

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"lykeion/server/internal/auth"
	"lykeion/server/internal/autoresearch"
	"lykeion/server/internal/seal"
)

const (
	maxWebhookBody  = 1 << 20
	webhookPerIP    = 120 // a minute
	webhookPerToken = 60  // a minute
)

// autoresearchWebhook is the public webhook route: the URL token is the
// credential, so it is rate limited per address and per token before any
// lookup, and only its hash is ever compared.
func (h *H) autoresearchWebhook(w http.ResponseWriter, r *http.Request) {
	hash := auth.Hash(chi.URLParam(r, "token"))
	for _, l := range []struct {
		key string
		n   int
	}{{"wh:ip:" + clientIP(r), webhookPerIP}, {"wh:token:" + hash, webhookPerToken}} {
		ok, err := h.Limit.Allow(r.Context(), l.key, l.n, time.Minute)
		if err != nil {
			writeErr(w, 503, "try again later")
			return
		}
		if !ok {
			writeErr(w, 429, "too many requests")
			return
		}
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWebhookBody))
	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) {
		writeErr(w, 413, "payload too large: at most 1 MiB")
		return
	}
	if err != nil {
		writeErr(w, 400, "unreadable body")
		return
	}
	rec, err := h.Autoresearch.Receive(r.Context(), hash, r.Header, body)
	switch {
	case err == nil:
		writeJSON(w, 200, rec)
	case errors.Is(err, autoresearch.ErrNoWebhook):
		writeErr(w, 404, "webhook not found")
	case errors.Is(err, autoresearch.ErrBadSignature):
		writeErr(w, 401, err.Error())
	case errors.Is(err, autoresearch.ErrBadPayload):
		writeErr(w, 400, err.Error())
	case errors.Is(err, seal.ErrNoKey):
		writeErr(w, 503, "the signing secret cannot be checked")
	default:
		slog.Error("request failed", "method", r.Method, "path", "/api/webhooks/autoresearch/{token}", "err", err)
		writeErr(w, 500, "internal error")
	}
}
