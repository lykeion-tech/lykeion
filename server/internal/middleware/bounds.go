package middleware

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

const (
	MaxBodyBytes        = 1 << 20
	BodyReadTimeout     = 2 * time.Second
	RequestTimeout      = 10 * time.Second
	BlobBodyReadTimeout = 30 * time.Second
	BlobRequestTimeout  = 60 * time.Second
)

// RequestBounds buffers JSON before workspace transactions. Raw role uploads
// authenticate first and spool in their handler before opening a transaction.
// A network deadline actually interrupts stalled reads;
// the context also bounds database lock waits and external calls afterward.
func RequestBounds(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A WebSocket owns its connection and lifetime after the handshake.
		if r.Method == http.MethodGet && (r.URL.Path == "/ws" || r.URL.Path == "/api/daemon/ws") &&
			websocket.IsWebSocketUpgrade(r) {
			next.ServeHTTP(w, r)
			return
		}
		rawBlob := r.Method == http.MethodPost && r.URL.Path == "/api/role/blobs"
		requestTimeout, bodyTimeout := RequestTimeout, BodyReadTimeout
		if rawBlob {
			requestTimeout, bodyTimeout = BlobRequestTimeout, BlobBodyReadTimeout
		}
		ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
		defer cancel()
		r = r.WithContext(ctx)
		if r.Body != nil && r.Body != http.NoBody {
			controller := http.NewResponseController(w)
			deadline := time.Now().Add(bodyTimeout)
			if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
				deadline = d
			}
			if err := controller.SetReadDeadline(deadline); err != nil && !errors.Is(err, http.ErrNotSupported) {
				deny(w, http.StatusInternalServerError, "body read unavailable")
				return
			}
			// Only this exact raw endpoint may defer reading until after authentication.
			// Keep the deadline through the spool. Close these upload connections so
			// net/http never drains an unread body after early authentication failure.
			if rawBlob {
				w.Header().Set("Connection", "close")
				defer func() { _ = controller.SetReadDeadline(time.Now()) }()
				next.ServeHTTP(w, r)
				return
			}
			data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxBodyBytes))
			if err != nil {
				// Do not let net/http drain an incomplete or oversized body on keep-alive.
				w.Header().Set("Connection", "close")
				_ = controller.SetReadDeadline(time.Now())
				var tooLarge *http.MaxBytesError
				var network net.Error
				switch {
				case errors.As(err, &tooLarge):
					deny(w, http.StatusRequestEntityTooLarge, "request body exceeds 1 MiB")
				case errors.As(err, &network) && network.Timeout(), ctx.Err() != nil:
					deny(w, http.StatusRequestTimeout, "request body timed out")
				default:
					deny(w, http.StatusBadRequest, "invalid request body")
				}
				return
			}
			_ = controller.SetReadDeadline(time.Time{})
			r.Body = io.NopCloser(bytes.NewReader(data))
		}
		next.ServeHTTP(w, r)
	})
}
