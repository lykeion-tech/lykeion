package handin

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

// ask is a valid ask hand-in with the decision changed by edit.
func ask(t *testing.T, edit func(d map[string]any)) []byte {
	t.Helper()
	d := map[string]any{
		"kind": "scope", "title": "Which population?", "body": "The question reads two ways.",
		"options": []any{
			map[string]any{"id": "adults", "label": "Adults", "consequence": "The study looks at adults only."},
			map[string]any{"id": "children", "label": "Children", "consequence": "The study looks at children only."},
		},
		"expires_at": nil, "from_member": nil,
	}
	if edit != nil {
		edit(d)
	}
	b, err := json.Marshal(map[string]any{"status": "ask", "answer": "I need the researcher.", "reason": nil, "decision": d,
		"papers": []any{}, "datasets": []any{}, "no_qualifying": nil, "replies": []any{}})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestAskParses(t *testing.T) {
	in := time.Now().Add(10 * 24 * time.Hour).UTC().Format(time.RFC3339)
	h, probs := Parse(Grounding, ask(t, func(d map[string]any) { d["expires_at"], d["from_member"] = in, "scout" }))
	if len(probs) != 0 {
		t.Fatalf("problems: %q", probs)
	}
	if h.Decision == nil || h.Decision.Kind != "scope" || len(h.Decision.Options) != 2 || h.Decision.ExpiresAt == nil || *h.Decision.FromMember != "scout" {
		t.Fatalf("decision: %+v", h.Decision)
	}
}

func TestAskProblems(t *testing.T) {
	cases := []struct {
		name string
		raw  []byte
		want string
	}{
		{"ask without decision", []byte(`{"status":"ask","answer":"x","reason":null,"decision":null}`), "decision: an ask needs a decision"},
		{"decision on done", []byte(strings.Replace(string(ask(t, nil)), `"status":"ask"`, `"status":"done"`, 1)), "decision: only an ask carries a decision; leave it null"},
		{"method kind", ask(t, func(d map[string]any) { d["kind"] = "method" }), "decision: kind must be scope, change_question, ethics, cost or access"},
		{"one option", ask(t, func(d map[string]any) { d["options"] = d["options"].([]any)[:1] }), "decision: give 2 to 5 options"},
		{"no consequence", ask(t, func(d map[string]any) { d["options"].([]any)[1].(map[string]any)["consequence"] = "" }),
			"decision options 2: consequence must say in 1 to 200 characters what the option leads to"},
		{"same id", ask(t, func(d map[string]any) { d["options"].([]any)[1].(map[string]any)["id"] = "adults" }), "decision options 2: another option already has the id adults"},
		{"bad id", ask(t, func(d map[string]any) { d["options"].([]any)[0].(map[string]any)["id"] = "Adults!" }),
			"decision options 1: id must be 1 to 40 lowercase letters, digits or underscores"},
		{"31 days", ask(t, func(d map[string]any) {
			d["expires_at"] = time.Now().Add(31 * 24 * time.Hour).UTC().Format(time.RFC3339)
		}),
			"decision: expires_at must be at least 1 hour and at most 30 days ahead"},
		{"past", ask(t, func(d map[string]any) { d["expires_at"] = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339) }),
			"decision: expires_at must be at least 1 hour and at most 30 days ahead"},
		{"in ten minutes", ask(t, func(d map[string]any) { d["expires_at"] = time.Now().Add(10 * time.Minute).UTC().Format(time.RFC3339) }),
			"decision: expires_at must be at least 1 hour and at most 30 days ahead"},
		{"no title", ask(t, func(d map[string]any) { d["title"] = "" }), "decision: title must be one line of 1 to 200 characters"},
		{"blank member", ask(t, func(d map[string]any) { d["from_member"] = " " }), "decision: from_member must name a Team member, or be null"},
		{"no body", ask(t, func(d map[string]any) { d["body"] = "" }), "decision: body must be 1 to 4000 characters"},
	}
	for _, c := range cases {
		_, probs := Parse(Grounding, c.raw)
		if !slices.Contains(probs, c.want) {
			t.Errorf("%s: problems %q, want %q", c.name, probs, c.want)
		}
	}
}

