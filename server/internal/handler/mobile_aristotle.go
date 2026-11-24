package handler

import (
	"github.com/google/uuid"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
	"net/http"
)

func (h *H) mobileAristotleConversations(w http.ResponseWriter, r *http.Request) {
	var out []db.ListMobileAristotleConversationsRow
	err := h.inWS(r, func(tx *store.Tx, ws, who uuid.UUID) error {
		var err error
		out, err = tx.Q.ListMobileAristotleConversations(r.Context())
		return err
	})
	if coresearchAnswer(w, r, err) {
		if out == nil {
			out = []db.ListMobileAristotleConversationsRow{}
		}
		writeJSON(w, 200, out)
	}
}
func (h *H) mobileAristotleMessages(w http.ResponseWriter, r *http.Request) {
	var out []db.ListCoresearchMessagesRow
	err := h.inWS(r, func(tx *store.Tx, ws, who uuid.UUID) error {
		id, err := coresearchID(r, "conversation")
		if err != nil {
			return err
		}
		chat, err := tx.Q.GetMobileAristotleChat(r.Context(), id)
		if err != nil {
			return err
		}
		out, err = tx.Q.ListCoresearchMessages(r.Context(), db.ListCoresearchMessagesParams{WorkspaceID: ws, ChatID: chat})
		return err
	})
	if coresearchAnswer(w, r, err) {
		if out == nil {
			out = []db.ListCoresearchMessagesRow{}
		}
		writeJSON(w, 200, out)
	}
}
