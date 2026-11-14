package store

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"time"
)

// CoresearchClock returns the database wall clock after the caller's locks.
func (t *Tx) CoresearchClock(ctx context.Context) (time.Time, error) {
	var now time.Time
	err := t.raw.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&now)
	return now, err
}

// CoresearchClaimStillLive rechecks a claim after its writes, including time spent
// waiting inside the write. The caller must already hold the workspace locks.
func (t *Tx) CoresearchClaimStillLive(ctx context.Context, kind string, id uuid.UUID, token *uuid.UUID) (bool, error) {
	var query string
	switch kind {
	case "cell":
		query = `SELECT EXISTS(SELECT 1 FROM coresearch_cell WHERE workspace_id=$1 AND id=$2 AND claim_token=$3 AND lease_expires_at>clock_timestamp())`
	case "run":
		query = `SELECT EXISTS(SELECT 1 FROM coresearch_run WHERE workspace_id=$1 AND id=$2 AND claim_token=$3 AND lease_expires_at>clock_timestamp())`
	case "setup":
		query = `SELECT EXISTS(SELECT 1 FROM coresearch_setup WHERE workspace_id=$1 AND id=$2 AND claim_token=$3 AND lease_expires_at>clock_timestamp())`
	case "build":
		query = `SELECT EXISTS(SELECT 1 FROM coresearch_build WHERE workspace_id=$1 AND id=$2 AND claim_token=$3 AND lease_expires_at>clock_timestamp())`
	default:
		return false, fmt.Errorf("unknown coresearch claim kind %q", kind)
	}
	var live bool
	err := t.raw.QueryRow(ctx, query, t.WS, id, token).Scan(&live)
	return live, err
}

func (t *Tx) CoresearchCellCandidateReferenced(ctx context.Context, cell, attempt uuid.UUID) (bool, error) {
	var referenced bool
	err := t.raw.QueryRow(ctx, "SELECT public.coresearch_cell_candidate_referenced($1,$2)", cell, attempt).Scan(&referenced)
	return referenced, err
}

// LockCoresearchCellGeneration prevents schema downgrade until this publish commits.
func (t *Tx) LockCoresearchCellGeneration(ctx context.Context) error {
	var enabled bool
	err := t.raw.QueryRow(ctx, "SELECT enabled FROM public.coresearch_cell_generation_capability").Scan(&enabled)
	if err != nil {
		return err
	}
	if !enabled {
		return fmt.Errorf("cell output generation unavailable")
	}
	return nil
}
