package handler

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"lykeion/server/internal/autoresearch"
	"lykeion/server/internal/middleware"
	"lykeion/server/internal/store"
	"lykeion/server/internal/team"
	db "lykeion/server/pkg/db/generated"
)

const maxReport = 64 << 10

// cleanText trims text an agent sent and refuses what cannot be stored or
// shown: nothing, too much, invalid UTF-8 or a NUL byte.
func cleanText(s string, max int, what string) (string, error) {
	s = strings.TrimSpace(s)
	switch {
	case s == "":
		return "", cardInputError(what + " required")
	case len(s) > max:
		return "", cardInputError(fmt.Sprintf("%s must be at most %d bytes", what, max))
	case !utf8.ValidString(s) || strings.ContainsRune(s, 0):
		return "", cardInputError(what + " must be UTF-8 without NUL bytes")
	}
	return s, nil
}

func (h *H) autoresearchReport(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Text string `json:"text"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, 400, "text required")
		return
	}
	text, err := cleanText(in.Text, maxReport, "text")
	if err == nil {
		err = h.roleTx(r, func(tx *store.Tx, role middleware.Role) error {
			return h.Autoresearch.Report(r.Context(), tx, role.AgentRunID, text)
		})
	}
	if errors.Is(err, middleware.ErrRoleUnauthorized) {
		roleError(w, r, err)
		return
	}
	if errors.Is(err, autoresearch.ErrReported) {
		writeErr(w, 409, err.Error())
		return
	}
	if answer(w, r, err) {
		writeJSON(w, 201, map[string]string{"status": "reported"})
	}
}

func (h *H) autoresearchCard(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Title    string `json:"title"`
		Question string `json:"question"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, 400, "question required")
		return
	}
	question, err := cleanText(in.Question, 4000, "question")
	title := strings.TrimSpace(in.Title)
	if err == nil && title == "" {
		title = titleFrom(question)
	}
	if err == nil {
		title, err = cleanText(title, 200, "title")
	}
	var c db.Card
	if err == nil {
		err = h.roleTx(r, func(tx *store.Tx, role middleware.Role) error {
			var err error
			c, err = h.Autoresearch.OpenCardForAgentRun(r.Context(), tx, role.AgentRunID, title, question)
			return err
		})
	}
	if errors.Is(err, middleware.ErrRoleUnauthorized) {
		roleError(w, r, err)
		return
	}
	if errors.Is(err, autoresearch.ErrAlreadyActive) {
		writeJSON(w, 409, map[string]any{"error": fmt.Sprintf("already_active: #%d is open with this title", c.Number),
			"card_id": c.ID, "number": c.Number})
		return
	}
	var noBuiltIn team.NoBuiltInError
	switch {
	case errors.As(err, &noBuiltIn):
		writeErr(w, 422, noBuiltIn.Error())
		return
	case errors.Is(err, autoresearch.ErrStartFailed):
		writeErr(w, 422, autoresearch.ErrStartFailed.Error())
		return
	}
	if answer(w, r, err) {
		writeJSON(w, 201, c)
	}
}
