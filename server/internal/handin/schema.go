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

func build(extra ...any) string {
	kv := append([]any{"status", enum("done", "could_not_do"), "answer", str(), "reason", nullStr()}, extra...)
	b, err := json.Marshal(object(kv...))
	if err != nil {
		panic(err)
	}
	return string(b)
}

var schemas = map[Kind]string{
	Grounding: build(
		"papers", list(object("id", str(), "why", str())),
		"datasets", list(object("accession", str(), "why", str())),
		"no_qualifying", nullObj(object("reason", str())),
		"replies", list(reply())),
	Hypotheses: build(
		"hypotheses", list(object(
			"text", str(),
			"prediction_shape", enum("direction", "outcomes", "range"),
			"direction", obj{"type": []string{"string", "null"}, "enum": []any{"increase", "decrease", "no_change", nil}},
			"outcomes", list(str()),
			"chosen", nullStr(), "low", nullNum(), "high", nullNum(), "uncertainty", nullStr(),
			"control", nullStr(), "no_control", nullStr(),
			"cites", list(str()))),
		"replies", list(reply())),
	Experimentation: build(
		"outputs", list(str()),
		"values", list(object("name", str(), "value", str(), "file", str())),
		"replies", list(reply())),
	Auditor: build(
		"concerns", list(object("item", str(), "concern", str())),
		"replies", list(reply())),
	Validation: build(
		"assessments", list(object(
			"hypothesis", str(), "measurement", str(), "control", nullStr(),
			"test", nullObj(object(
				"kind", enum("t", "F", "chi2", "r", "z"),
				"statistic", obj{"type": "number"}, "df1", nullNum(), "df2", nullNum(),
				"p", obj{"type": "number"}, "p_bound", enum("=", "<", ">"))),
			"observed", nullStr())),
		"verdicts", list(object("claim", str(), "verdict", enum("supported", "refuted", "not_enough_info"), "source", str(), "quote", str())),
		"flagged_errors", list(object("source", str(), "quote", str(), "problem", str())),
		"replies", list(reply())),
	Objector: build(
		"objections", list(object(
			"target", str(),
			"type", enum("wrong_test", "leakage", "unit_mismatch", "missing_control", "overreach"),
			"quote", nullStr(), "problem", str()))),
	Autoresearch: build(
		"report", nullStr(),
		"new_studies", list(object("title", str(), "question", str()))),
}

// Schema is the JSON schema of kind k's hand-in, or "" for an unknown kind.
func Schema(k Kind) string { return schemas[k] }
