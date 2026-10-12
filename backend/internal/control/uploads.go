package control

import (
	"encoding/base64"
	"encoding/json"
	"github.com/lykeion-ia/lykeion-redesign/backend/internal/artifacts"
	"github.com/lykeion-ia/lykeion-redesign/backend/internal/store"
	"net/http"
	"strings"
)

func (s *Server) upload(w http.ResponseWriter, r *http.Request, user, workspace, role string, parts []string) {
	if role != "owner" && role != "admin" {
		problem(w, 403, "ACCESS_DENIED")
		return
	}
	if s.ArtifactRoot == "" {
		problem(w, 503, "ARTIFACT_STORE_UNAVAILABLE")
		return
	}
	if len(parts) == 4 && r.Method == "POST" {
		var p struct {
			Hash string `json:"sha256"`
			Size int    `json:"byte_length"`
		}
		key := r.Header.Get("Idempotency-Key")
		if key == "" || len(key) > 128 || decode(w, r, &p) != nil || len(p.Hash) != 64 || strings.Trim(p.Hash, "0123456789abcdef") != "" || p.Size < 1 || p.Size > 65536 {
			problem(w, 422, "INVALID_UPLOAD")
			return
		}
		id := store.ID("upl")
		var oldHash string
		var size int
		err := s.DB.Pool.QueryRow(r.Context(), `INSERT INTO uploads(id,workspace_id,user_id,idempotency_key,sha256,byte_length) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(workspace_id,user_id,idempotency_key) DO UPDATE SET idempotency_key=uploads.idempotency_key RETURNING id,sha256,byte_length`, id, workspace, user, key, p.Hash, p.Size).Scan(&id, &oldHash, &size)
		if err != nil {
			problem(w, 503, "STORE_UNAVAILABLE")
			return
		}
		if oldHash != p.Hash || size != p.Size {
			problem(w, 409, "IDEMPOTENCY_MISMATCH")
			return
		}
		read(w, map[string]any{"id": id, "maximum_bytes": 65536, "part_count": 1}, "bootstrap")
		return
	}
	if len(parts) < 6 {
		problem(w, 404, "NOT_FOUND")
		return
	}
	id := parts[4]
	tx, err := s.DB.Pool.Begin(r.Context())
	if err != nil {
		problem(w, 503, "STORE_UNAVAILABLE")
		return
	}
	defer tx.Rollback(r.Context())
	var hash string
	var size int
	var data []byte
	var complete, expired bool
	if tx.QueryRow(r.Context(), "SELECT sha256,byte_length,bytes,complete,expires_at<=clock_timestamp() FROM uploads WHERE id=$1 AND workspace_id=$2 AND user_id=$3 FOR UPDATE", id, workspace, user).Scan(&hash, &size, &data, &complete, &expired) != nil {
		problem(w, 404, "NOT_FOUND")
		return
	}
	if len(parts) == 7 && parts[5] == "parts" && parts[6] == "1" && r.Method == "PUT" {
		var p struct {
			Data string `json:"content_base64"`
		}
		if decode(w, r, &p) != nil {
			problem(w, 422, "INVALID_PART")
			return
		}
		bytes, e := base64.StdEncoding.DecodeString(p.Data)
		if e != nil || len(bytes) != size || artifacts.Hash(bytes) != hash {
			problem(w, 422, "HASH_MISMATCH")
			return
		}
		if expired && !complete {
			problem(w, 409, "UPLOAD_EXPIRED")
			return
		}
		if _, err = tx.Exec(r.Context(), "UPDATE uploads SET bytes=$2 WHERE id=$1 AND complete=false", id, bytes); err != nil {
			problem(w, 503, "STORE_UNAVAILABLE")
			return
		}
		if tx.Commit(r.Context()) != nil {
			problem(w, 503, "STORE_UNAVAILABLE")
			return
		}
		read(w, map[string]any{"upload_id": id, "sha256": hash}, "bootstrap")
		return
	}
	if len(parts) != 6 || parts[5] != "complete" || r.Method != "POST" {
		problem(w, 404, "NOT_FOUND")
		return
	}
	if !complete {
		if expired {
			problem(w, 409, "UPLOAD_EXPIRED")
			return
		}
		if len(data) != size || artifacts.Hash(data) != hash {
			problem(w, 409, "UPLOAD_INCOMPLETE")
			return
		}
		if _, err = artifacts.Put(s.ArtifactRoot, data); err != nil {
			problem(w, 503, "ARTIFACT_STORE_UNAVAILABLE")
			return
		}
		if _, err = tx.Exec(r.Context(), "UPDATE uploads SET complete=true,bytes=NULL WHERE id=$1", id); err != nil {
			problem(w, 503, "STORE_UNAVAILABLE")
			return
		}
	}
	if _, err = artifacts.Read(s.ArtifactRoot, hash); err != nil {
		problem(w, 503, "ARTIFACT_INTEGRITY_FAILURE")
		return
	}
	if tx.Commit(r.Context()) != nil {
		problem(w, 503, "STORE_UNAVAILABLE")
		return
	}
	read(w, ref{Kind: "upload", ID: id, Revision: "1", SHA256: hash}, "bootstrap")
}

// Config artifacts are author-private until a programme explicitly references
// them. Artifact visibility is not a scientific acceptance verdict.
func (s *Server) artifactContent(w http.ResponseWriter, r *http.Request, user, workspace, id string) {
	var raw json.RawMessage
	var hash string
	err := s.DB.Pool.QueryRow(r.Context(), "SELECT manifest,sha256 FROM artifacts WHERE id=$1 AND workspace_id=$2 AND author_id=$3", id, workspace, user).Scan(&raw, &hash)
	if err != nil {
		problem(w, 404, "NOT_FOUND")
		return
	}
	read(w, map[string]any{"id": id, "revision": "1", "sha256": hash, "manifest": raw}, "bootstrap")
}
