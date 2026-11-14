package handler

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

type commentFile struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	ContentType string    `json:"content_type"`
	SizeBytes   int64     `json:"size_bytes"`
}

func commentFileViews(files []db.Attachment) []commentFile {
	out := make([]commentFile, 0, len(files))
	for _, f := range files {
		out = append(out, commentFile{f.ID, f.Name, f.ContentType, f.SizeBytes})
	}
	return out
}
func (h *H) downloadCommentAttachment(w http.ResponseWriter, r *http.Request) {
	ws, e1 := downloadWorkspace(r)
	card, e2 := uuid.Parse(chi.URLParam(r, "id"))
	comment, e3 := uuid.Parse(chi.URLParam(r, "comment"))
	aid, e4 := uuid.Parse(chi.URLParam(r, "aid"))
	if errors.Join(e1, e2, e3, e4) != nil {
		writeErr(w, 404, "not found")
		return
	}
	var file db.Attachment
	err := h.memberTx(r, ws, false, func(tx *store.Tx) error {
		var err error
		file, err = tx.Q.CommentAttachment(r.Context(), db.CommentAttachmentParams{WorkspaceID: ws, CardID: card, CommentID: comment, ID: aid})
		return err
	})
	if err != nil {
		logoFail(w, r, err)
		return
	}
	rc, err := h.Blobs.Get(r.Context(), file.BlobKey)
	if err != nil {
		logoFail(w, r, err)
		return
	}
	defer rc.Close()
	serveFile(w, r, file.Name, file.ContentType, file.SizeBytes, rc)
}
