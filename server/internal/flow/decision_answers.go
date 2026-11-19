package flow

import (
	"context"
	"encoding/json"
	"errors"

	"fmt"
	"github.com/google/uuid"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
	"slices"
	"strings"
	"unicode/utf8"

	"lykeion/server/internal/handin"
)

// QuestionAnswer is one answer in a question set. Text is either a text
// question's answer or a custom alternative to all proposed choices.
type QuestionAnswer struct {
	QuestionID string   `json:"question_id"`
	OptionIDs  []string `json:"option_ids"`
	Text       *string  `json:"text"`
}

func normalizeQuestionAnswers(set handin.QuestionSetIn, answers []QuestionAnswer) ([]QuestionAnswer, error) {
	byID := make(map[string]QuestionAnswer, len(answers))
	for _, a := range answers {
		if _, ok := byID[a.QuestionID]; ok {
			return nil, OptionError("duplicate question answer: " + a.QuestionID)
		}
		byID[a.QuestionID] = a
	}
	out := make([]QuestionAnswer, 0, len(answers))
	for _, q := range set.Questions {
		a, ok := byID[q.ID]
		if !ok {
			if q.Required != nil && *q.Required {
				return nil, OptionError("answer required: " + q.ID)
			}
			continue
		}
		delete(byID, q.ID)
		if a.Text != nil && (!utf8.ValidString(*a.Text) || strings.ContainsRune(*a.Text, 0) || utf8.RuneCountInString(*a.Text) > 100000) {
			return nil, OptionError("answer text exceeds 100000 characters")
		}
		if q.Mode == "text" {
			if len(a.OptionIDs) > 0 {
				return nil, OptionError("text question cannot select options: " + q.ID)
			}
			if a.Text == nil || strings.TrimSpace(*a.Text) == "" {
				if q.Required != nil && *q.Required {
					return nil, OptionError("answer required: " + q.ID)
				}
				continue
			}
		} else {
			if a.Text != nil {
				if q.AllowOther == nil || !*q.AllowOther || strings.TrimSpace(*a.Text) == "" || len(a.OptionIDs) > 0 {
					return nil, OptionError("invalid custom answer: " + q.ID)
				}
			} else {
				if len(a.OptionIDs) == 0 {
					if q.Required != nil && *q.Required {
						return nil, OptionError("answer required: " + q.ID)
					}
					continue
				}
				if q.Mode == "single" && len(a.OptionIDs) != 1 {
					return nil, OptionError("select one option: " + q.ID)
				}
				seen := map[string]bool{}
				for _, id := range a.OptionIDs {
					if seen[id] || !slices.ContainsFunc(q.Options, func(o handin.OptionIn) bool { return o.ID == id }) {
						return nil, OptionError("invalid option: " + id)
					}
					seen[id] = true
				}
			}
		}
		if a.OptionIDs == nil {
			a.OptionIDs = []string{}
		}
		out = append(out, a)
	}
	if len(byID) > 0 {
		return nil, OptionError("unknown question answer")
	}
	return out, nil
}

// AnswerQuestionSet validates and records every answer under the same study
// and decision locks used for single-option decisions and expiry.
func (e *Engine) AnswerQuestionSet(ctx context.Context, ws, item, user uuid.UUID, answers []QuestionAnswer) error {
	return e.Store.InWorkspace(ctx, ws, user, func(tx *store.Tx) error {
		if _, err := tx.Q.LockWorkspaceForMutation(ctx, ws); err != nil {
			return err
		}
		member, err := tx.Q.LockMember(ctx, db.LockMemberParams{WorkspaceID: ws, UserID: user})
		if err != nil {
			return err
		}
		row, err := tx.Q.GetDecision(ctx, db.GetDecisionParams{ID: item, WorkspaceID: ws, Viewer: user, IsAdmin: member.Role == "owner" || member.Role == "admin"})
		if err != nil {
			return err
		}
		if !row.MayAnswer {
			return ErrNotAnswerer
		}
		if row.InboxItem.CardID == nil {
			return ErrNotWaiting
		}
		card, err := lockCard(ctx, tx, *row.InboxItem.CardID)
		if err != nil {
			return err
		}
		it, err := tx.Q.LockDecision(ctx, db.LockDecisionParams{ID: item, WorkspaceID: ws})
		if err != nil {
			return err
		}
		if it.Status != "open" || it.ResolvedAt != nil || it.ArchivedAt != nil {
			return ErrDecisionClosed
		}
		if it.Kind != "agent_decision" || len(it.QuestionSet) == 0 {
			return OptionError("this decision has no question set")
		}
		if card.ID == uuid.Nil || card.Ending != "" || !waitsOnQuestion(card) {
			return ErrNotWaiting
		}
		var set handin.QuestionSetIn
		if err := json.Unmarshal(it.QuestionSet, &set); err != nil {
			return err
		}
		normalized, err := normalizeQuestionAnswers(set, answers)
		if err != nil {
			return err
		}
		raw, err := json.Marshal(normalized)
		if err != nil {
			return err
		}
		decided, err := tx.Q.AnswerQuestionSet(ctx, db.AnswerQuestionSetParams{ID: item, WorkspaceID: ws, DecidedBy: user, Answers: raw})
		if err != nil {
			return err
		}
		if err := tx.Emit(ctx, "inbox.resolved", &card.ID, store.User(user), map[string]string{"inbox_item_id": item.String()}); err != nil {
			return err
		}
		err = e.resumeAfterDecision(ctx, tx, card, decided)
		if errors.Is(err, ErrPackUnavailable) || errors.Is(err, errNoAgent) {
			return e.askOwner(ctx, tx, card, "its question was answered and its lead could not be woken: "+err.Error())
		}
		if err != nil {
			return err
		}
		return emitSteered(ctx, tx, card.ID, store.User(user))
	})
}

func questionSetAnswerText(set handin.QuestionSetIn, answers []QuestionAnswer, withContext bool) string {
	byID := make(map[string]QuestionAnswer, len(answers))
	for _, a := range answers {
		byID[a.QuestionID] = a
	}
	var b strings.Builder
	for _, q := range set.Questions {
		fmt.Fprintf(&b, "\n- %s [%s]: ", q.Prompt, q.ID)
		a, ok := byID[q.ID]
		if !ok {
			b.WriteString("Not answered")
			continue
		}
		if a.Text != nil {
			if q.Mode != "text" {
				b.WriteString("Custom: ")
			}
			text := *a.Text
			if withContext {
				if excerpt, truncated := Excerpt(text, 512); truncated {
					text = excerpt + " [truncated]"
				}
			}
			b.WriteString(text)
			continue
		}
		labels := make([]string, 0, len(a.OptionIDs))
		for _, id := range a.OptionIDs {
			for _, o := range q.Options {
				if o.ID == id {
					label := o.Label + " (" + o.ID + ")"
					if withContext && o.Consequence != "" {
						label += " — " + o.Consequence
					}
					labels = append(labels, label)
					break
				}
			}
		}
		b.WriteString(strings.Join(labels, ", "))
	}
	return b.String()
}
