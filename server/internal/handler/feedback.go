package handler

import (
	"net/http"
	"strings"

	"lykeion/server/internal/auth"
)

func (h *H) sendFeedback(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Message string `json:"message"`
	}
	if err := readJSON(r, &in); err != nil || !contactField(in.Message, 10000, true, true) {
		writeErr(w, http.StatusBadRequest, "Please enter feedback of at most 10,000 bytes")
		return
	}
	if h.limited(w, r, map[string]int{"feedback:user:" + user(r).String(): 10}) ||
		h.limited(w, r, map[string]int{"feedback:global": 500}) {
		return
	}
	if h.Mail == nil {
		writeErr(w, http.StatusServiceUnavailable, "Feedback delivery unavailable")
		return
	}
	switch h.Mail.(type) {
	case auth.LogMailer, *auth.LogMailer:
		writeErr(w, http.StatusServiceUnavailable, "Feedback delivery unavailable")
		return
	}
	if err := h.Mail.Send(r.Context(), "noreply@lykeion.tech", "Lykeion feedback", strings.TrimSpace(in.Message)); err != nil {
		writeErr(w, http.StatusServiceUnavailable, "Feedback delivery unavailable")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
