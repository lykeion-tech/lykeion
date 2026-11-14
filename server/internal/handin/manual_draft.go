package handin

import (
	"encoding/json"
	"github.com/google/uuid"
	"strings"
	"time"
	"unicode/utf8"
)

func parseManualDraft(h HandIn, keys map[string]json.RawMessage) (HandIn, []string) {
	var p []string
	for _, k := range []string{"message", "draft", "statements"} {
		if _, ok := keys[k]; !ok {
			p = append(p, k+": required")
		}
	}
	if strings.TrimSpace(h.Message) == "" {
		p = append(p, "message: a proposal message is required")
	}
	var draftKeys map[string]json.RawMessage
	_ = json.Unmarshal(keys["draft"], &draftKeys)
	for _, k := range []string{"title", "context", "deadline", "card_id"} {
		if _, ok := draftKeys[k]; !ok {
			p = append(p, "draft "+k+": required")
		}
	}
	for _, k := range []string{"title", "context"} {
		if string(draftKeys[k]) == "null" {
			p = append(p, "draft "+k+": must be a string")
		}
	}
	if h.Draft == nil {
		p = append(p, "draft: required")
	} else {
		p = append(p, ValidateManualDraft(*h.Draft, false)...)
	}
	if h.Statements == nil || len(h.Statements) > 40 {
		p = append(p, "statements: give an array of at most 40 statements")
	}
	if len(h.Statements) > 0 {
		raw, _ := json.Marshal(map[string]any{"statements": h.Statements})
		_, more := Parse(Aristotle, raw)
		p = append(p, more...)
	}
	return h, p
}

// Editable drafts may have an unfinished title; a proposed final draft may not.
func ValidateManualDraft(d ManualDraft, unfinished bool) []string {
	var p []string
	if (!unfinished && strings.TrimSpace(d.Title) == "") || utf8.RuneCountInString(d.Title) > 200 || strings.ContainsAny(d.Title, "\r\n") {
		p = append(p, "draft title: one line of 1 to 200 characters")
	}
	if utf8.RuneCountInString(d.Context) > 4000 {
		p = append(p, "draft context: at most 4000 characters")
	}
	if d.Deadline != nil {
		if v, e := time.Parse(time.DateOnly, *d.Deadline); e != nil || v.Format(time.DateOnly) != *d.Deadline {
			p = append(p, "draft deadline: use YYYY-MM-DD or null")
		}
	}
	if d.CardID != nil {
		if id, e := uuid.Parse(*d.CardID); e != nil || id == uuid.Nil {
			p = append(p, "draft card_id: use a study UUID or null")
		}
	}
	return p
}
