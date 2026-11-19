package flow

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	db "lykeion/server/pkg/db/generated"
	"strings"
	"testing"
	"time"

	"lykeion/server/internal/handin"
)

func boolptr(v bool) *bool { return &v }

func sampleQuestions() handin.QuestionSetIn {
	return handin.QuestionSetIn{Questions: []handin.QuestionIn{
		{ID: "population", Prompt: "Which population?", Required: boolptr(true), Mode: "single", AllowOther: boolptr(true), Options: []handin.OptionIn{{ID: "children", Label: "Children", Consequence: "Children only"}, {ID: "adults", Label: "Adults", Consequence: "Adults only"}}},
		{ID: "outcomes", Prompt: "Which outcomes?", Required: boolptr(true), Mode: "multiple", AllowOther: boolptr(true), Options: []handin.OptionIn{{ID: "recall", Label: "Recall", Consequence: "Recall"}, {ID: "recognition", Label: "Recognition", Consequence: "Recognition"}}},
		{ID: "details", Prompt: "Any details?", Required: boolptr(false), Mode: "text", AllowOther: boolptr(false)},
	}}
}
func strptr(v string) *string { return &v }
func TestNormalizeQuestionAnswers(t *testing.T) {
	set := sampleQuestions()
	valid := []QuestionAnswer{{QuestionID: "population", OptionIDs: []string{"children"}}, {QuestionID: "outcomes", OptionIDs: []string{"recall", "recognition"}}, {QuestionID: "details", OptionIDs: []string{}, Text: strptr("  details  ")}}
	got, err := normalizeQuestionAnswers(set, valid)
	if err != nil || len(got) != 3 || *got[2].Text != "  details  " {
		t.Fatalf("valid answers: %+v %v", got, err)
	}
	tests := []struct {
		name string
		a    []QuestionAnswer
	}{
		{"missing required", valid[:1]},
		{"duplicate question", append(append([]QuestionAnswer{}, valid...), valid[0])},
		{"unknown question", append(append([]QuestionAnswer{}, valid...), QuestionAnswer{QuestionID: "no"})},
		{"unknown option", []QuestionAnswer{{QuestionID: "population", OptionIDs: []string{"invalid"}}, valid[1]}},
		{"single multiple", []QuestionAnswer{{QuestionID: "population", OptionIDs: []string{"children", "adults"}}, valid[1]}},
		{"duplicate option", []QuestionAnswer{valid[0], {QuestionID: "outcomes", OptionIDs: []string{"recall", "recall"}}}},
		{"custom plus choices", []QuestionAnswer{valid[0], {QuestionID: "outcomes", OptionIDs: []string{"recall"}, Text: strptr("other")}}},
		{"blank custom", []QuestionAnswer{valid[0], {QuestionID: "outcomes", Text: strptr(" ")}}},
		{"too long", []QuestionAnswer{valid[0], {QuestionID: "outcomes", Text: strptr(strings.Repeat("a", 100001))}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := normalizeQuestionAnswers(set, tt.a); err == nil {
				t.Fatal("accepted invalid answers")
			}
		})
	}
	if _, err := normalizeQuestionAnswers(set, []QuestionAnswer{valid[0], {QuestionID: "outcomes", Text: strptr("other")}}); err != nil {
		t.Fatalf("valid custom: %v", err)
	}
	if _, err := normalizeQuestionAnswers(set, []QuestionAnswer{valid[0], {QuestionID: "outcomes", Text: strptr(strings.Repeat("🧪", 100000))}}); err != nil {
		t.Fatalf("100000 unicode code points: %v", err)
	}
	if _, err := normalizeQuestionAnswers(set, []QuestionAnswer{valid[0], {QuestionID: "outcomes", Text: strptr(strings.Repeat("🧪", 100001))}}); err == nil {
		t.Fatal("accepted 100001 code points")
	}
}

