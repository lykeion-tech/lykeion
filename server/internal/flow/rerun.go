package flow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math/big"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/board"
	"lykeion/server/internal/records"
	"lykeion/server/internal/review"
	"lykeion/server/internal/service"
	"lykeion/server/internal/store"
	"lykeion/server/pkg/agent"
	db "lykeion/server/pkg/db/generated"
)

const (
	noScriptNote    = "no recorded script run produced this value"
	environmentNote = "the saved package list could not be rebuilt"
	rerunFailedNote = "the rerun failed before reporting a result"
)

// RerunStepResult is one replayed script as the daemon reports it, under the
// key of the run record it replays.
type RerunStepResult struct {
	Key        string `json:"key"`
	ExitStatus int    `json:"exit_status"`
	Output     string `json:"output"`
	Error      string `json:"error"`
}

// RerunOutput is a file the rerun saved through the outputs upload, named as
// the study's run saved it, with the sha256 of its text.
type RerunOutput struct {
	Name       string `json:"name"`
	TextSHA256 string `json:"text_sha256"`
}

// RerunResult is what a rerun's daemon posts. Failure is empty when every
// step ran, "environment" when the saved package list could not be rebuilt,
// or an infrastructure failure, which the harness retries.
type RerunResult struct {
	Steps   []RerunStepResult `json:"steps"`
	Outputs []RerunOutput     `json:"outputs"`
	Failure *string           `json:"failure"`
}

// rerunBody is the server's `rerun` record: what each replayed step did;
// per value name, whether the rerun reproduced it and, if not, why; and per
// assessed hypothesis's text, trimmed, whether the rerun printed the test
// its assessment reports.
type rerunBody struct {
	Steps      []rerunStep       `json:"steps"`
	Outputs    []RerunOutput     `json:"outputs"`
	Reproduced map[string]bool   `json:"reproduced"`
	Notes      map[string]string `json:"notes"`
	Printed    []string          `json:"printed"` // every number the rerun printed, as exact fractions
	Failure    *string           `json:"failure"`
}

type rerunStep struct {
	Of           string `json:"of"`
	ExitStatus   int    `json:"exit_status"`
	OutputSHA256 string `json:"output_sha256"`
}

// startRerun queues, once per Validation round, the job that replays the
// study's newest Experimentation round on its machine. A round with no
// script run to replay needs no job: its values are recorded as not
// reproduced at once.
func (e *Engine) startRerun(ctx context.Context, tx *store.Tx, c db.Card) error {
	runs, err := tx.Q.RoundAgentRuns(ctx, db.RoundAgentRunsParams{WorkspaceID: tx.WS, CardID: c.ID, BoardColumn: c.BoardColumn, Round: c.Round})
	if err != nil {
		return err
	}
	// A rerun cancelled, its machine withdrawn, or refused when claimed
	// left no record: the round gets another.
	if slices.ContainsFunc(runs, func(r db.AgentRun) bool { return r.Kind == "rerun" && r.Status != "cancelled" && !refusedAtClaim(r) }) {
		return nil
	}
	recs, err := tx.Q.ListRecords(ctx, db.ListRecordsParams{WorkspaceID: tx.WS, CardID: c.ID, BoardColumn: c.BoardColumn})
	if err != nil {
		return err
	}
	if slices.ContainsFunc(recs, func(r db.Record) bool { return r.Kind == "rerun" && r.Round == c.Round }) {
		return nil
	}
	st, err := service.StandingExperimentation(ctx, tx, c.ID)
	if err != nil {
		return err
	}
	if steps := st.RerunSteps(); len(steps) > 0 {
		// A single replay folder cannot faithfully reproduce scripts that saw
		// different study files. Never silently replay them with newer inputs.
		var source *uuid.UUID
		var snapshot []uuid.UUID
		for _, step := range steps {
			for _, record := range st.Records {
				if record.Key != step.Key || record.AgentRunID == nil {
					continue
				}
				ids, err := tx.RunAttachmentSnapshot(ctx, *record.AgentRunID)
				if err != nil {
					return err
				}
				if source == nil {
					source = record.AgentRunID
					snapshot = ids
				} else if !slices.Equal(snapshot, ids) {
					ok, notes := noneReproduced(st.Values, "the scripts used different attached files; they cannot be replayed together")
					return writeRerun(ctx, tx, c.ID, c.BoardColumn, c.Round, nil, rerunBody{Steps: []rerunStep{}, Outputs: []RerunOutput{}, Reproduced: ok, Notes: notes})
				}
			}
		}
		run, err := e.AgentRuns.EnqueueRerun(ctx, tx, c.OwnerID, c.ID, c.Round)
		if err != nil {
			return err
		}
		if source != nil {
			return tx.CopyRunAttachmentSnapshot(ctx, *source, run.ID)
		}
		return nil
	}
	ok, notes := noneReproduced(st.Values, noScriptNote)
	return writeRerun(ctx, tx, c.ID, c.BoardColumn, c.Round, nil, rerunBody{Steps: []rerunStep{}, Outputs: []RerunOutput{}, Reproduced: ok, Notes: notes})
}

