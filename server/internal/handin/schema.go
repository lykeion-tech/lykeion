package handin

import "encoding/json"

// The schemas go to Codex --output-schema and Claude --json-schema, so they
// stay inside OpenAI strict mode: every object lists all its properties in
// required and sets additionalProperties false, and an optional value is a
// null-able type. Length and count limits are not in the schema (strict mode
// rejects some of those keywords); Parse checks them.

type obj = map[string]any

func str() obj             { return obj{"type": "string"} }
func nullStr() obj         { return obj{"type": []string{"string", "null"}} }
func nullNum() obj         { return obj{"type": []string{"number", "null"}} }
func enum(v ...string) obj { return obj{"type": "string", "enum": v} }
func list(item any) obj    { return obj{"type": "array", "items": item} }
func nullObj(o obj) obj    { return obj{"anyOf": []any{o, obj{"type": "null"}}} }

// desc is o described for the agent filling it in.
func desc(o obj, d string) obj { o["description"] = d; return o }

// object lists props in the given order and requires all of them.
func object(kv ...any) obj {
	props, req := obj{}, []string{}
	for i := 0; i < len(kv); i += 2 {
		props[kv[i].(string)] = kv[i+1]
		req = append(req, kv[i].(string))
	}
	return obj{"type": "object", "properties": props, "required": req, "additionalProperties": false}
}

func reply() obj {
	return object("item", str(), "action", enum("corrected", "withdrawn", "declined"), "reason", str())
}

// decision is the question an ask hand-in puts to the researcher.
func decision() obj {
	return desc(nullObj(object(
		"kind", enum("scope", "change_question", "ethics", "cost", "access"),
		"title", str(), "body", str(),
		"options", obj{"anyOf": []any{list(object("id", str(), "label", str(), "consequence", str())), obj{"type": "null"}}},
		"question_set", nullObj(object("questions", list(object(
			"id", str(), "prompt", str(), "help_text", nullStr(), "required", obj{"type": "boolean"},
			"mode", enum("single", "multiple", "text"), "options", list(object("id", str(), "label", str(), "consequence", str())),
			"allow_other", obj{"type": "boolean"})))),
		"expires_at", desc(nullStr(), "When the question expires, as an RFC 3339 time at most 30 days ahead; null for 7 days."),
		"from_member", desc(nullStr(), "The Team member whose question this is; null when it is your own."))),
		"The question for the researcher when status is ask; null otherwise.")
}

func build(extra ...any) string {
	kv := append([]any{"status", enum("done", "could_not_do", "ask"), "answer", str(), "reason", nullStr(), "decision", decision()}, extra...)
	return strictSchema(kv...)
}

func buildStudy(extra ...any) string {
	return build(append([]any{"blocked_by_environment", desc(nullStr(), "If a local Python, R or Julia environment prevented the work, use python, r or julia; otherwise null."), "blocked_by_member_run", desc(nullStr(), "For a delegated member's environment failure, the exact member ticket UUID; otherwise null.")}, extra...)...)
}

func strictSchema(kv ...any) string {
	b, err := json.Marshal(object(kv...))
	if err != nil {
		panic(err)
	}
	return string(b)
}

var schemas = map[Kind]string{
	ManualDecisionDraft: strictSchema("message", str(), "draft", object("title", str(), "context", str(), "deadline", nullStr(), "card_id", nullStr()), "statements", list(object("kind", enum("fact", "lesson", "gap"), "text", str(), "cites", list(str())))),
	Aristotle:           strictSchema("statements", list(object("kind", enum("fact", "lesson", "gap"), "text", str(), "cites", list(str())))),
	StudySuggestion:     strictSchema("new_study", desc(nullObj(object("description", str(), "stage", enum("grounding", "hypotheses", "experimentation", "validation"), "assignee_name", nullStr())), "A proposed new research question. Choose a known stage and an eligible worker by exact name if known. Null when no new study is requested or warranted."), "statements", list(object("kind", enum("fact", "lesson", "gap"), "text", str(), "cites", list(str())))),
	Grounding: buildStudy(
		"papers", list(object("id", str(), "why", str())),
		"datasets", list(object("accession", str(), "why", str())),
		"no_qualifying", nullObj(object("reason", str())),
		"replies", list(reply())),
	Hypotheses: buildStudy(
		"hypotheses", list(object(
			"text", str(),
			"measure", desc(nullObj(object("id", str(), "name", str(), "unit", str())), "Declare the predicted measure with a short lowercase id, name and unit, or null if no measure can be declared."),
			"prediction_shape", enum("direction", "outcomes", "range"),
			"direction", obj{"type": []string{"string", "null"}, "enum": []any{"increase", "decrease", "no_change", nil}},
			"outcomes", list(str()),
			"chosen", nullStr(), "low", nullNum(), "high", nullNum(), "uncertainty", nullStr(),
			"control", nullStr(), "no_control", nullStr(),
			"cites", list(str()))),
		"replies", list(reply())),
	Experimentation: buildStudy(
		"outputs", list(str()),
		"values", list(object("name", str(),
			"value", desc(str(), "The exact number or observed outcome label printed by the script or saved in its output."),
			"file", str(), "run", desc(nullStr(), "Exact run:KEY of the script that produced this value in a saved output, or null when the file is already a run:KEY or no script produced it."),
			"field", desc(nullStr(), "JSON Pointer to the number in the named saved JSON file, or null for an unbound value."),
			"measure", desc(nullStr(), "ID from an accepted hypothesis for this measurement, or null for an unbound value."))),
		"replies", list(reply())),
	Auditor: build(
		"concerns", list(object("item", str(), "concern", str())),
		"replies", list(reply())),
	Validation: buildStudy(
		"assessments", list(object(
			"hypothesis", str(),
			"measurement", desc(str(), "The name of the recorded value that measures the hypothesis: the effect itself, such as a difference, or the treated group's raw value."),
			"control", desc(nullStr(), "The name of the recorded value holding the control group's raw value, when measurement is the treated group's raw value; null when measurement is already the effect."),
			"test", desc(nullObj(object(
				"kind", enum("t", "F", "chi2", "r", "z"),
				"statistic", obj{"type": "number"}, "df1", nullNum(),
				"df2", desc(nullNum(), "The second degrees of freedom, for an F test; null when the test has none."),
				"p", obj{"type": "number"}, "p_bound", enum("=", "<", ">"))),
				"The test exactly as a recorded script run printed it: Lykeion looks for its statistic and p in the rerun's output. Null when there is none."),
			"observed", desc(nullStr(), "For a hypothesis that names outcomes, the name of the recorded value whose text is the outcome seen, one of the named outcomes; null otherwise."))),
		"verdicts", list(object("claim", str(), "verdict", enum("supported", "refuted", "not_enough_info"), "source", str(), "quote", str())),
		"flagged_errors", list(object("source", str(), "quote", str(), "problem", str())),
		"replies", list(reply())),
	Objector: build(
		"objections", list(object(
			"target", str(),
			"type", enum("wrong_test", "leakage", "unit_mismatch", "missing_control", "overreach"),
			"quote", nullStr(), "problem", str()))),
	Report: reportSchema(),
	Autoresearch: build(
		"report", nullStr(),
		"new_studies", list(object("title", str(), "question", str()))),
}

// Schema is the JSON schema of kind k's hand-in, or "" for an unknown kind.
func Schema(k Kind) string { return schemas[k] }
