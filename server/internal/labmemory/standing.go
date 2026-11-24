package labmemory

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"

	"lykeion/server/internal/store"
)

// shownAlways marks evidence that records what it showed. Evidence without it
// (older answers, lessons, reports) stands only at its exact version.
const shownAlways = "shown"

// titleOnly is the exposure of a fact that a search or a link listed.
func titleOnly(r Ref) []string {
	if r.Kind == "lesson" || r.Kind == "report" {
		return nil
	}
	return []string{shownAlways}
}

// exposure lists what a read fact showed that can be withdrawn while the fact
// itself stays open: shared Team definitions, files, and written text, which may
// be rewritten to remove something. A study's column, state, runs, comments and
// Teams are its progress, so they are changes, not withdrawals.
func exposure(f Fact) []string {
	out := titleOnly(f.Ref)
	if out == nil {
		return nil
	}
	var body map[string]json.RawMessage
	if json.Unmarshal([]byte(f.Text), &body) != nil {
		return nil
	}
	var fields []string
	// Text that was empty showed nothing, so writing it later withdraws nothing.
	text := func(from map[string]json.RawMessage, fields ...string) {
		for _, field := range fields {
			if v := string(from[field]); v != "" && v != "null" && v != `""` {
				out = append(out, fmt.Sprintf("text:%s:%x", field, sha256.Sum256(from[field])))
			}
		}
	}
	switch f.Ref.Kind {
	case "study", "workspace":
		var parts struct {
			Teams []struct {
				ID       uuid.UUID       `json:"id"`
				Manifest json.RawMessage `json:"manifest"`
			} `json:"teams"`
			Attachments []struct {
				ID uuid.UUID `json:"id"`
			} `json:"attachments"`
			Study map[string]json.RawMessage `json:"study"`
		}
		if json.Unmarshal([]byte(f.Text), &parts) != nil {
			return nil
		}
		text(parts.Study, "title", "question", "handoff_note", "end_reason")
		// Every answer rests on the lab's context, so writing it first counts too.
		if v, ok := body["research_context"]; ok {
			out = append(out, fmt.Sprintf("context:%x", sha256.Sum256(v)))
		}
		for _, team := range parts.Teams {
			if team.Manifest != nil {
				out = append(out, "team:"+team.ID.String())
			}
		}
		for _, file := range parts.Attachments {
			out = append(out, "file:"+file.ID.String())
		}
		return out
	case "proposal":
		text(body, "question")
		return out
	case "record":
		fields = []string{"kind", "key", "body", "blob_key"}
	case "comment":
		fields = []string{"body"}
	case "decision":
		fields = []string{"text", "title", "context", "options", "question_set"}
	default:
		return nil
	}
	content := map[string]json.RawMessage{}
	for _, field := range fields {
		content[field] = body[field]
	}
	raw, err := json.Marshal(content)
	if err != nil {
		return nil
	}
	return append(out, fmt.Sprintf("content:%x", sha256.Sum256(raw)))
}

// stands reports whether evidence may still be shown and whether its source
// has changed since. It may be shown while everything it showed is still open.
func stands(ctx context.Context, tx *store.Tx, f Fact, e answerEvidence) (ok, changed bool, err error) {
	if e.Version == "" {
		return false, false, nil
	}
	if pin(f).Version == e.Version {
		return true, false, nil
	}
	if !slices.Contains(e.Shown, shownAlways) {
		return false, false, nil
	}
	now := exposure(f)
	for _, token := range e.Shown {
		if slices.Contains(now, token) {
			continue
		}
		// A Team can leave a study and stay shared with the workspace.
		id, isTeam := strings.CutPrefix(token, "team:")
		team, parseErr := uuid.Parse(id)
		if !isTeam || parseErr != nil {
			return false, false, nil
		}
		if shared, err := tx.Q.LabTeamShared(ctx, team); err != nil || !shared {
			return false, false, err
		}
	}
	return true, true, nil
}