func TestEveryKindAcceptsADecision(t *testing.T) {
	for _, k := range []Kind{Grounding, Hypotheses, Experimentation, Auditor, Autoresearch, Validation, Objector} {
		if !strings.Contains(Schema(k), `"decision"`) {
			t.Errorf("%s: the schema has no decision", k)
		}
	}
}

// Only a column's lead asks the researcher: the auditor, the objector and
// Autoresearch hand in done or could_not_do.
func TestOnlyLeadsAsk(t *testing.T) {
	var full map[string]any
	if err := json.Unmarshal(ask(t, nil), &full); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]any{"status": "ask", "answer": "x", "reason": nil, "decision": full["decision"]})
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []Kind{Auditor, Objector, Autoresearch} {
		if _, probs := Parse(k, raw); !slices.Contains(probs, "status: must be done or could_not_do") {
			t.Errorf("%s: problems %q", k, probs)
		}
	}
}

func TestAskQuestionSetShapes(t *testing.T) {
	options := []any{map[string]any{"id": "adults", "label": "Adults", "consequence": "Study adults."}, map[string]any{"id": "children", "label": "Children", "consequence": "Study children."}}
	question := func(id, mode string, options []any, other bool) map[string]any {
		return map[string]any{"id": id, "prompt": "Which population?", "help_text": nil, "required": true, "mode": mode, "options": options, "allow_other": other}
	}
	questions := []any{question("one", "single", options, true), question("many", "multiple", options, true), question("details", "text", []any{}, false)}
	raw := ask(t, func(d map[string]any) { d["options"] = nil; d["question_set"] = map[string]any{"questions": questions} })
	if _, problems := Parse(Grounding, raw); len(problems) != 0 {
		t.Fatalf("valid mixed question set: %v", problems)
	}
	for _, tc := range []struct {
		name string
		edit func(map[string]any)
	}{
		{"duplicate id", func(d map[string]any) {
			q := question("same", "text", []any{}, false)
			d["question_set"] = map[string]any{"questions": []any{q, q}}
		}},
		{"too few choices", func(d map[string]any) {
			d["question_set"] = map[string]any{"questions": []any{question("q", "single", options[:1], false)}}
		}},
		{"text with options", func(d map[string]any) {
			d["question_set"] = map[string]any{"questions": []any{question("q", "text", options, false)}}
		}},
		{"text with custom", func(d map[string]any) {
			d["question_set"] = map[string]any{"questions": []any{question("q", "text", []any{}, true)}}
		}},
		{"unknown mode", func(d map[string]any) {
			d["question_set"] = map[string]any{"questions": []any{question("q", "unknown", options, false)}}
		}},
		{"both representations", func(d map[string]any) { d["options"] = options }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := ask(t, func(d map[string]any) {
				d["options"] = nil
				d["question_set"] = map[string]any{"questions": questions}
				tc.edit(d)
			})
			if _, problems := Parse(Grounding, raw); len(problems) == 0 {
				t.Fatal("invalid question set accepted")
			}
		})
	}
}

func TestQuestionSetRequiresExplicitNullableFields(t *testing.T) {
	q := map[string]any{"id": "q", "prompt": "Which population?", "required": false, "mode": "text", "options": []any{}, "allow_other": false}
	raw := ask(t, func(d map[string]any) { d["options"] = nil; d["question_set"] = map[string]any{"questions": []any{q}} })
	if _, problems := Parse(Grounding, raw); len(problems) == 0 {
		t.Fatal("question with omitted help_text accepted")
	}
}

func TestQuestionSetCountLimit(t *testing.T) {
	question := func(i int) map[string]any {
		return map[string]any{"id": fmt.Sprintf("q_%d", i), "prompt": "Which population?", "help_text": nil, "required": false, "mode": "text", "options": []any{}, "allow_other": false}
	}
	for _, count := range []int{0, 1, 64, 65} {
		questions := make([]any, count)
		for i := range questions {
			questions[i] = question(i)
		}
		raw := ask(t, func(d map[string]any) { d["options"] = nil; d["question_set"] = map[string]any{"questions": questions} })
		_, problems := Parse(Grounding, raw)
		want := count >= 1 && count <= 64
		if (len(problems) == 0) != want {
			t.Errorf("count %d: %v", count, problems)
		}
	}
}
