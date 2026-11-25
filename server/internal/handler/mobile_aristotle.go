package handler

import (
	"net/http"
	"strings"

	"github.com/google/uuid"

	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
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
		if err != nil {
			return err
		}
		prior := priorCoresearchTurns(out)
		for i, row := range out {
			if row.AgentRunID == nil {
				continue
			}
			draft, err := coresearchStudyDraft(r.Context(), tx, row, prior[*row.AgentRunID])
			if err != nil {
				return err
			}
			if draft != nil {
				// Installed mobile clients read the draft from the last plain-text paragraph.
				out[i].Body = strings.TrimSpace(draft.Prose + "\n\nStudy draft: " + strings.Join(strings.Fields(draft.Description), " "))
			}
		}
		return nil
	})
	if coresearchAnswer(w, r, err) {
		if out == nil {
			out = []db.ListCoresearchMessagesRow{}
		}
		writeJSON(w, 200, out)
	}
}
