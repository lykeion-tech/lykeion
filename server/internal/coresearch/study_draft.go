package coresearch

import (
	"encoding/json"
	"strings"

	"lykeion/server/internal/board"
)

const studyDraftFence = "```study-draft\n"
const priorTurnsForOffer = 1

type StudyDraft struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Stage       string `json:"stage"`
	Prose       string `json:"prose"`
}

// ParseStudyDraft accepts only the trailing block in a settled assistant reply.
func ParseStudyDraft(text string, priorTurns int) *StudyDraft {
	text = strings.TrimSpace(text)
	start := strings.LastIndex(text, studyDraftFence)
	if start < 0 || (start > 0 && text[start-1] != '\n') {
		return nil
	}
	raw, ended := strings.CutSuffix(text[start+len(studyDraftFence):], "\n```")
	if !ended || len(raw) > 8192 {
		return nil
	}
	var draft struct {
		StudyDraft
		Trigger string `json:"trigger"`
	}
	if json.Unmarshal([]byte(raw), &draft) != nil ||
		(draft.Trigger != "request" && !(draft.Trigger == "offer" && priorTurns >= priorTurnsForOffer)) {
		return nil
	}
	draft.Title = strings.TrimSpace(draft.Title)
	draft.Description = strings.TrimSpace(draft.Description)
	if draft.Title == "" || len([]rune(draft.Title)) > 120 || draft.Description == "" ||
		strings.ContainsRune(draft.Title, 0) || strings.ContainsRune(draft.Description, 0) ||
		board.StageIndex(draft.Stage) < 0 {
		return nil
	}
	draft.Prose = strings.TrimSpace(text[:start])
	return &draft.StudyDraft
}
