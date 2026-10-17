package middleware

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"lykeion/server/internal/pack"
)

const (
	MaxBodyBytes          = 1 << 20
	MaxDaemonMessageBytes = 32 << 20
	BodyReadTimeout       = 2 * time.Second
	RequestTimeout        = 10 * time.Second
	BlobBodyReadTimeout   = 30 * time.Second
	BlobRequestTimeout    = 60 * time.Second
	// SourcesRequestTimeout bounds a sources-server call, which may wait on
	// two upstream calls of up to 30 s each before recording what it got.
	SourcesRequestTimeout = 60 * time.Second
)

// RequestBounds buffers JSON before workspace transactions. Raw role uploads
// authenticate first and spool in their handler before opening a transaction.
// A network deadline actually interrupts stalled reads;
// the context also bounds database lock waits and external calls afterward.
func RequestBounds(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A WebSocket owns its connection and lifetime after the handshake.
		if r.Method == http.MethodGet && (r.URL.Path == "/ws" || r.URL.Path == "/api/daemon/ws") &&
			websocket.IsWebSocketUpgrade(r) && validWebSocketHandshake(r) {
			next.ServeHTTP(w, r)
			return
		}
		rawBlob := r.Method == http.MethodPost && r.URL.Path == "/api/role/blobs"
		packUpload := r.Method == http.MethodPost && r.URL.Path == "/api/packs"
		requestTimeout, bodyTimeout := RequestTimeout, BodyReadTimeout
		if rawBlob || packUpload {
			requestTimeout, bodyTimeout = BlobRequestTimeout, BlobBodyReadTimeout
		}
		if r.Method == http.MethodPost && r.URL.Path == "/mcp/sources" {
			requestTimeout = SourcesRequestTimeout
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
			limit := JSONBodyLimit(r)
			data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
			if err != nil {
				// Do not let net/http drain an incomplete or oversized body on keep-alive.
				w.Header().Set("Connection", "close")
				_ = controller.SetReadDeadline(time.Now())
				var tooLarge *http.MaxBytesError
				var network net.Error
				switch {
				case errors.As(err, &tooLarge):
					deny(w, http.StatusRequestEntityTooLarge, "request body exceeds endpoint limit")
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

// Match only the UUID message route, not a permissive prefix.
func daemonMessagePost(r *http.Request) bool {
	if r.Method != http.MethodPost {
		return false
	}
	p := strings.Split(r.URL.Path, "/")
	if len(p) != 6 || p[1] != "api" || p[2] != "daemon" || p[3] != "agent-runs" || p[5] != "messages" || len(p[4]) != 36 {
		return false
	}
	_, err := uuid.Parse(p[4])
	return err == nil
}

// JSONBodyLimit is shared by pre-transaction buffering and strict decoding.
func JSONBodyLimit(r *http.Request) int64 {
	if daemonMessagePost(r) {
		return MaxDaemonMessageBytes
	}
	if r.Method == http.MethodPost && r.URL.Path == "/api/packs" {
		return pack.MaxArchive
	}
	return MaxBodyBytes
}

func validWebSocketHandshake(r *http.Request) bool {
	if r.Header.Get("Sec-WebSocket-Version") != "13" {
		return false
	}
	key, err := base64.StdEncoding.DecodeString(r.Header.Get("Sec-WebSocket-Key"))
	return err == nil && len(key) == 16
}
