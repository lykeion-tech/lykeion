package handler

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"io"
	"lykeion/server/internal/coresearch"
	"lykeion/server/internal/service"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
	"net/http"
	"os"
	"strconv"
	"strings"
)

func (h *H) coresearchRunFiles(w http.ResponseWriter, r *http.Request) {
	content := strings.HasSuffix(r.URL.Path, "/content")
	var out []coresearch.FileRef
	var v db.CoresearchFileVersion
	e := h.coresearchRunTx(r, false, func(tx *store.Tx, c db.CoresearchChat, _ uuid.UUID) error {
		if !content {
			var e error
			out, e = coresearch.FileRefs(r.Context(), tx, c.ProjectID)
			return e
		}
		clean, e := coresearch.CleanPath(r.URL.Query().Get("path"))
		if e != nil {
			return pgx.ErrNoRows
		}
		f, e := tx.Q.CoresearchFileByPath(r.Context(), db.CoresearchFileByPathParams{WorkspaceID: tx.WS, ProjectID: c.ProjectID, Path: clean})
		if e != nil {
			return e
		}
		if raw := r.URL.Query().Get("n"); raw != "" {
			n, e := strconv.ParseInt(raw, 10, 32)
			if e != nil || n < 1 {
				return pgx.ErrNoRows
			}
			v, e = tx.Q.CoresearchFileVersion(r.Context(), db.CoresearchFileVersionParams{WorkspaceID: tx.WS, FileID: f.ID, N: int32(n)})
			return e
		}
		v, e = tx.Q.CoresearchFileHead(r.Context(), db.CoresearchFileHeadParams{WorkspaceID: tx.WS, FileID: f.ID})
		return e
	})
	if !coresearchAnswer(w, r, e) {
		return
	}
	if !content {
		writeJSON(w, 200, out)
		return
	}
	rc, e := h.Blobs.Get(r.Context(), v.BlobKey)
	if e != nil {
		logoFail(w, r, e)
		return
	}
	defer rc.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = io.Copy(w, rc)
}

type coresearchOutput struct {
	path, sha string
	size      int64
	file      *os.File
}

