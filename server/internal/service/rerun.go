package service

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/board"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

// EnqueueRerun queues the job that replays a study's Experimentation round
// for its Validation round. It runs no agent CLI, so it names no harness and
// no agent; it runs where the study's runs run.
func (s *AgentRuns) EnqueueRerun(ctx context.Context, tx *store.Tx, owner, card uuid.UUID, round int32) (db.AgentRun, error) {
	return s.Enqueue(ctx, tx, owner, NewAgentRun{Card: card, Column: board.Validation, Round: round, Role: "rerun", kind: "rerun"})
}

// OnRerun is OnAttempt for a rerun; any other run is not found.
func (s *AgentRuns) OnRerun(ctx context.Context, user uuid.UUID, ref RuntimeRef, run uuid.UUID, fn func(*store.Tx, db.AgentRun) error) error {
	err := s.onAttempt(ctx, user, ref, run, "rerun", fn)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotYours
	}
	return err
}

// ExperimentationRound is the live records of the study's newest
// Experimentation round, in the order they were written: the round a
// Validation rerun replays.
func ExperimentationRound(ctx context.Context, tx *store.Tx, card uuid.UUID) ([]db.Record, error) {
	recs, err := tx.Q.ListRecords(ctx, db.ListRecordsParams{WorkspaceID: tx.WS, CardID: card, BoardColumn: board.Experimentation})
	if err != nil {
		return nil, err
	}
	var round int32
	for _, r := range recs {
		round = max(round, r.Round)
	}
	var out []db.Record
	for _, r := range recs {
		if r.Round == round {
			out = append(out, r)
		}
	}
	return out, nil
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
