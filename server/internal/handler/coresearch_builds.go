package handler

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/coresearch"
	"lykeion/server/internal/middleware"
	"lykeion/server/internal/service"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

// Public builds contain domain state only; daemon claim and receipt fields
// remain confined to the authenticated daemon work protocol.
type publicBuild struct {
	ID            uuid.UUID       `json:"id"`
	ProjectID     uuid.UUID       `json:"project_id"`
	Seq           int32           `json:"seq"`
	RequestedBy   uuid.UUID       `json:"requested_by"`
	Status        string          `json:"status"`
	Sources       json.RawMessage `json:"sources"`
	HasPDF        bool            `json:"has_pdf"`
	HasLog        bool            `json:"has_log"`
	HasSyncTeX    bool            `json:"has_synctex"`
	Pages         *int32          `json:"pages"`
	FailureReason *string         `json:"failure_reason"`
	CreatedAt     time.Time       `json:"created_at"`
	StartedAt     *time.Time      `json:"started_at"`
	FinishedAt    *time.Time      `json:"finished_at"`
}

func projectBuild(b db.CoresearchBuild) publicBuild {
	return publicBuild{ID: b.ID, ProjectID: b.ProjectID, Seq: b.Seq, RequestedBy: b.RequestedBy, Status: b.Status, Sources: b.Sources, HasPDF: b.PdfBlob != nil, HasLog: b.LogBlob != nil, HasSyncTeX: b.SynctexBlob != nil, Pages: b.Pages, FailureReason: b.FailureReason, CreatedAt: b.CreatedAt, StartedAt: b.StartedAt, FinishedAt: b.FinishedAt}
}