func (h *H) daemonCoresearchRunFinish(w http.ResponseWriter, r *http.Request) {
	id, e := coresearchID(r, "id")
	if !coresearchAnswer(w, r, e) {
		return
	}
	var run db.CoresearchRun
	var c db.CoresearchChat
	check := func(tx *store.Tx, d string) error {
		var e error
		run, e = tx.Q.GetCoresearchRun(r.Context(), db.GetCoresearchRunParams{WorkspaceID: tx.WS, ID: id})
		if e != nil {
			return e
		}
		c, e = daemonCoresearchChat(r, tx, run.ChatID, d)
		if e != nil {
			return e
		}
		if run.ClaimedDaemonID == nil || *run.ClaimedDaemonID != d {
			return pgx.ErrNoRows
		}
		if run.Status != "running" {
			return service.ErrConflict
		}
		return nil
	}
	if e = h.coresearchDaemonTx(r, check); !coresearchAnswer(w, r, e) {
		return
	}
	// Per-file limits are independent of the multipart aggregate project budget.
	r.Body = http.MaxBytesReader(w, r.Body, coresearch.MaxProjectBytes+multipartSlack)
	mr, e := r.MultipartReader()
	if e != nil {
		writeErr(w, 400, "expected multipart result")
		return
	}
	outputs := []coresearchOutput{}
	defer func() {
		for _, out := range outputs {
			out.file.Close()
			os.Remove(out.file.Name())
		}
	}()
	var facts map[string]json.RawMessage
	seen := map[string]bool{}
	outputPaths := map[string]bool{}
	var total int64
	for {
		part, e := mr.NextPart()
		if errors.Is(e, io.EOF) {
			break
		}
		if e != nil {
			uploadReadErr(w, r, e, "run outputs exceed project budget")
			return
		}
		name := part.FormName()
		if seen[name] {
			writeErr(w, 400, "duplicate result field")
			return
		}
		seen[name] = true
		if name == "facts" {
			b, e := io.ReadAll(io.LimitReader(part, (1<<20)+1))
			if e != nil || len(b) > 1<<20 || json.Unmarshal(b, &facts) != nil || facts == nil {
				writeErr(w, 400, "invalid facts object")
				return
			}
			continue
		}
		raw, ok := strings.CutPrefix(name, "out/")
		if !ok {
			writeErr(w, 400, "expected facts or out/path")
			return
		}
		clean, e := coresearch.CleanPath(raw)
		if e != nil {
			writeErr(w, 400, e.Error())
			return
		}
		clean, e = coresearch.CleanPath("results/" + clean)
		if e != nil {
			writeErr(w, 400, e.Error())
			return
		}
		if outputPaths[clean] {
			writeErr(w, 400, "duplicate output path")
			return
		}
		outputPaths[clean] = true
		tmp, e := os.CreateTemp("", "lyk-coresearch-result-*")
		if e != nil {
			fail(w, r, e)
			return
		}
		outputs = append(outputs, coresearchOutput{path: clean, file: tmp})
		out := &outputs[len(outputs)-1]
		hash := sha256.New()
		out.size, e = io.Copy(io.MultiWriter(tmp, hash), io.LimitReader(part, coresearch.MaxFileBytes+1))
		if e != nil {
			uploadReadErr(w, r, e, "output file exceeds 50 MiB")
			return
		}
		if out.size > coresearch.MaxFileBytes {
			writeErr(w, 413, "output file exceeds 50 MiB")
			return
		}
		total += out.size
		if total > coresearch.MaxProjectBytes {
			writeErr(w, 413, "run outputs exceed project budget")
			return
		}
		out.sha = hex.EncodeToString(hash.Sum(nil))
		if _, e = tmp.Seek(0, io.SeekStart); e != nil {
			fail(w, r, e)
			return
		}
	}
	if facts == nil {
		writeErr(w, 400, "facts required")
		return
	}
	var status, env string
	if json.Unmarshal(facts["status"], &status) != nil || (status != "ok" && status != "failed") {
		writeErr(w, 400, "facts.status must be ok or failed")
		return
	}
	if b := facts["env_sha"]; len(b) > 0 {
		if json.Unmarshal(b, &env) != nil || env != "" && !validSHA(env) {
			writeErr(w, 400, "invalid env_sha")
			return
		}
	}
	if status == "ok" && env == "" {
		writeErr(w, 400, "successful run requires env_sha")
		return
	}
	if status == "ok" && run.ExpectedEnvSha != nil && env != *run.ExpectedEnvSha {
		writeErr(w, 409, "environment fingerprint does not match original")
		return
	}
	hashes := map[string]string{}
	blobs := []coresearch.UploadBlob{}
	for _, out := range outputs {
		hashes[out.path] = out.sha
		blobs = append(blobs, coresearch.UploadBlob{SHA256: out.sha, Body: out.file, Size: out.size})
	}
	facts["outputs"], _ = json.Marshal(hashes)
	delete(facts, "matches_original")
	e = coresearch.WithBlobs(r.Context(), h.Store, h.Blobs, run.WorkspaceID, user(r), blobs, func() error {
		return h.coresearchDaemonTx(r, func(tx *store.Tx, d string) error {
			if e := check(tx, d); e != nil {
				return e
			}
			for _, out := range outputs {
				if _, e := coresearch.SaveVersion(r.Context(), tx, coresearch.SaveIn{Workspace: tx.WS, Project: c.ProjectID, Path: out.path, Sha256: out.sha, Size: out.size, Force: true, Run: &run.ID}); e != nil {
					return e
				}
			}
			if run.RerunOf != nil {
				rows, e := tx.Q.CoresearchRunOutputs(r.Context(), db.CoresearchRunOutputsParams{WorkspaceID: tx.WS, RunID: run.RerunOf})
				if e != nil {
					return e
				}
				original := map[string]string{}
				for _, row := range rows {
					original[row.Path] = row.Sha256
				}
				facts["matches_original"], _ = json.Marshal(coresearch.OutputsMatch(original, hashes))
			}
			b, e := json.Marshal(facts)
			if e != nil {
				return e
			}
			var envptr *string
			if env != "" {
				envptr = &env
			}
			if _, e = tx.Q.FinishCoresearchRun(r.Context(), db.FinishCoresearchRunParams{WorkspaceID: tx.WS, ID: run.ID, Status: status, EnvSha: envptr, Facts: b}); e != nil {
				return e
			}
			return coresearch.Changed(r.Context(), tx, "run", c, run.ID)
		})
	})
	if coresearchAnswer(w, r, e) {
		w.WriteHeader(204)
	}
}
