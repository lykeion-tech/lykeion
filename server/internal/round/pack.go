package round

import (
	"context"
	"slices"

	"lykeion/server/internal/board"
	"lykeion/server/internal/gate"
	"lykeion/server/internal/ground"
	"lykeion/server/internal/jev"
	"lykeion/server/internal/pack"
	"lykeion/server/internal/pack/check"
	"lykeion/server/internal/records"
	"lykeion/server/internal/service"
	"lykeion/server/internal/storage"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

// ServerKinds are the record kinds only the server writes: the sources a role
// called, the gate and its choices, the card's lifecycle. They are accepted on
// every card whatever its pack, and no pack can let a role write one.
var ServerKinds = []string{"fetch", "search", "open_dataset", "choice", "starting_point", "stop", "report_status", "reopen_evidence", "gate", "gate_draft"}

// checkAttempts is how many times a pack check that errors is run in one gate
// before the card is blocked on infrastructure.
const checkAttempts = 3

// checkSlots bounds the check child processes running at once in this server;
// each may use 512 MiB.
var checkSlots = make(chan struct{}, 2)

// runPackCheck runs a pack's column check; tests wrap it to count the runs.
var runPackCheck = (*pack.Pack).Check

// rolesFor returns the card's column roles: from its pinned pack when PackID is set, else StageRoles.
func (e *Engine) rolesFor(ctx context.Context, tx *store.Tx, c db.Card) (Roles, *pack.Pack, bool, error) {
	return loadRoles(ctx, tx, e.Blobs, c)
}

func loadRoles(ctx context.Context, tx *store.Tx, blobs storage.Storage, c db.Card) (Roles, *pack.Pack, bool, error) {
	if c.PackID == nil {
		r, ok := StageRoles[c.BoardColumn]
		return r, nil, ok, nil
	}
	p, err := pack.LoadPack(ctx, tx, blobs, tx.WS, *c.PackID)
	if err != nil {
		return Roles{}, nil, false, err
	}
	r, ok := packRoles(p, c.BoardColumn)
	return r, p, ok, nil
}

// KindsFor is what the card's roles may write in its column: its pack's
// kinds, or every legacy stage's kinds for a card without a pack.
func KindsFor(ctx context.Context, tx *store.Tx, blobs storage.Storage, c db.Card) (map[string]service.Kind, error) {
	if c.PackID == nil {
		return Kinds(), nil
	}
	r, _, ok, err := loadRoles(ctx, tx, blobs, c)
	if err != nil || !ok {
		return map[string]service.Kind{}, err
	}
	return r.Kinds(), nil
}

// packRoles is a pack column as stage roles: the manifest's phases, reviewer
// and kinds, the result kinds for every role, the reviewer's review_item and
// verdict, and the column's check script as the gate.
func packRoles(p *pack.Pack, column string) (Roles, bool) {
	col, ok := p.Manifest.Columns[column]
	if !ok {
		return Roles{}, false
	}
	kinds := func() map[string]service.Kind {
		out := map[string]service.Kind{}
		for name, d := range col.Kinds {
			if !slices.Contains(ServerKinds, name) {
				out[name] = service.Kind{Roles: d.Roles, KeyPrefix: d.KeyPrefix}
			}
		}
		review := ground.Kinds()
		for _, name := range []string{"review_item", "verdict"} {
			k := review[name]
			k.Roles = []string{col.Reviewer}
			out[name] = k
		}
		all := slices.Concat(slices.Concat(col.Phases...), []string{col.Reviewer})
		for _, name := range ResultKinds {
			out[name] = service.Kind{Roles: all, Validate: needReason}
		}
		return out
	}
	roleKinds := func(role string) []string {
		var out []string
		for name, k := range kinds() {
			if !slices.Contains(ResultKinds, name) && slices.Contains(k.Roles, role) {
				out = append(out, name)
			}
		}
		slices.Sort(out)
		return out
	}
	return Roles{
		Phases: col.Phases, Reviewer: col.Reviewer, Kinds: kinds, RoleKinds: roleKinds, Finalize: gate.Finalize,
		Check: func(ctx context.Context, v *records.View, _ *jev.Client) (*board.Gate, error) {
			return packGate(ctx, p, column, v)
		},
		Skill: func(role string) string { return p.Skill(column, role) },
	}, true
}

// packGate runs the column's check script over the card's records. A failing
// check decides the gate at once; when every check passes the reviewer
// settles it. A script that keeps erroring blocks the card on infrastructure.
func packGate(ctx context.Context, p *pack.Pack, column string, v *records.View) (*board.Gate, error) {
	g := &board.Gate{Column: column, InputDigest: v.Digest(ground.ReviewKinds...)}
	recs, err := checkRecords(v, column)
	if err != nil {
		return nil, err
	}
	var res []check.Result
	for range checkAttempts {
		if res, err = runCheck(ctx, p, column, recs); err == nil {
			break
		}
	}
	if err != nil {
		g.Verdict, g.Block, g.Reason = board.CannotProceed, board.BlockInfra, err.Error()
		return g, nil
	}
	decided := len(res) == 0
	for _, r := range res {
		ch := board.Check{ID: r.ID, Judge: board.Code, Result: board.Pass, Note: r.Note}
		if !r.OK {
			ch.Result, ch.To = board.Fail, p.Target(r.Failure)
			decided = true
		}
		g.Checks = append(g.Checks, ch)
	}
	if decided {
		board.Decide(g)
	}
	return g, nil
}

func runCheck(ctx context.Context, p *pack.Pack, column string, recs []check.Record) ([]check.Result, error) {
	select {
	case checkSlots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-checkSlots }()
	return runPackCheck(p, ctx, column, recs)
}

// checkRecords is what a check reads: the live records of every earlier
// column, then the column's own. The reviewer's records and the gates are
// left out, as from the gate's input digest: they answer a gate, never feed it.
func checkRecords(v *records.View, column string) ([]check.Record, error) {
	views := []*records.View{}
	for _, col := range board.Stages[:max(board.StageIndex(column), 0)] {
		ev, err := v.Column(col)
		if err != nil {
			return nil, err
		}
		views = append(views, ev)
	}
	var out []check.Record
	for _, cv := range append(views, v) {
		for _, r := range cv.All() {
			if slices.Contains(ground.ReviewKinds, r.Kind) {
				continue
			}
			out = append(out, check.Record{Kind: r.Kind, Key: r.Key, Role: r.Role, Column: r.BoardColumn,
				Body: records.Body[map[string]any](r)})
		}
	}
	return out, nil
}
