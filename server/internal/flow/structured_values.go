package flow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"slices"
	"strconv"
	"strings"

	"lykeion/server/internal/board"
	"lykeion/server/internal/records"
	"lykeion/server/internal/review"
	db "lykeion/server/pkg/db/generated"
)

func hasBinding(v valueRecord) bool { return v.Field != nil || v.Measure != nil }

func experimentationValues(work, executions []db.Record, runs map[string]Run, files map[string]string, measures map[string]review.Measure) []review.ValueCheck {
	legacy := make([]db.Record, 0, len(work))
	var bound []db.Record
	unbound := map[string]bool{}
	for _, r := range work {
		if r.Kind == "value" {
			if hasBinding(valueOf(r)) {
				bound = append(bound, r)
				continue
			}
			unbound[r.Key] = true
		}
		legacy = append(legacy, r)
	}
	values := valueChecks(MatchValuesIn(legacy, runs, files), work, runs, files)
	for i := range values {
		values[i].Unbound = unbound[values[i].Record]
	}
	docs := map[string]decodedOutput{}
	for _, r := range bound {
		values = append(values, checkBoundValue(r, executions, work, files, measures, docs))
	}
	slices.SortStableFunc(values, func(a, b review.ValueCheck) int {
		return strings.Compare(a.Record, b.Record)
	})
	return values
}

func checkBoundValue(r db.Record, executions, work []db.Record, files map[string]string, measures map[string]review.Measure, docs map[string]decodedOutput) review.ValueCheck {
	v := valueOf(r)
	result := review.ValueCheck{Record: r.Key, Value: v.Value, Sentence: v.Name}
	add := func(id, detail string, pass bool) {
		status := review.Fail
		if pass {
			status = review.Pass
		}
		result.Binding = append(result.Binding, review.Check{ID: id, By: "server", Detail: detail, Status: status})
	}
	if v.Measure != nil {
		if m, ok := measures[*v.Measure]; ok {
			result.Measure = &m
		}
	}
	measureChecked := false
	deferMeasure := func() {
		if measureChecked {
			return
		}
		switch {
		case v.Measure == nil:
			add("measure", "Declare a hypothesis measure ID.", false)
		case result.Measure == nil:
			add("measure", "No unambiguous accepted hypothesis declares measure "+*v.Measure+".", false)
		default:
			add("measure", "Matches accepted hypothesis measure "+result.Measure.ID+".", true)
		}
	}
	if v.Field == nil {
		add("bound", "Declare the JSON field containing this value.", false)
		deferMeasure()
		return result
	}
	key, reason := boundOutput(r, v, executions, work, files)
	if reason != "" {
		add("bound", reason, false)
		deferMeasure()
		return result
	}
	doc, seen := docs[key]
	if !seen {
		doc.data, doc.err = decodeOutput(files[key])
		docs[key] = doc
	}
	if doc.err != nil {
		add("bound", "The saved file is not unambiguous JSON.", false)
		deferMeasure()
		return result
	}
	field, ok := numberAt(doc.data, *v.Field)
	if !ok {
		add("bound", "The field is missing or is not a number.", false)
		deferMeasure()
		return result
	}
	reported, a := parseNumber(strings.TrimSpace(v.Value))
	saved, b := parseNumber(field.number.String())
	if !a || !b {
		add("bound", "The reported value and saved field must be supported decimal numbers.", false)
		deferMeasure()
		return result
	}
	if reported.r.Cmp(roundAsReported(saved.r, reported.precision())) == 0 {
		add("bound", fmt.Sprintf("Matches %s at JSON Pointer %q.", v.File, *v.Field), true)
		result.Output, result.Where = key, "output:"+key
		result.Context = around(files[key], field.number.String())
	} else {
		add("bound", fmt.Sprintf("The reported value differs from the saved field in %s at JSON Pointer %q.", v.File, *v.Field), false)
		if saved.r.Sign() != 0 {
			switch {
			case reported.r.Cmp(new(big.Rat).Mul(saved.r, big.NewRat(100, 1))) == 0:
				add("scale", "The reported value is 100 times the saved value. Save and cite the number in the declared unit.", false)
			case reported.r.Cmp(new(big.Rat).Quo(saved.r, big.NewRat(100, 1))) == 0:
				add("scale", "The reported value is one hundredth of the saved value. Save and cite the number in the declared unit.", false)
			}
		}
	}
	if result.Measure != nil && field.taggedMeasure && field.measure != result.Measure.ID {
		add("measure", "The saved field names a different hypothesis measure.", false)
		measureChecked = true
	}
	deferMeasure()
	if result.Measure != nil && field.taggedUnit && !slices.ContainsFunc(result.Binding, func(c review.Check) bool { return c.ID == "scale" }) {
		if field.unit == result.Measure.Unit {
			add("scale", "The field unit matches the hypothesis unit.", true)
		} else {
			add("scale", "The field unit differs from the hypothesis unit. Save and cite the number in the declared unit.", false)
		}
	}
	result.Found = review.ItemStatus(result.Binding) == review.Pass
	return result
}

