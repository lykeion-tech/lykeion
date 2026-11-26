package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/board"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

// EnqueueRerun queues the job that replays a study's Experimentation round
// for its Validation round. It runs no agent CLI, so it names no harness and
// no agent; it runs where the study's runs run.
func (s *AgentRuns) EnqueueRerun(ctx context.Context, tx *store.Tx, owner, card uuid.UUID, round int32, requiresOutputAttestation bool) (db.AgentRun, error) {
	return s.Enqueue(ctx, tx, owner, NewAgentRun{Card: card, Column: board.Validation, Round: round, Role: "rerun", kind: "rerun", RequiresOutputAttestation: requiresOutputAttestation})
}

// OnRerun is OnAttempt for a rerun; any other run is not found.
func (s *AgentRuns) OnRerun(ctx context.Context, user uuid.UUID, ref RuntimeRef, run uuid.UUID, fn func(*store.Tx, db.AgentRun) error) error {
	err := s.onAttempt(ctx, user, ref, run, "rerun", fn)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotYours
	}
	return err
}

// Standing is the study's Experimentation as Validation checks it: the
// live records of the Experimentation rounds since Validation last sent the
// study back, in the order they were written, and the values that stand.
type Standing struct {
	Records []db.Record
	// Values are the value records that stand: the latest declaration of
	// each name, unless withdrawn, so a targeted redo keeps the values it
	// carried. Each one's cites are the script runs it rests on (ValueRuns).
	Values []db.Record
}

// StandingExperimentation reads the study's Standing.
func StandingExperimentation(ctx context.Context, tx *store.Tx, card uuid.UUID) (Standing, error) {
	recs, err := tx.Q.ListRecords(ctx, db.ListRecordsParams{WorkspaceID: tx.WS, CardID: card, BoardColumn: board.Experimentation})
	if err != nil {
		return Standing{}, err
	}
	back, err := tx.Q.ListRecords(ctx, db.ListRecordsParams{WorkspaceID: tx.WS, CardID: card, BoardColumn: board.Validation})
	if err != nil {
		return Standing{}, err
	}
	return NewStanding(append(recs, back...)), nil
}

// NewStanding is the Standing among a study's live records.
func NewStanding(study []db.Record) Standing {
	since := int32(-1)
	for _, r := range study {
		if r.BoardColumn == board.Validation && r.Kind == "route_back" {
			since = max(since, r.Round)
		}
	}
	var st Standing
	gone := map[string]bool{}
	for _, r := range study {
		if r.BoardColumn != board.Experimentation || r.Round <= since || r.SupersededAt != nil {
			continue
		}
		st.Records = append(st.Records, r)
		if r.Kind == "withdrawn" {
			var b struct{ Item string }
			if json.Unmarshal(r.Body, &b) == nil {
				gone[b.Item] = true
			}
		}
	}
	latest := map[string]int{}
	var order []string
	for i, r := range st.Records {
		if r.Kind != "value" {
			continue
		}
		name := valueOf(r).Name
		if _, ok := latest[name]; !ok {
			order = append(order, name)
		}
		latest[name] = i
	}
	for _, name := range order {
		r := st.Records[latest[name]]
		if gone[r.Key] {
			continue
		}
		v := valueOf(r)
		if v.Field == nil && v.Measure == nil {
			v.Cites = st.ValueRuns(v.File, v.Cites)
		}
		if v.Cites == nil {
			v.Cites = []string{}
		}
		if b, err := json.Marshal(v); err == nil {
			r.Body = b
		}
		st.Values = append(st.Values, r)
	}
	return st
}

type standingValue struct {
	Name      string   `json:"name"`
	Value     string   `json:"value"`
	File      string   `json:"file"`
	Cites     []string `json:"cites"`
	ScriptRun bool     `json:"script_run,omitempty"`
	Field     *string  `json:"field,omitempty"`
	Measure   *string  `json:"measure,omitempty"`
}

func valueOf(r db.Record) standingValue {
	var v standingValue
	_ = json.Unmarshal(r.Body, &v)
	return v
}

// ValueRuns are the script runs, run:KEY, a value shown in file rests on:
// cites, when the value names any; else ScriptRuns.
func (st Standing) ValueRuns(file string, cites []string) []string {
	if len(cites) > 0 {
		return cites
	}
	return ScriptRuns(st.Records, file)
}

