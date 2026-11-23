package handler

import (
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"path"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

func (h *H) listResearchCoresearch(w http.ResponseWriter, r *http.Request) {
	projectID, ok := cardID(w, r)
	if !ok {
		return
	}
	var chats json.RawMessage
	err := h.inWS(r, func(tx *store.Tx, ws, _ uuid.UUID) error {
		if _, err := tx.Q.GetResearch(r.Context(), db.GetResearchParams{WorkspaceID: ws, ID: projectID}); err != nil {
			return err
		}
		var err error
		chats, err = tx.Q.ListResearchCoresearch(r.Context(), projectID)
		return err
	})
	if coresearchAnswer(w, r, err) {
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, chats)
	}
}

func (h *H) getResearchCoresearch(w http.ResponseWriter, r *http.Request) {
	projectID, ok := cardID(w, r)
	if !ok {
		return
	}
	conversationID, err := coresearchID(r, "conversation")
	if !coresearchAnswer(w, r, err) {
		return
	}
	var chat json.RawMessage
	err = h.inWS(r, func(tx *store.Tx, _, _ uuid.UUID) error {
		var err error
		chat, err = tx.Q.GetResearchCoresearch(r.Context(), db.GetResearchCoresearchParams{
			ResearchID: projectID, ConversationID: conversationID,
		})
		return err
	})
	if coresearchAnswer(w, r, err) {
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, chat)
	}
}

func (h *H) downloadResearchCoresearchArtifact(w http.ResponseWriter, r *http.Request) {
	ws, err := downloadWorkspace(r)
	if err != nil {
		coresearchAnswer(w, r, pgx.ErrNoRows)
		return
	}
	projectID, ok := cardID(w, r)
	if !ok {
		return
	}
	conversationID, err := coresearchID(r, "conversation")
	if !coresearchAnswer(w, r, err) {
		return
	}
	artifactID, err := coresearchID(r, "file")
	if !coresearchAnswer(w, r, err) {
		return
	}
	var file db.GetResearchCoresearchArtifactRow
	err = h.memberTx(r, ws, false, func(tx *store.Tx) error {
		var err error
		file, err = tx.Q.GetResearchCoresearchArtifact(r.Context(), db.GetResearchCoresearchArtifactParams{
			ResearchID: projectID, ConversationID: conversationID, ArtifactID: artifactID,
		})
		return err
	})
	if !coresearchAnswer(w, r, err) {
		return
	}
	body, err := h.Blobs.Get(r.Context(), file.BlobKey)
	if err != nil {
		logoFail(w, r, err)
		return
	}
	defer body.Close()
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": path.Base(file.Path)}))
	if file.Size >= 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(file.Size, 10))
	}
	_, _ = io.Copy(w, body)
}
