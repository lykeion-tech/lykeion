package handin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

// What the Autoresearch operations answer that a hand-in treats specially.
var (
	// ErrReported: the run already posted its report.
	ErrReported = errors.New("the run already reported")
	// ErrDuplicate: an active study already has the title.
	ErrDuplicate = errors.New("an active study already has this title")
)

// CannotStart is why a study could not be opened.
type CannotStart struct{ Reason string }

func (e CannotStart) Error() string { return e.Reason }

const (
	maxReport   = 64 << 10
	maxTitle    = 200
	maxQuestion = 4000
)

// text trims s and says what is wrong with it as stored text, or "".
func text(s string, max int, what string) (string, string) {
	s = strings.TrimSpace(s)
	switch {
	case s == "":
		return s, what + ": cannot be empty"
	case len(s) > max:
		return s, fmt.Sprintf("%s: at most %d bytes", what, max)
	case !utf8.ValidString(s) || strings.ContainsRune(s, 0):
		return s, what + ": must be UTF-8 without NUL bytes"
	}
	return s, ""
}

// autoresearch applies an Autoresearch run's hand-in: its report, then a
// study for each new study. Every part is checked before anything is written
// and all of it is written in the caller's transaction, so a retry never
// repeats a side effect. A run that already reported has applied its
// hand-in, and a repeat is accepted and writes nothing.
func (s *HandIns) autoresearch(ctx context.Context, tx *store.Tx, run uuid.UUID, raw []byte) ([]string, error) {
	h, probs := Parse(Autoresearch, raw)
	if len(probs) > 0 {
		return probs, nil
	}
	body, p := h.Answer, ""
	switch {
	case h.Status == "could_not_do":
		body = "Could not run: " + deref(h.Reason)
	case h.Report != nil && strings.TrimSpace(*h.Report) != "":
		body = *h.Report
	}
	body, p = text(body, maxReport, "report")
	if p != "" {
		probs = append(probs, p)
	}
	type study struct{ title, question string }
	var studies []study
	if h.Status == "done" {
		for i, n := range h.NewStudies {
			t, tp := text(n.Title, maxTitle, fmt.Sprintf("new_studies %d: title", i+1))
			q, qp := text(n.Question, maxQuestion, fmt.Sprintf("new_studies %d: question", i+1))
			probs = append(probs, slicesNonEmpty(tp, qp)...)
			studies = append(studies, study{t, q})
		}
	}
	if len(probs) > 0 {
		return probs, nil
	}
	if r, err := tx.Q.RunByAgentRun(ctx, db.RunByAgentRunParams{WorkspaceID: tx.WS, AgentRunID: run}); err != nil {
		return nil, err
	} else if r.Report != "" {
		return nil, nil
	}
	// A study that cannot start is not the agent's to fix, so it is skipped
	// and named in the report.
	for _, st := range studies {
		// A study that fails to start leaves nothing behind.
		err := tx.Savepoint(ctx, func(sp *store.Tx) error {
			_, err := s.Autoresearch.OpenCardForAgentRun(ctx, sp, run, st.title, st.question)
			return err
		})
		var cs CannotStart
		switch {
		case err == nil, errors.Is(err, ErrDuplicate):
		case errors.As(err, &cs):
			body += fmt.Sprintf("\n\nCould not open the study %q: %v", st.title, err)
		default:
			return nil, err
		}
	}
	if err := s.Autoresearch.Report(ctx, tx, run, body); err != nil && !errors.Is(err, ErrReported) {
		return nil, err
	}
	return nil, nil
}

func slicesNonEmpty(ss ...string) (out []string) {
	for _, s := range ss {
		if s != "" {
			out = append(out, s)
		}
	}
	return
}
