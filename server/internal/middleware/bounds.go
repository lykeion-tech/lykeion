package middleware

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

const (
	MaxBodyBytes          = 1 << 20
	MaxDaemonMessageBytes = 32 << 20
	// MaxCallBodyBytes holds a 32 MiB answer and its JSON escapes.
	MaxCallBodyBytes    = 64 << 20
	BodyReadTimeout     = 2 * time.Second
	RequestTimeout      = 10 * time.Second
	BlobBodyReadTimeout = 30 * time.Second
	BlobRequestTimeout  = 60 * time.Second
	// A 5 GB study file at about 1 MB/s needs up to two hours to read,
	// with a third hour for storage and request completion.
	AttachmentBodyReadTimeout = 2 * time.Hour
	AttachmentRequestTimeout  = 3 * time.Hour
	// Outputs retain their 100 MB transfer budget.
	OutputBodyReadTimeout = 10 * time.Minute
	OutputRequestTimeout  = 15 * time.Minute
	// ToolsRequestTimeout bounds lyk tools' routing: three levels of Jev
	// requests of up to 60 s each, a level split when it is long.
	ToolsRequestTimeout = 4 * time.Minute
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
		attachment := r.Method == http.MethodPost && r.URL.Path == "/api/attachments"
		output := r.Method == http.MethodPost && r.URL.Path == "/api/role/outputs"
		coresearchUpload := coresearchFileTransfer(r, true) || coresearchWorkTransfer(r, true) || r.Method == http.MethodPost && r.URL.Path == "/api/coresearch/uploads"
		rawBlob := r.Method == http.MethodPost && r.URL.Path == "/api/role/blobs" || RuntimeSkillZIPUpload(r) || attachment || output || coresearchUpload
		callPost := r.Method == http.MethodPost && (r.URL.Path == "/api/role/calls" || r.URL.Path == "/api/role/runs")
		requestTimeout, bodyTimeout := RequestTimeout, BodyReadTimeout
		if rawBlob || callPost || r.Method == http.MethodPost && coresearchDaemonRoute(r, "cells", "finish") {
			requestTimeout, bodyTimeout = BlobRequestTimeout, BlobBodyReadTimeout
		}
		if attachment || attachmentDownload(r) && !outputDownload(r) {
			requestTimeout, bodyTimeout = AttachmentRequestTimeout, AttachmentBodyReadTimeout
		}
		if output || outputDownload(r) || coresearchUpload || coresearchFileTransfer(r, false) || coresearchWorkTransfer(r, false) || coresearchCellOutputDownload(r) || coresearchPaperTransfer(r) {
			requestTimeout, bodyTimeout = OutputRequestTimeout, OutputBodyReadTimeout
		}
		if r.Method == http.MethodPost && r.URL.Path == "/mcp/sources" {
			requestTimeout = SourcesRequestTimeout
		}
		if r.Method == http.MethodPost && r.URL.Path == "/api/role/tools" {
			requestTimeout = ToolsRequestTimeout
		}
		if workspaceMCPBrokerPost(r) {
			requestTimeout = 5 * time.Minute
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
			// Only the exact raw endpoints may defer reading until after authentication.
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
	// A one MiB document may expand sixfold in its JSON representation.
	if r.Method == http.MethodPost {
		if r.URL.Path == "/api/run/coresearch/paper/edit" {
			return 7 << 20
		}
		p := strings.Split(r.URL.Path, "/")
		if len(p) == 8 && p[1] == "api" && p[2] == "coresearch" && p[3] == "projects" && p[5] == "docs" && p[7] == "steps" {
			if _, e := uuid.Parse(p[4]); e == nil {
				if _, e := uuid.Parse(p[6]); e == nil {
					return 7 << 20
				}
			}
		}
	}

	if coresearchDaemonRoute(r, "cells", "finish") && r.Method == http.MethodPost {
		return 21 << 20
	}
	if r.Method == http.MethodPost && r.URL.Path == "/api/skills/import" {
		mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err == nil && mediaType == "multipart/form-data" {
			return 16 << 20
		}
	}

	// A one MiB UTF-8 main file may expand sixfold when JSON escapes it.
	if r.Method == http.MethodPatch {
		p := strings.Split(r.URL.Path, "/")
		if len(p) == 4 && p[1] == "api" && p[2] == "skills" && len(p[3]) == 36 {
			if _, err := uuid.Parse(p[3]); err == nil {
				return 7 << 20
			}
		}
	}
	if daemonMessagePost(r) {
		return MaxDaemonMessageBytes
	}
	if r.Method == http.MethodPost && (r.URL.Path == "/api/role/calls" || r.URL.Path == "/api/role/runs") {
		return MaxCallBodyBytes
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

// RuntimeSkillZIPUpload identifies only the daemon's selected-item archive result.
// Its handler authenticates the operation before spooling at the archive limit.
func RuntimeSkillZIPUpload(r *http.Request) bool {
	if r.Method != http.MethodPost {
		return false
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/zip" {
		return false
	}
	p := strings.Split(r.URL.Path, "/")
	if len(p) != 9 || p[1] != "api" || p[2] != "daemon" || p[3] != "runtimes" || p[5] != "local-skills" || p[6] != "import" || p[8] != "result" || len(p[4]) != 36 || len(p[7]) != 36 {
		return false
	}
	if _, err := uuid.Parse(p[4]); err != nil {
		return false
	}
	_, err = uuid.Parse(p[7])
	return err == nil
}

// workspaceMCPBrokerPost gives only the two daemon broker endpoints the door's
// existing call budget. Body size and read deadlines remain the ordinary ones.
func workspaceMCPBrokerPost(r *http.Request) bool {
	if r.Method != http.MethodPost {
		return false
	}
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) != 6 || parts[1] != "api" || parts[2] != "role" || parts[3] != "mcp-servers" || len(parts[4]) != 36 || (parts[5] != "tools" && parts[5] != "call") {
		return false
	}
	_, err := uuid.Parse(parts[4])
	return err == nil
}

// attachmentDownload matches GET /api/cards/{id}/attachments/{aid},
// GET /api/cards/{id}/outputs/{key} and a run's GET /api/role/attachments/{id}
// and GET /api/role/outputs/{key}. Outputs receive their separate budget.
func attachmentDownload(r *http.Request) bool {
	p := strings.Split(r.URL.Path, "/")
	if r.Method != http.MethodGet {
		return false
	}
	if len(p) == 7 && p[1] == "api" && p[2] == "daemon" && p[3] == "runs" && p[5] == "services" {
		_, e1 := uuid.Parse(p[4])
		_, e2 := uuid.Parse(p[6])
		return e1 == nil && e2 == nil
	}
	if len(p) == 5 && p[1] == "api" && p[2] == "role" {
		switch p[3] {
		case "attachments":
			_, err := uuid.Parse(p[4])
			return err == nil
		case "outputs":
			return p[4] != ""
		}
		return false
	}
	if len(p) != 6 || p[1] != "api" || p[2] != "cards" || p[5] == "" {
		return false
	}
	if _, err := uuid.Parse(p[3]); err != nil {
		return false
	}
	switch p[4] {
	case "attachments":
		_, err := uuid.Parse(p[5])
		return err == nil
	case "outputs":
		return true
	}
	return false
}

func outputDownload(r *http.Request) bool {
	if !attachmentDownload(r) {
		return false
	}
	p := strings.Split(r.URL.Path, "/")
	return len(p) == 5 && p[3] == "outputs" || len(p) == 6 && p[4] == "outputs"
}

// coresearchFileTransfer matches only the two streaming file routes.
func coresearchFileTransfer(r *http.Request, upload bool) bool {
	p := strings.Split(r.URL.Path, "/")
	if len(p) < 6 || p[1] != "api" || p[2] != "coresearch" || p[3] != "projects" || p[5] != "files" {
		return false
	}
	if _, err := uuid.Parse(p[4]); err != nil {
		return false
	}
	if upload {
		return r.Method == http.MethodPost && len(p) == 6
	}
	if r.Method != http.MethodGet || len(p) != 10 || p[7] != "versions" || p[9] != "content" {
		return false
	}
	if _, err := uuid.Parse(p[6]); err != nil {
		return false
	}
	n, err := strconv.ParseInt(p[8], 10, 32)
	return err == nil && n > 0
}

func coresearchDaemonRoute(r *http.Request, kind, action string) bool {
	p := strings.Split(r.URL.Path, "/")
	if len(p) != 7 || p[1] != "api" || p[2] != "daemon" || p[3] != "coresearch" || p[4] != kind || p[6] != action {
		return false
	}
	_, e := uuid.Parse(p[5])
	return e == nil
}
func coresearchWorkTransfer(r *http.Request, upload bool) bool {
	if upload {
		return r.Method == http.MethodPost && (r.URL.Path == "/api/run/coresearch/files" || coresearchDaemonRoute(r, "runs", "finish"))
	}
	if r.Method != http.MethodGet {
		return false
	}
	if r.URL.Path == "/api/run/coresearch/files/content" {
		return true
	}
	p := strings.Split(r.URL.Path, "/")
	return len(p) == 6 && p[1] == "api" && p[2] == "daemon" && p[3] == "coresearch" && p[4] == "blobs" && len(p[5]) == 64
}

// coresearchCellOutputDownload is an exact person output download, not an upload.
func coresearchCellOutputDownload(r *http.Request) bool {
	p := strings.Split(r.URL.Path, "/")
	if r.Method != http.MethodGet || len(p) != 10 || p[1] != "api" || p[2] != "coresearch" || p[3] != "projects" || p[5] != "chats" || p[7] != "cells" || p[9] != "outputs" {
		return false
	}
	for _, at := range []int{4, 6, 8} {
		if _, err := uuid.Parse(p[at]); err != nil {
			return false
		}
	}
	return true
}

func coresearchPaperTransfer(r *http.Request) bool {
	if r.Method == http.MethodGet && (strings.HasPrefix(r.URL.Path, "/api/run/coresearch/paper/") || coresearchDaemonRoute(r, "chats", "files")) {
		return true
	}
	p := strings.Split(r.URL.Path, "/")
	if r.Method == http.MethodPost && len(p) == 8 && p[1] == "api" && p[2] == "coresearch" && p[3] == "projects" && p[5] == "versions" && p[7] == "restore" {
		_, e := uuid.Parse(p[4])
		n, ne := strconv.Atoi(p[6])
		return e == nil && ne == nil && n > 0
	}
	return false
}