func (h *H) buildTransaction(r *http.Request, write bool) (coresearch.BuildTransaction, uuid.UUID, uuid.UUID) {
	role := middleware.RoleFrom(r.Context())
	if role.AgentRunID != uuid.Nil {
		return func(fn func(*store.Tx, uuid.UUID, uuid.UUID) error) error {
			return h.coresearchRunTx(r, write, func(tx *store.Tx, c db.CoresearchChat, _ uuid.UUID) error {
				if c.Mode != "cowrite" {
					return pgx.ErrNoRows
				}
				return fn(tx, c.ProjectID, c.OwnerID)
			})
		}, role.WorkspaceID, role.AgentRunID
	}
	ws, _ := uuid.Parse(r.Header.Get("X-Workspace-ID"))
	return func(fn func(*store.Tx, uuid.UUID, uuid.UUID) error) error {
		return h.coresearchSaveTx(r, ws, func(tx *store.Tx) error {
			p, e := coresearchProject(r, tx, ws, user(r), write, false)
			if e != nil {
				return e
			}
			if p.Mode != "cowrite" {
				return pgx.ErrNoRows
			}
			return fn(tx, p.ID, user(r))
		})
	}, ws, user(r)
}
func (h *H) coresearchBuilds(w http.ResponseWriter, r *http.Request) {
	transaction, ws, identity := h.buildTransaction(r, r.Method == "POST")
	if r.Method == "POST" {
		out, e := coresearch.QueueBuild(r.Context(), h.Store, h.Blobs, ws, identity, transaction)
		if paperAnswer(w, r, e) {
			if middleware.RoleFrom(r.Context()).AgentRunID != uuid.Nil {
				writeJSON(w, 201, map[string]any{"build_id": out.ID, "seq": out.Seq})
			} else {
				writeJSON(w, 201, map[string]any{"id": out.ID, "seq": out.Seq})
			}
		}
		return
	}
	var out []db.CoresearchBuild
	var single db.CoresearchBuild
	id := chi.URLParam(r, "id")
	asset := chi.URLParam(r, "asset")
	limit := 20
	if n, e := strconv.Atoi(r.URL.Query().Get("limit")); e == nil && n > 0 && n <= 100 {
		limit = n
	}
	e := transaction(func(tx *store.Tx, p, who uuid.UUID) error {
		var e error
		if id != "" {
			uid, e := uuid.Parse(id)
			if e != nil {
				return pgx.ErrNoRows
			}
			single, e = tx.Q.GetCoresearchBuild(r.Context(), db.GetCoresearchBuildParams{WorkspaceID: ws, ID: uid})
			if e != nil {
				return e
			}
			if single.ProjectID != p {
				return pgx.ErrNoRows
			}
			return nil
		}
		if asset != "" {
			seq, _, _ := strings.Cut(asset, ".")
			n, e := strconv.Atoi(seq)
			if e != nil {
				return pgx.ErrNoRows
			}
			single, e = tx.Q.GetCoresearchBuildSeq(r.Context(), db.GetCoresearchBuildSeqParams{WorkspaceID: ws, ProjectID: p, Seq: int32(n)})
			return e
		}
		out, e = tx.Q.ListCoresearchBuilds(r.Context(), db.ListCoresearchBuildsParams{WorkspaceID: ws, ProjectID: p, RowLimit: int32(limit)})
		if e != nil {
			return e
		}

		return nil
	})
	if !coresearchAnswer(w, r, e) {
		return
	}
	if asset != "" {
		var key *string
		ct := "text/plain; charset=utf-8"
		switch {
		case strings.HasSuffix(asset, ".pdf"):
			key = single.PdfBlob
			ct = "application/pdf"
		case strings.HasSuffix(asset, ".synctex.gz"):
			key = single.SynctexBlob
			ct = "application/gzip"
		case strings.HasSuffix(asset, ".log"):
			key = single.LogBlob
		}
		if key == nil {
			coresearchAnswer(w, r, pgx.ErrNoRows)
			return
		}
		rc, e := h.Blobs.Get(r.Context(), *key)
		if e != nil {
			fail(w, r, e)
			return
		}
		defer rc.Close()
		w.Header().Set("Content-Type", ct)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		io.Copy(w, rc)
		return
	}
	if id != "" {
		writeJSON(w, 200, projectBuild(single))
	} else {
		public := make([]publicBuild, 0, len(out))
		for _, b := range out {
			public = append(public, projectBuild(b))
		}
		writeJSON(w, 200, public)
	}
}
func (h *H) authorizeBuild(r *http.Request, tx *store.Tx, daemon string, id uuid.UUID, terminal bool) (db.CoresearchBuild, error) {
	b, e := tx.Q.GetCoresearchBuild(r.Context(), db.GetCoresearchBuildParams{WorkspaceID: tx.WS, ID: id})
	if e != nil {
		return b, e
	}
	if b.RequestedBy != user(r) || b.ClaimedDaemonID == nil || *b.ClaimedDaemonID != daemon {
		return b, pgx.ErrNoRows
	}
	if _, e = tx.Q.CoresearchMember(r.Context(), db.CoresearchMemberParams{WorkspaceID: tx.WS, ProjectID: b.ProjectID, UserID: b.RequestedBy}); e != nil {
		return b, e
	}
	p, e := tx.Q.LockCoresearchProject(r.Context(), db.LockCoresearchProjectParams{WorkspaceID: tx.WS, ID: b.ProjectID})
	if e != nil {
		return b, e
	}
	if p.ArchivedAt != nil {
		return b, pgx.ErrNoRows
	}
	return b, coresearchClaim(r, b.ClaimToken, b.LeaseExpiresAt, b.Status, b.ReceiptSha, terminal)
}
func (h *H) daemonCoresearchBuild(w http.ResponseWriter, r *http.Request) {
	id, e := coresearchID(r, "id")
	if !coresearchAnswer(w, r, e) {
		return
	}
	var b db.CoresearchBuild
	e = h.coresearchDaemonTx(r, func(tx *store.Tx, d string) error {
		var e error
		b, e = h.authorizeBuild(r, tx, d, id, false)
		if e != nil {
			return e
		}
		if r.Method == "POST" {
			if strings.HasSuffix(r.URL.Path, "/start") {
				return tx.Q.StartCoresearchBuild(r.Context(), db.StartCoresearchBuildParams{WorkspaceID: tx.WS, ID: id})
			}
			n, e := tx.Q.HeartbeatCoresearchBuild(r.Context(), db.HeartbeatCoresearchBuildParams{WorkspaceID: tx.WS, ID: id, ClaimToken: b.ClaimToken})
			if e != nil {
				return e
			}
			if n != 1 {
				return service.ErrConflict
			}
		}
		return nil
	})
	if coresearchAnswer(w, r, e) {
		writeJSON(w, 200, map[string]string{"status": b.Status})
	}
}
func (h *H) daemonCoresearchBuildFinish(w http.ResponseWriter, r *http.Request) {
	id, e := coresearchID(r, "id")
	if !coresearchAnswer(w, r, e) {
		return
	}
	var b db.CoresearchBuild
	check := func(tx *store.Tx, d string) error { var e error; b, e = h.authorizeBuild(r, tx, d, id, true); return e }
	if e = h.coresearchDaemonTx(r, check); !coresearchAnswer(w, r, e) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 3*coresearch.MaxFileBytes+multipartSlack)
	mr, e := r.MultipartReader()
	if e != nil {
		writeErr(w, 400, "expected multipart result")
		return
	}
	var meta struct {
		Status string `json:"status"`
		Pages  int32  `json:"pages"`
		Reason string `json:"reason"`
	}
	outputs := map[string]coresearchOutput{}
	defer func() {
		for _, out := range outputs {
			os.Remove(out.file)
		}
	}()
	seen := map[string]bool{}
	for {
		part, e := mr.NextPart()
		if errors.Is(e, io.EOF) {
			break
		}
		if e != nil {
			uploadReadErr(w, r, e, "build result too large")
			return
		}
		name := part.FormName()
		if seen[name] {
			writeErr(w, 400, "duplicate result part")
			return
		}
		seen[name] = true
		if name == "meta" {
			data, e := io.ReadAll(io.LimitReader(part, 65537))
			if e != nil || len(data) > 65536 || json.Unmarshal(data, &meta) != nil {
				writeErr(w, 400, "invalid metadata")
				return
			}
			continue
		}
		if name != "pdf" && name != "log" && name != "synctex" {
			writeErr(w, 400, "unknown build artifact")
			return
		}
		tmp, e := os.CreateTemp("", "lyk-build-result-")
		if e != nil {
			fail(w, r, e)
			return
		}
		hash := sha256.New()
		size, e := io.Copy(io.MultiWriter(tmp, hash), io.LimitReader(part, coresearch.MaxFileBytes+1))
		e = errors.Join(e, tmp.Close())
		outputs[name] = coresearchOutput{file: tmp.Name(), size: size, sha: hex.EncodeToString(hash.Sum(nil))}
		if e != nil {
			fail(w, r, e)
			return
		}
		if size > coresearch.MaxFileBytes {
			writeErr(w, 413, "build artifact exceeds 50 MiB")
			return
		}
	}
	if !seen["meta"] || (meta.Status != "ok" && meta.Status != "failed" && meta.Status != "aborted") || meta.Pages < 0 || len(meta.Reason) > 4096 || (meta.Status == "ok" && (!seen["pdf"] || !seen["synctex"])) {
		writeErr(w, 400, "invalid build result")
		return
	}
	digest := coresearchReceipt(struct {
		Meta   any
		Hashes map[string]string
	}{Meta: meta, Hashes: func() map[string]string {
		m := map[string]string{}
		for k, v := range outputs {
			m[k] = v.sha
		}
		return m
	}()})
	uploads := []coresearch.UploadBlob{}
	keys := map[string]*string{}
	for name, out := range outputs {
		key := coresearch.BlobKey(b.WorkspaceID, out.sha)
		keys[name] = &key
		uploads = append(uploads, coresearch.UploadBlob{SHA256: out.sha, Size: out.size, Open: func() (io.ReadCloser, error) { return os.Open(out.file) }})
	}
	quotaFailure := false
	e = coresearch.WithBlobs(r.Context(), h.Store, h.Blobs, b.WorkspaceID, user(r), uploads, func() error {
		err := h.coresearchDaemonTx(r, func(tx *store.Tx, d string) error {
			if e := check(tx, d); e != nil {
				return e
			}
			if b.Status != "running" {
				if b.ReceiptSha != nil && *b.ReceiptSha == digest {
					quotaFailure = b.Status == "failed" && b.FailureReason != nil && *b.FailureReason == "project_quota_exceeded"
					return nil
				}
				return service.ErrConflict
			}
			var reason *string
			if meta.Reason != "" {
				reason = &meta.Reason
			}
			params := db.FinishCoresearchBuildParams{WorkspaceID: tx.WS, ID: id, Status: meta.Status, PdfSize: outputs["pdf"].size, LogSize: outputs["log"].size, SynctexSize: outputs["synctex"].size, PdfBlob: keys["pdf"], LogBlob: keys["log"], SynctexBlob: keys["synctex"], Pages: &meta.Pages, FailureReason: reason, ReceiptSha: &digest}
			var row db.CoresearchBuild
			e := tx.Savepoint(r.Context(), func(candidate *store.Tx) error {
				var e error
				row, e = candidate.Q.FinishCoresearchBuild(r.Context(), params)
				if e != nil {
					return e
				}
				used, e := candidate.Q.CoresearchProjectBytes(r.Context(), db.CoresearchProjectBytesParams{WorkspaceID: tx.WS, ProjectID: b.ProjectID, Sha256: ""})
				if e != nil {
					return e
				}
				if used > coresearch.MaxProjectBytes {
					return coresearch.ErrQuota
				}
				return nil
			})
			if errors.Is(e, coresearch.ErrQuota) {
				quotaFailure = true
				reason := "project_quota_exceeded"
				row, e = tx.Q.FinishCoresearchBuild(r.Context(), db.FinishCoresearchBuildParams{WorkspaceID: tx.WS, ID: id, Status: "failed", FailureReason: &reason, ReceiptSha: &digest})
			}
			if e != nil {
				return e
			}
			if row.Status == "ok" {
				_, e = tx.Q.InsertBuildVersion(r.Context(), db.InsertBuildVersionParams{WorkspaceID: tx.WS, ProjectID: b.ProjectID, Title: fmt.Sprintf("Build %d", b.Seq), ByUserID: &b.RequestedBy, BuildID: &b.ID, Files: b.Sources})
				if e != nil {
					return e
				}
			}
			return coresearch.BuildChanged(r.Context(), tx, row)
		})
		// The terminal failure is committed. Signal WithBlobs to remove only
		// newly staged, unreferenced artifacts before acknowledging delivery.
		if err == nil && quotaFailure {
			return coresearch.ErrQuota
		}
		return err
	})
	if e == coresearch.ErrQuota {
		e = nil
	} else if quotaFailure && e != nil {
		// Cleanup/transport errors must remain retryable even though the
		// terminal quota result itself is already durably committed.
		fail(w, r, e)
		return
	}
	if coresearchAnswer(w, r, e) {
		w.WriteHeader(204)
	}
}