func boundOutput(value db.Record, v valueRecord, executions, work []db.Record, files map[string]string) (string, string) {
	if v.ScriptRun || !records.ValidScriptOutputPath(v.File) || len(v.Cites) != 1 || !strings.HasPrefix(v.Cites[0], "run:") {
		return "", "Name a saved JSON output and cite the exact script execution that produced it."
	}
	key := strings.TrimPrefix(v.Cites[0], "run:")
	i := slices.IndexFunc(executions, func(r db.Record) bool {
		return r.Key == key && r.Kind == "run" && r.BoardColumn == board.Experimentation &&
			r.WorkspaceID == value.WorkspaceID && r.CardID == value.CardID && r.SupersededAt == nil
	})
	if i < 0 {
		return "", "The cited Experimentation script execution is missing."
	}
	run := executions[i]
	body := records.Body[struct {
		Tool         string
		ExitStatus   *int              `json:"exit_status"`
		OutputHashes map[string]string `json:"output_hashes"`
	}](run)
	if body.Tool != "run_script" || body.ExitStatus == nil || *body.ExitStatus != 0 || run.AgentRunID == nil {
		return "", "The cited record is not a successful script execution."
	}
	hash := body.OutputHashes[v.File]
	if hash == "" {
		return "", "The cited script did not attest this output. Run it with the JSON path listed in outputs."
	}
	var output string
	for _, r := range work {
		if r.Kind != "output" || r.Round != run.Round || r.BoardColumn != board.Experimentation ||
			r.WorkspaceID != run.WorkspaceID || r.CardID != run.CardID || r.SupersededAt != nil ||
			r.AgentRunID == nil || *r.AgentRunID != *run.AgentRunID {
			continue
		}
		meta := records.Body[struct{ Name, SHA256 string }](r)
		if meta.Name != v.File || meta.SHA256 != hash {
			continue
		}
		if data, ok := files[r.Key]; ok && hashText(data) == hash && (output == "" || r.Key < output) {
			output = r.Key
		}
	}
	if output == "" {
		return "", "The cited script did not produce these saved output bytes, or the JSON output could not be read."
	}
	return output, ""
}