func TestQuestionSetAnswerAtomicAndResume(t *testing.T) {
	l := newLab(t)
	agent := l.agent(t, "Scope researcher", "claude", "", "Ask the researcher.")
	c := l.study(t, "hypotheses", false, &agent)
	l.e.Kick(l.ws, c.ID)
	run := l.latestRun(t, c.ID, "agent")
	if _, err := l.env.Owner.Exec(context.Background(), `UPDATE agent_run SET status='completed' WHERE id=$1`, run.ID); err != nil {
		t.Fatal(err)
	}
	set := sampleQuestions()
	raw, err := json.Marshal(set)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.env.Owner.Exec(context.Background(), `UPDATE card SET state='blocked',block_kind='decision' WHERE id=$1`, c.ID); err != nil {
		t.Fatal(err)
	}
	var id uuid.UUID
	if err := l.env.Owner.QueryRow(context.Background(), `INSERT INTO inbox_item (workspace_id,card_id,user_id,kind,text,options,question_set,raised_by,raised_run_id,decision_kind,expires_at) VALUES($1,$2,$3,'agent_decision','Scope','[]',$4::jsonb,'agent',$5,'scope',now()+interval '1 day') RETURNING id`, l.ws, c.ID, l.owner, raw, run.ID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	invalid := []QuestionAnswer{{QuestionID: "population", OptionIDs: []string{"children"}}}
	if err := l.e.AnswerQuestionSet(context.Background(), l.ws, id, l.owner, invalid); err == nil {
		t.Fatal("partial answers accepted")
	}
	if n := l.count(t, `SELECT count(*) FROM inbox_item WHERE id=$1 AND status='open' AND answers IS NULL`, id); n != 1 {
		t.Fatal("invalid request changed decision")
	}
	answers := []QuestionAnswer{{QuestionID: "population", OptionIDs: []string{"children"}}, {QuestionID: "outcomes", Text: strptr("My own outcome")}, {QuestionID: "details", Text: strptr("  keep whitespace  ")}}
	if err := l.e.AnswerQuestionSet(context.Background(), l.ws, id, l.owner, answers); err != nil {
		t.Fatal(err)
	}
	var saved []byte
	var status string
	if err := l.env.Owner.QueryRow(context.Background(), `SELECT answers,status FROM inbox_item WHERE id=$1`, id).Scan(&saved, &status); err != nil {
		t.Fatal(err)
	}
	if status != "decided" || !strings.Contains(string(saved), "My own outcome") || !strings.Contains(string(saved), "  keep whitespace  ") {
		t.Fatalf("answer not saved: %s %s", status, saved)
	}
	runs := l.runs(t, c.ID)
	if len(runs) != 1 || !strings.Contains(runs[0].Prompt, "Which population?") || !strings.Contains(runs[0].Prompt, "Children") || !strings.Contains(runs[0].Prompt, "My own outcome") || !strings.Contains(runs[0].Prompt, "answered by") {
		t.Fatalf("continuation: %+v", runs)
	}
	if err := l.e.AnswerQuestionSet(context.Background(), l.ws, id, l.owner, answers); !errors.Is(err, ErrDecisionClosed) {
		t.Fatalf("second answer: %v", err)
	}
}

func TestDecisionContextPreservesLaterChoice(t *testing.T) {
	set := sampleQuestions()
	set.Questions[0], set.Questions[2] = set.Questions[2], set.Questions[0]
	answers := []QuestionAnswer{
		{QuestionID: "details", Text: strptr(strings.Repeat("x", 4000))},
		{QuestionID: "population", OptionIDs: []string{"children"}},
	}
	context := questionSetAnswerText(set, answers, true)
	if len(context) >= 3000 || !strings.Contains(context, "[truncated]") || !strings.Contains(context, "Children (children)") {
		t.Fatalf("long free text hid the later choice: %q", context)
	}
}

func TestQuestionSetConcurrentAnswersOneWins(t *testing.T) {
	l := newLab(t)
	agent := l.agent(t, "Scope racer", "claude", "", "Ask.")
	c := l.study(t, "hypotheses", false, &agent)
	l.e.Kick(l.ws, c.ID)
	run := l.latestRun(t, c.ID, "agent")
	if _, err := l.env.Owner.Exec(context.Background(), `UPDATE agent_run SET status='completed' WHERE id=$1`, run.ID); err != nil {
		t.Fatal(err)
	}
	set, _ := json.Marshal(sampleQuestions())
	if _, err := l.env.Owner.Exec(context.Background(), `UPDATE card SET state='blocked',block_kind='decision' WHERE id=$1`, c.ID); err != nil {
		t.Fatal(err)
	}
	var id uuid.UUID
	if err := l.env.Owner.QueryRow(context.Background(), `INSERT INTO inbox_item(workspace_id,card_id,user_id,kind,text,options,question_set,raised_by,raised_run_id,decision_kind,expires_at) VALUES($1,$2,$3,'agent_decision','Scope','[]',$4::jsonb,'agent',$5,'scope',now()+interval '1 day') RETURNING id`, l.ws, c.ID, l.owner, set, run.ID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	errs := make(chan error, 2)
	for _, opt := range []string{"children", "adults"} {
		go func(option string) {
			errs <- l.e.AnswerQuestionSet(context.Background(), l.ws, id, l.owner, []QuestionAnswer{{QuestionID: "population", OptionIDs: []string{option}}, {QuestionID: "outcomes", OptionIDs: []string{"recall"}}})
		}(opt)
	}
	a, b := <-errs, <-errs
	if !((a == nil && errors.Is(b, ErrDecisionClosed)) || (b == nil && errors.Is(a, ErrDecisionClosed))) {
		t.Fatalf("concurrent results %v / %v", a, b)
	}
	if n := l.count(t, `SELECT count(*) FROM inbox_item WHERE id=$1 AND status='decided' AND answers IS NOT NULL`, id); n != 1 {
		t.Fatalf("decision count %d", n)
	}
	if runs := l.runs(t, c.ID); len(runs) != 1 {
		t.Fatalf("queued %d continuations", len(runs))
	}
}

func TestQuestionSetAnswerRacesExpiry(t *testing.T) {
	l := newLab(t)
	agent := l.agent(t, "Expiry racer", "claude", "", "Ask.")
	c := l.study(t, "hypotheses", false, &agent)
	l.e.Kick(l.ws, c.ID)
	run := l.latestRun(t, c.ID, "agent")
	if _, err := l.env.Owner.Exec(context.Background(), `UPDATE agent_run SET status='completed' WHERE id=$1`, run.ID); err != nil {
		t.Fatal(err)
	}
	set, _ := json.Marshal(sampleQuestions())
	if _, err := l.env.Owner.Exec(context.Background(), `UPDATE card SET state='blocked',block_kind='decision' WHERE id=$1`, c.ID); err != nil {
		t.Fatal(err)
	}
	var id uuid.UUID
	if err := l.env.Owner.QueryRow(context.Background(), `INSERT INTO inbox_item(workspace_id,card_id,user_id,kind,text,options,question_set,raised_by,raised_run_id,decision_kind,expires_at) VALUES($1,$2,$3,'agent_decision','Scope','[]',$4::jsonb,'agent',$5,'scope',now()-interval '1 minute') RETURNING id`, l.ws, c.ID, l.owner, set, run.ID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	errs := make(chan error, 2)
	go func() {
		errs <- l.e.AnswerQuestionSet(context.Background(), l.ws, id, l.owner, []QuestionAnswer{{QuestionID: "population", OptionIDs: []string{"children"}}, {QuestionID: "outcomes", OptionIDs: []string{"recall"}}})
	}()
	go func() {
		errs <- l.e.expire(context.Background(), db.DueDecisionsRow{ID: id, CardID: c.ID, WorkspaceID: l.ws}, time.Now())
	}()
	a, b := <-errs, <-errs
	for _, err := range []error{a, b} {
		if err != nil && !errors.Is(err, ErrDecisionClosed) {
			t.Fatalf("race error %v", err)
		}
	}
	if n := l.count(t, `SELECT count(*) FROM inbox_item WHERE id=$1 AND status IN ('decided','expired')`, id); n != 1 {
		t.Fatalf("terminal decisions %d", n)
	}
	if runs := l.runs(t, c.ID); len(runs) != 1 {
		t.Fatalf("queued %d continuations", len(runs))
	}
}