// ScriptRuns are the script runs, run:KEY, behind file among a study's
// Experimentation records: the run_script runs of the script at that path;
// else, when file is a saved output, the run_script runs that exited 0 made
// by the run that saved it, in the round it saved it. Nil when none.
func ScriptRuns(exp []db.Record, file string) []string {
	type run struct {
		Tool       string `json:"tool"`
		Path       string `json:"path"`
		ExitStatus *int   `json:"exit_status"`
	}
	var out []string
	for _, r := range exp {
		var b run
		if r.Kind == "run" && json.Unmarshal(r.Body, &b) == nil && b.Tool == "run_script" && b.Path == file {
			out = append(out, "run:"+r.Key)
		}
	}
	if out != nil {
		return out
	}
	for _, o := range exp {
		var saved struct{ Name string }
		if o.Kind != "output" || o.AgentRunID == nil || json.Unmarshal(o.Body, &saved) != nil || saved.Name != file {
			continue
		}
		for _, r := range exp {
			var b run
			if r.Kind == "run" && r.Round == o.Round && r.AgentRunID != nil && *r.AgentRunID == *o.AgentRunID &&
				json.Unmarshal(r.Body, &b) == nil && b.Tool == "run_script" && b.ExitStatus != nil && *b.ExitStatus == 0 &&
				!slices.Contains(out, "run:"+r.Key) {
				out = append(out, "run:"+r.Key)
			}
		}
	}
	return out
}

// RerunSteps are the script runs the standing values rest on that exited
// 0, in the order they ran, each with the sha256 of the package list it
// saved, empty when it saved none. Code run with run_code is never replayed.
func (st Standing) RerunSteps() []RerunStep {
	cited := map[string]bool{}
	outputs := map[string][]string{}
	for _, v := range st.Values {
		value := valueOf(v)
		for _, c := range value.Cites {
			if k, ok := strings.CutPrefix(c, "run:"); ok {
				cited[k] = true
				if len(value.Cites) == 1 && !value.ScriptRun && strings.HasPrefix(value.File, "outputs/") &&
					!slices.Contains(outputs[k], value.File) {
					outputs[k] = append(outputs[k], value.File)
				}
			}
		}
	}
	var runs []db.Record
	for _, r := range st.Records {
		if cited[r.Key] {
			runs = append(runs, r)
		}
	}
	steps := RerunSteps(runs)
	for i := range steps {
		steps[i].OutputPaths = outputs[steps[i].Key]
	}
	return steps
}

// ReplayOutputName identifies a JSON snapshot from a particular replay step
// without reusing the shared scratch path where that step wrote it.
func ReplayOutputName(key, name string) string {
	return fmt.Sprintf("outputs/.lyk-replay/%x.json", sha256.Sum256([]byte(key+"\x00"+name)))
}

// ReplayInputs restores published files from the runs that produced cited
// scripts. Measured output paths and replayed scripts must be written anew.
func (st Standing) ReplayInputs(steps []RerunStep) []ReplayInput {
	keys, excluded := map[string]bool{}, map[string]bool{}
	for _, step := range steps {
		keys[step.Key] = true
		excluded[strings.ToLower(step.Path)] = true
		for _, name := range step.OutputPaths {
			excluded[strings.ToLower(name)] = true
		}
	}
	producers := map[uuid.UUID]bool{}
	for _, r := range st.Records {
		if keys[r.Key] && r.AgentRunID != nil {
			producers[*r.AgentRunID] = true
		}
	}
	var inputs []ReplayInput
	byName := map[string]string{}
	for _, r := range st.Records {
		var in ReplayInput
		if r.Kind != "output" || r.AgentRunID == nil || !producers[*r.AgentRunID] || json.Unmarshal(r.Body, &in) != nil {
			continue
		}
		name := strings.ToLower(in.Name)
		if !strings.HasPrefix(in.Name, "outputs/") || excluded[name] {
			continue
		}
		if prior, ok := byName[name]; ok {
			// A shared replay folder cannot represent conflicting versions.
			if prior != in.Sha256 {
				excluded[name] = true
			}
			continue
		}
		byName[name] = in.Sha256
		in.Key = r.Key
		inputs = append(inputs, in)
	}
	return slices.DeleteFunc(inputs, func(in ReplayInput) bool { return excluded[strings.ToLower(in.Name)] })
}

// RerunSteps are the script runs in recs that exited 0, in order, each with
// the sha256 of the package list it saved, empty when it saved none. Code run
// with run_code is never replayed.
func RerunSteps(recs []db.Record) []RerunStep {
	var steps []RerunStep
	for _, r := range recs {
		if r.Kind != "run" {
			continue
		}
		var b struct {
			Tool, Path, Language, Script string
			ExitStatus                   *int   `json:"exit_status"`
			Packages                     string `json:"packages_sha256"`
		}
		if json.Unmarshal(r.Body, &b) != nil || b.Tool != "run_script" || b.ExitStatus == nil || *b.ExitStatus != 0 {
			continue
		}
		steps = append(steps, RerunStep{Key: r.Key, Path: b.Path, Language: b.Language, Script: b.Script, packagesSHA: b.Packages})
	}
	return steps
}