func hashText(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

type decodedOutput struct {
	data any
	err  error
}

func decodeOutput(text string) (any, error) {
	d := json.NewDecoder(strings.NewReader(text))
	d.UseNumber()
	data, err := decodeOutputValue(d, 0)
	if err != nil {
		return nil, err
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, errors.New("expected a single JSON document")
	}
	return data, nil
}

func decodeOutputValue(d *json.Decoder, depth int) (any, error) {
	if depth > 256 {
		return nil, errors.New("JSON output is too deeply nested")
	}
	token, err := d.Token()
	if err != nil {
		return nil, err
	}
	var result any
	switch token {
	case json.Delim('{'):
		object := map[string]any{}
		for d.More() {
			k, err := d.Token()
			if err != nil {
				return nil, err
			}
			key, ok := k.(string)
			if !ok {
				return nil, errors.New("invalid JSON object key")
			}
			if _, duplicate := object[key]; duplicate {
				return nil, errors.New("duplicate JSON object key")
			}
			object[key], err = decodeOutputValue(d, depth+1)
			if err != nil {
				return nil, err
			}
		}
		result = object
	case json.Delim('['):
		var list []any
		for d.More() {
			v, err := decodeOutputValue(d, depth+1)
			if err != nil {
				return nil, err
			}
			list = append(list, v)
		}
		result = list
	default:
		return token, nil
	}
	_, err = d.Token()
	return result, err
}

type fieldNumber struct {
	number                    json.Number
	measure, unit             string
	taggedMeasure, taggedUnit bool
}

func numberAt(data any, pointer string) (fieldNumber, bool) {
	var out fieldNumber
	if pointer != "" {
		path, ok := strings.CutPrefix(pointer, "/")
		if !ok {
			return out, false
		}
		for part := range strings.SplitSeq(path, "/") {
			part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
			out = fieldNumber{}
			switch node := data.(type) {
			case map[string]any:
				data = node[part]
				if part == "value" {
					out.measure, out.taggedMeasure = bindingTag(node, "measure")
					out.unit, out.taggedUnit = bindingTag(node, "unit")
				}
			case []any:
				i, err := strconv.Atoi(part)
				if err != nil || i < 0 || i >= len(node) || strconv.Itoa(i) != part {
					return out, false
				}
				data = node[i]
			default:
				return out, false
			}
		}
	}
	n, ok := data.(json.Number)
	out.number = n
	return out, ok
}

func bindingTag(obj map[string]any, key string) (string, bool) {
	v, exists := obj[key]
	s, _ := v.(string)
	return strings.TrimSpace(s), exists
}

// A bound value can only be reproduced by the same field in the output
// attested by the corresponding replay step.
func replayBound(v valueRecord, steps map[string]RerunStepResult, files map[string]string, m review.Measure, tolerance float64) (string, string) {
	if v.Field == nil || v.Measure == nil || m.ID == "" || m.ID != *v.Measure || len(v.Cites) != 1 || v.ScriptRun {
		return "", "the reported field or hypothesis measure could not be resolved"
	}
	runKey, cited := strings.CutPrefix(v.Cites[0], "run:")
	step, found := steps[runKey]
	text, saved := files[v.File]
	if !cited || !found || step.ExitStatus != 0 || !saved || step.OutputHashes[v.File] != hashText(text) {
		return "", "the replayed script did not produce the cited JSON output"
	}
	doc, err := decodeOutput(text)
	if err != nil {
		return "", "the replay output is not unambiguous JSON"
	}
	at, ok := numberAt(doc, *v.Field)
	if !ok {
		return "", "the declared numeric field is missing from the replay output"
	}
	if at.taggedMeasure && at.measure != m.ID || at.taggedUnit && at.unit != m.Unit {
		return "", "the replayed field has a different measure or unit"
	}
	actual, actualOK := parseNumber(at.number.String())
	n, reportedOK := parseNumber(strings.TrimSpace(v.Value))
	if !actualOK || !reportedOK {
		return "", "the replayed field or reported value is not a supported decimal"
	}
	tol := new(big.Rat).SetFloat64(tolerance)
	if tol == nil || tol.Sign() < 0 {
		tol = new(big.Rat)
	}
	tol.Mul(tol, new(big.Rat).Abs(n.r))
	delta := new(big.Rat).Abs(new(big.Rat).Sub(actual.r, n.r))
	if roundAsReported(actual.r, n.precision()).Cmp(n.r) != 0 && delta.Cmp(tol) > 0 {
		return "", fmt.Sprintf("the replayed field holds %s, reported %s", at.number, v.Value)
	}
	return at.number.String(), ""
}