// RerunResult takes a rerun's result from the daemon running it. An
// infrastructure failure is the harness's: the run is retried and nothing is
// recorded. Otherwise the result becomes the study's `rerun` record, once,
// and the run completes.
func (e *Engine) RerunResult(ctx context.Context, user uuid.UUID, ref service.RuntimeRef, id uuid.UUID, in RerunResult) error {
	infra := ""
	if in.Failure != nil {
		switch f := *in.Failure; {
		case f == "environment":
		case f == "machine_offline":
			infra = agent.ReasonRuntimeOffline
		case service.Infra(f):
			infra = f
		default:
			return service.Invalid("failure must be environment or an infrastructure failure")
		}
	}
	if infra != "" {
		if err := e.AgentRuns.OnRerun(ctx, user, ref, id, func(*store.Tx, db.AgentRun) error { return nil }); err != nil {
			return err
		}
		_, err := e.AgentRuns.Fail(ctx, user, ref, id, service.Report{FailureReason: infra, Error: "the rerun could not run: " + *in.Failure})
		return err
	}
	var exp, saved []db.Record
	var claimed []service.RerunStep
	done := false
	tolerance := 0.0
	err := e.AgentRuns.OnRerun(ctx, user, ref, id, func(tx *store.Tx, t db.AgentRun) error {
		own, err := tx.Q.AgentRunRecords(ctx, db.AgentRunRecordsParams{WorkspaceID: tx.WS, AgentRunID: &t.ID})
		if err != nil {
			return err
		}
		for _, r := range own {
			switch r.Kind {
			case "rerun":
				done = true
			case "rerun_output":
				saved = append(saved, r)
			}
		}
		st, err := service.StandingExperimentation(ctx, tx, *t.CardID)
		if err != nil {
			return err
		}
		exp, claimed = st.Values, st.RerunSteps()
		_, m, err := columnTeam(ctx, tx, *t.CardID, board.Experimentation)
		if err == nil {
			tolerance = m.Validation.Tolerance
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}
	if !done {
		// A step the claim did not hand out is not the round's; it is dropped.
		in.Steps = slices.DeleteFunc(in.Steps, func(s RerunStepResult) bool {
			return !slices.ContainsFunc(claimed, func(c service.RerunStep) bool { return c.Key == s.Key })
		})
		body, err := e.rerunRecord(ctx, exp, saved, in, tolerance)
		if err != nil {
			return err
		}
		err = e.AgentRuns.OnRerun(ctx, user, ref, id, func(tx *store.Tx, t db.AgentRun) error {
			own, err := tx.Q.AgentRunRecords(ctx, db.AgentRunRecordsParams{WorkspaceID: tx.WS, AgentRunID: &t.ID})
			if err != nil || slices.ContainsFunc(own, func(r db.Record) bool { return r.Kind == "rerun" }) {
				return err
			}
			return writeRerun(ctx, tx, *t.CardID, t.BoardColumn, t.Round, &t.ID, body)
		})
		if err != nil {
			return err
		}
	}
	_, err = e.AgentRuns.Complete(ctx, user, ref, id, service.Report{})
	return err
}

// refusedAtClaim says whether the server refused a rerun when a machine
// claimed it, before any daemon started it: its machine no longer shared,
// or another claim refusal. It never ran.
func refusedAtClaim(r db.AgentRun) bool {
	return r.Status == "failed" && r.StartedAt == nil &&
		(ptrOr(r.FailureReason) == "runtime_access_denied" || strings.HasPrefix(r.Error, "refused at claim: "))
}

// afterRerun records, for a rerun that failed without a result once its
// daemon had started it, every value of its round not reproduced, so its
// Validation round does not wait for ever. A cancelled rerun, its study
// stopped or its machine withdrawn, or one refused when claimed, records
// nothing and is queued again; an infrastructure failure was parked, not
// settled.
func (e *Engine) afterRerun(ctx context.Context, t db.AgentRun) error {
	if t.Status != "failed" || refusedAtClaim(t) {
		return nil
	}
	return e.Store.InWorkspace(ctx, t.WorkspaceID, uuid.Nil, func(tx *store.Tx) error {
		if _, err := lockCard(ctx, tx, *t.CardID); err != nil {
			return err
		}
		recs, err := tx.Q.ListRecords(ctx, db.ListRecordsParams{WorkspaceID: tx.WS, CardID: *t.CardID, BoardColumn: t.BoardColumn})
		if err != nil || slices.ContainsFunc(recs, func(r db.Record) bool { return r.Kind == "rerun" && r.Round == t.Round }) {
			return err
		}
		st, err := service.StandingExperimentation(ctx, tx, *t.CardID)
		if err != nil {
			return err
		}
		ok, notes := noneReproduced(st.Values, rerunFailedNote)
		return writeRerun(ctx, tx, *t.CardID, t.BoardColumn, t.Round, &t.ID, rerunBody{Steps: []rerunStep{}, Outputs: []RerunOutput{}, Reproduced: ok, Notes: notes})
	})
}

// rerunRecord turns a result into the record's body, checking the standing
// values in exp and keeping every number it printed, for the tests the
// lead's assessments report, which may come later. The saved files it names
// must be outputs this rerun uploaded, with the text it names.
func (e *Engine) rerunRecord(ctx context.Context, exp, saved []db.Record, in RerunResult, tolerance float64) (rerunBody, error) {
	body := rerunBody{Steps: []rerunStep{}, Outputs: []RerunOutput{}, Printed: []string{}, Failure: in.Failure}
	steps := map[string]RerunStepResult{}
	for _, s := range in.Steps {
		steps[s.Key] = s
		sum := sha256.Sum256([]byte(s.Output))
		body.Steps = append(body.Steps, rerunStep{Of: s.Key, ExitStatus: s.ExitStatus, OutputSHA256: hex.EncodeToString(sum[:])})
	}
	var kept []db.Record
	for _, o := range in.Outputs {
		i := slices.IndexFunc(saved, func(r db.Record) bool {
			b := records.Body[struct{ Name, Sha256 string }](r)
			return b.Name == o.Name && b.Sha256 == o.TextSHA256
		})
		if i < 0 {
			return body, service.Invalid(fmt.Sprintf("output %s was not saved by this rerun", o.Name))
		}
		kept = append(kept, saved[i])
		body.Outputs = append(body.Outputs, o)
	}
	if in.Failure != nil {
		body.Reproduced, body.Notes = noneReproduced(exp, environmentNote)
		return body, nil
	}
	texts, err := e.outputTexts(ctx, kept)
	if err != nil {
		return body, fmt.Errorf("reading the rerun's saved files: %w", err)
	}
	files := map[string]string{}
	for _, r := range kept {
		if text, ok := texts[r.Key]; ok {
			files[records.Body[struct{ Name string }](r).Name] = text
		}
	}
	body.Reproduced, body.Notes = reproduce(exp, steps, files, tolerance)
	body.Printed = printedNumbers(steps, files)
	return body, nil
}

// printedNumbers is every number the steps that succeeded and the saved
// files printed, once each, as exact fractions.
func printedNumbers(steps map[string]RerunStepResult, files map[string]string) []string {
	var texts []string
	for _, s := range steps {
		if s.ExitStatus == 0 {
			texts = append(texts, s.Output)
		}
	}
	for _, f := range files {
		texts = append(texts, f)
	}
	seen := map[string]bool{}
	for _, text := range texts {
		eachToken(text, func(_ int, t *big.Rat) bool {
			seen[t.RatString()] = true
			return true
		})
	}
	return slices.Sorted(maps.Keys(seen))
}

// testsPrinted says, per assessment's hypothesis, trimmed, whether the rerun
// printed the test the assessment reports: its statistic and, when p is
// given exactly, its p, each equal to a printed number rounded to the digits
// reported. The Team's tolerance for values does not apply: a statistic
// near the printed one is not the printed one. An assessment with no test is
// left out.
func testsPrinted(as []review.Assessment, printed []string) map[string]bool {
	var nums []*big.Rat
	for _, p := range printed {
		if r, ok := new(big.Rat).SetString(p); ok {
			nums = append(nums, r)
		}
	}
	out := map[string]bool{}
	for _, a := range as {
		if a.Test == nil {
			continue
		}
		reported := []string{strconv.FormatFloat(a.Test.Statistic, 'f', max(a.Test.StatDigits, 0), 64)}
		if a.Test.PBound == "=" {
			reported = append(reported, strconv.FormatFloat(a.Test.P, 'f', max(a.Test.PDigits, 0), 64))
		}
		found := true
		for _, v := range reported {
			n, ok := parseNumber(v)
			found = found && ok && slices.ContainsFunc(nums, func(t *big.Rat) bool {
				if n.integer {
					return t.Cmp(n.r) == 0
				}
				return roundTo(t, n.places).Cmp(n.r) == 0
			})
		}
		out[strings.TrimSpace(a.Hypothesis)] = found
	}
	return out
}

// noneReproduced marks every value in recs not reproduced, for note.
func noneReproduced(recs []db.Record, note string) (map[string]bool, map[string]string) {
	ok, notes := map[string]bool{}, map[string]string{}
	for _, r := range recs {
		if r.Kind == "value" {
			name := valueOf(r).Name
			ok[name], notes[name] = false, note
		}
	}
	return ok, notes
}

// reproduce checks each value in recs against what the rerun printed in the
// steps replaying the runs it cites and in the saved file it names. A value
// is reproduced when a number found there equals it rounded to its stated
// digits, the rule the Experimentation check uses, or lies within tolerance
// of it, relative to it. A value reported under one name more than once is
// reproduced only when each is.
func reproduce(recs []db.Record, steps map[string]RerunStepResult, files map[string]string, tolerance float64) (map[string]bool, map[string]string) {
	ok, notes := map[string]bool{}, map[string]string{}
	for _, r := range recs {
		if r.Kind != "value" {
			continue
		}
		v := valueOf(r)
		if prev, seen := ok[v.Name]; seen && !prev {
			continue
		}
		yes, note := reproduced(v, steps, files, tolerance)
		ok[v.Name] = yes
		if !yes {
			notes[v.Name] = note
		}
	}
	return ok, notes
}

// reproduced checks one value. When it was not reproduced, the note names
// the number the rerun printed nearest to it. ponytail: nearest, not the
// number at the value's place in the original output; match by position if
// the nearest misleads.
func reproduced(v valueRecord, steps map[string]RerunStepResult, files map[string]string, tolerance float64) (bool, string) {
	var texts []string
	failed := 0
	for _, c := range v.Cites {
		s, ok := steps[strings.TrimPrefix(c, "run:")]
		switch {
		case !ok:
		case s.ExitStatus != 0:
			failed = s.ExitStatus
		default:
			texts = append(texts, s.Output)
		}
	}
	// As in the Experimentation check, a value a replayed script made may
	// be in any file the rerun saved; one no replayed script made only in
	// the file it names.
	if len(texts) > 0 {
		for _, name := range slices.Sorted(maps.Keys(files)) {
			texts = append(texts, files[name])
		}
	} else if text, ok := files[v.File]; ok {
		texts = append(texts, text)
	}
	n, isNumber := parseNumber(strings.TrimSpace(v.Value))
	switch {
	case len(texts) == 0 && failed != 0:
		return false, fmt.Sprintf("rerun exited with status %d, reported %s", failed, v.Value)
	case len(texts) == 0:
		return false, noScriptNote
	case !isNumber:
		// A named outcome is reproduced when the rerun prints its words.
		if wordsIn(texts, v.Value) {
			return true, ""
		}
		return false, fmt.Sprintf("rerun did not print %s", strings.TrimSpace(v.Value))
	}
	tol := new(big.Rat).Mul(new(big.Rat).SetFloat64(tolerance), new(big.Rat).Abs(n.r))
	var gap *big.Rat
	nearest := ""
	for _, text := range texts {
		found := false
		eachToken(text, func(start int, t *big.Rat) bool {
			at := t
			if !n.integer {
				at = roundTo(t, n.places)
			}
			d := new(big.Rat).Abs(new(big.Rat).Sub(t, n.r))
			if at.Cmp(n.r) == 0 || d.Cmp(tol) <= 0 {
				found = true
				return false
			}
			if gap == nil || d.Cmp(gap) < 0 {
				gap, nearest = d, numberToken.FindString(text[start:])
			}
			return true
		})
		if found {
			return true, ""
		}
	}
	if gap == nil {
		return false, fmt.Sprintf("rerun printed no number, reported %s", v.Value)
	}
	return false, fmt.Sprintf("rerun printed %s, reported %s", nearest, v.Value)
}

// wordsIn says whether text, trimmed, is printed in texts as whole words,
// in any case.
func wordsIn(texts []string, text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	re := regexp.MustCompile(`(?i)(^|[^\pL\pN_])` + regexp.QuoteMeta(text) + `($|[^\pL\pN_])`)
	return slices.ContainsFunc(texts, re.MatchString)
}

// writeRerun stores the server's `rerun` record for a Validation round,
// naming the rerun run that made it, if any.
func writeRerun(ctx context.Context, tx *store.Tx, card uuid.UUID, column string, round int32, run *uuid.UUID, body rerunBody) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	key, err := records.NextRecordKey(ctx, tx, card)
	if err != nil {
		return err
	}
	rec, err := tx.Q.InsertRecord(ctx, db.InsertRecordParams{WorkspaceID: tx.WS, CardID: card, BoardColumn: column,
		Round: round, Kind: "rerun", Role: "server", Key: key, Body: b, AgentRunID: run})
	if err != nil {
		return err
	}
	return tx.Emit(ctx, "record.created", &card, store.System, map[string]any{"record_id": rec.ID, "kind": "rerun", "key": key, "role": "server"})
}
