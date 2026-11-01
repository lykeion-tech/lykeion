package flow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"lykeion/server/internal/board"
	"lykeion/server/internal/jev"
	"lykeion/server/internal/records"
	"lykeion/server/internal/review"
	"lykeion/server/internal/sources"
	"lykeion/server/internal/storage"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

var errNoSources = errors.New("the server has no sources lookup")

// maxOutputText is the largest saved file read as text for the value check;
// larger files are left out. ponytail: whole-file read, stream the tokens if
// big text outputs must count.
const maxOutputText = 8 << 20

// reviewBlockedText is the Inbox item for a column that failed its review
// after its one redo; failed lists the labels of the items that failed.
func reviewBlockedText(number int32, column, failed string) string {
	return fmt.Sprintf("Study #%d's %s column failed Jev's review twice: %s. Comment on the study to tell the Team what to fix.", number, title(column), failed)
}

// usesReview says whether the study's current column is reviewed item by item:
// Grounding, Hypotheses and Experimentation, unless the column was already
// under way when item review shipped.
func usesReview(c db.Card) bool {
	return c.BoardColumn != c.LegacyColumn && c.BoardColumn != board.Validation
}

// reviewing is what one pass read to review a column's items, with no
// transaction open. The auditor's findings are never part of it: Jev reviews
// the items and their evidence alone.
type reviewing struct {
	criteria []string
	items    []db.Record          // this round's item records
	carried  []review.Item        // the last attempt's items that stand, withdrawn or failed unanswered
	cited    map[string]db.Record // what the hypotheses cite, by the cite as written
	saved    []string             // names of the files saved in this column
	work     []db.Record          // this round's records that may report values, and the column's outputs
	runs     []db.Record          // the run records the work cites
}

// reviewColumn reviews the study's current column item by item, writes the
// review and its outcome, and moves the study on, carries its unclear items
// forward, redoes the failed items or blocks it. Jev and the sources are
// asked with no transaction open; when either cannot answer, the study stays
// where it is, as with scoring.
func (e *Engine) reviewColumn(ctx context.Context, ws uuid.UUID, in scoring) error {
	rv, err := e.readReview(ctx, ws, in.card)
	var res review.Result
	if err != nil {
		err = fmt.Errorf("reading the %s review's inputs: %w", in.card.BoardColumn, err)
	} else if res, err = e.runReview(ctx, ws, in, rv); err != nil {
		err = fmt.Errorf("%s review: %w", in.card.BoardColumn, err)
	}
	moved, werr := e.applyReview(ctx, ws, in, res, err)
	if werr != nil {
		return werr
	}
	if moved {
		e.Kick(ws, in.card.ID)
	}
	return nil
}

func (e *Engine) readReview(ctx context.Context, ws uuid.UUID, c db.Card) (reviewing, error) {
	var rv reviewing
	if err := json.Unmarshal(c.GroundingCriteria, &rv.criteria); err != nil {
		return rv, fmt.Errorf("grounding criteria: %w", err)
	}
	err := e.Store.InWorkspace(ctx, ws, uuid.Nil, func(tx *store.Tx) error {
		recs, err := tx.Q.ListRecords(ctx, db.ListRecordsParams{WorkspaceID: tx.WS, CardID: c.ID, BoardColumn: c.BoardColumn})
		if err != nil {
			return err
		}
		var last, declared *db.Record
		var withdrawn []db.Record
		for _, r := range recs {
			col, item := review.ColumnOf(r.Kind)
			switch {
			case r.Kind == "review" && r.Round == c.Round-1:
				last = &r
			case r.Kind == review.KindWithdrawn:
				if r.Round == c.Round {
					withdrawn = append(withdrawn, r)
				}
			case r.Kind == "output":
				rv.work = append(rv.work, r)
				rv.saved = append(rv.saved, records.Body[struct{ Name string }](r).Name)
			case r.Round != c.Round:
				if r.Kind == review.KindOutputs {
					declared = &r
				}
			case item && col == c.BoardColumn:
				rv.items = append(rv.items, r)
			case r.Kind != "call" && r.Kind != "run":
				rv.work = append(rv.work, r)
			}
		}
		rv.items = latestItems(rv.items)
		// A redo that declares no outputs keeps the last attempt's list.
		if c.BoardColumn == board.Experimentation && len(rv.items) == 0 && declared != nil {
			rv.items = []db.Record{*declared}
		}
		// The previous attempt's passed and unclear items stand. Outputs are
		// checked afresh each attempt.
		var prev review.Result
		if last != nil {
			if err := json.Unmarshal(last.Body, &prev); err != nil {
				return fmt.Errorf("review %s: %w", last.Key, err)
			}
			for _, it := range prev.Items {
				if (it.Status == review.Pass || it.Status == review.Unclear) && it.Kind != review.KindOutputs {
					rv.carried = append(rv.carried, it)
				}
			}
		}
		// An item declared again keeps its earlier standing: it is not asked again.
		rv.items = slices.DeleteFunc(rv.items, func(r db.Record) bool {
			return slices.ContainsFunc(rv.carried, func(it review.Item) bool {
				return sameItem(it, r)
			})
		})
		// A failed item must be answered: declared again, it is reviewed
		// again; withdrawn, it no longer counts; left unanswered, it fails again.
		for _, it := range prev.Items {
			if it.Status != review.Fail || it.Kind == review.KindOutputs ||
				slices.ContainsFunc(rv.items, func(r db.Record) bool { return sameItem(it, r) }) {
				continue
			}
			if reason, ok := withdrawal(it, withdrawn, recs); ok {
				it.Status = review.Withdrawn
				it.Checks = []review.Check{{ID: "withdrawn", By: "agent", Detail: reason, Status: review.Withdrawn}}
			}
			rv.carried = append(rv.carried, it)
		}
		var citeKeys, runKeys []string
		for _, r := range rv.items {
			if r.Kind == review.KindHypothesis {
				for _, k := range records.Body[review.HypothesisBody](r).Cites {
					citeKeys = append(citeKeys, recordKey(k))
				}
			}
		}
		for _, r := range rv.work {
			for _, m := range runCite.FindAllStringSubmatch(string(r.Body), -1) {
				runKeys = append(runKeys, m[1])
			}
		}
		if len(citeKeys) > 0 {
			cited, err := tx.Q.LiveRecordsByKeys(ctx, db.LiveRecordsByKeysParams{WorkspaceID: tx.WS, CardID: c.ID, Keys: citeKeys})
			if err != nil {
				return err
			}
			byKey := map[string]db.Record{}
			for _, r := range cited {
				// A hypothesis rests on Grounding's sources and the sources
				// server's calls alone: the agents' own prose, an audit or a
				// review cited here would reach Jev as evidence.
				if r.Kind == "call" || review.GroundingSource(r) {
					byKey[r.Key] = r
				}
			}
			rv.cited = map[string]db.Record{}
			for _, r := range rv.items {
				for _, k := range records.Body[review.HypothesisBody](r).Cites {
					if rec, ok := byKey[recordKey(k)]; ok {
						rv.cited[k] = rec
					}
				}
			}
		}
		if len(runKeys) > 0 {
			runs, err := tx.Q.LiveRecordsByKeys(ctx, db.LiveRecordsByKeysParams{WorkspaceID: tx.WS, CardID: c.ID, Keys: runKeys})
			if err != nil {
				return err
			}
			for _, r := range runs {
				if r.Kind == "run" {
					rv.runs = append(rv.runs, r)
				}
			}
		}
		return nil
	})
	return rv, err
}

// withdrawal is the reason given by the last of withdrawn naming the item,
// by its record key or by a record declaring the same item; recs are the
// column's records the names are looked up in.
func withdrawal(it review.Item, withdrawn, recs []db.Record) (string, bool) {
	for _, w := range slices.Backward(withdrawn) {
		b := records.Body[review.WithdrawnBody](w)
		if b.Item == it.Key {
			return b.Reason, true
		}
		i := slices.IndexFunc(recs, func(r db.Record) bool { return r.Key == b.Item })
		if i >= 0 && sameItem(it, recs[i]) {
			return b.Reason, true
		}
	}
	return "", false
}

// sameItem says whether r declares the item an earlier review saw. Reviews
// written before items carried their identity match by label.
func sameItem(it review.Item, r db.Record) bool {
	if it.Kind != r.Kind {
		return false
	}
	if it.Identity == "" {
		return strings.EqualFold(it.Label, review.LabelOf(r))
	}
	return it.Identity == review.IdentityOf(r)
}

// latestItems keeps one of each item declared more than once in a round,
// matched by kind and identity: the last one declared.
func latestItems(recs []db.Record) []db.Record {
	seen := map[string]bool{}
	var out []db.Record
	for _, r := range slices.Backward(recs) {
		k := r.Kind + "\x00" + review.IdentityOf(r)
		if !seen[k] {
			seen[k] = true
			out = append(out, r)
		}
	}
	slices.Reverse(out)
	return out
}

// recordKey is the record a cite names: `call:c3` and `run:x2` name c3 and x2.
func recordKey(cite string) string {
	for _, p := range []string{"call:", "run:"} {
		if k, ok := strings.CutPrefix(cite, p); ok {
			return k
		}
	}
	return cite
}

// runReview asks the column's reviewer. It reads blobs and asks Jev and the
// sources, so no transaction is open.
func (e *Engine) runReview(ctx context.Context, ws uuid.UUID, in scoring, rv reviewing) (review.Result, error) {
	if e.Jev == nil {
		return review.Result{}, errNoJev
	}
	p := &reviewPass{e: e, ws: ws, card: in.card, retry: e.retry(), renewed: e.now()}
	a := retryAsker{p}
	switch in.card.BoardColumn {
	case board.Grounding:
		if e.Sources == nil {
			return review.Result{}, errNoSources
		}
		return review.Grounding(ctx, a, lookup{e.Sources, p}, review.GroundingInput{Question: asked(in.card), Criteria: rv.criteria,
			Items: rv.items, Carried: rv.carried, Attempt: in.attempt})
	case board.Hypotheses:
		return review.Hypotheses(ctx, a, review.HypothesesInput{Question: asked(in.card), Items: rv.items, Cited: rv.cited,
			Carried: rv.carried, Attempt: in.attempt})
	case board.Experimentation:
		runs, err := LoadRuns(ctx, e.Blobs, rv.runs)
		if err != nil {
			return review.Result{}, fmt.Errorf("reading run output: %w", err)
		}
		files, err := e.outputTexts(ctx, rv.work)
		if err != nil {
			return review.Result{}, fmt.Errorf("reading saved files: %w", err)
		}
		values := slices.DeleteFunc(valueChecks(MatchValuesIn(rv.work, runs, files), rv.work, runs, files), func(v review.ValueCheck) bool {
			return slices.ContainsFunc(rv.carried, func(it review.Item) bool {
				if it.Identity == "" {
					return it.Label == review.ValueLabel(v)
				}
				return it.Identity == review.ValueIdentity(v)
			})
		})
		return review.Experimentation(ctx, a, review.ExperimentationInput{Declared: rv.items, Saved: rv.saved,
			Values: values, Carried: rv.carried, Attempt: in.attempt})
	}
	return review.Result{}, fmt.Errorf("no item review for the %s column", in.card.BoardColumn)
}

// outputTexts reads the text of each saved file among recs, by record key.
// Files that are not text, too large or gone are left out.
func (e *Engine) outputTexts(ctx context.Context, recs []db.Record) (map[string]string, error) {
	files := map[string]string{}
	for _, r := range recs {
		if r.Kind != "output" || r.BlobKey == "" || e.Blobs == nil ||
			records.Body[struct {
				SizeBytes int64 `json:"size_bytes"`
			}](r).SizeBytes > maxOutputText {
			continue
		}
		rc, err := e.Blobs.Get(ctx, r.BlobKey)
		if errors.Is(err, storage.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		b, err := io.ReadAll(io.LimitReader(rc, maxOutputText+1))
		rc.Close()
		if err != nil {
			return nil, err
		}
		if len(b) <= maxOutputText && utf8.Valid(b) && !bytes.ContainsRune(b, 0) {
			files[r.Key] = string(b)
		}
	}
	return files, nil
}

// valueChecks are the value matches as the Experimentation reviewer reads
// them. A value whose run could not be read counts as not traced.
func valueChecks(ms []ValueMatch, work []db.Record, runs map[string]Run, files map[string]string) []review.ValueCheck {
	text := map[string]string{}
	for _, r := range work {
		text[r.Key] = records.Body[struct{ Text string }](r).Text
	}
	var out []review.ValueCheck
	for _, m := range ms {
		v := review.ValueCheck{Record: m.Record, Value: m.Value, Found: m.Matched, Sentence: text[m.Record]}
		if m.Matched {
			src := runs[m.Run].Stdout
			v.Where = "run:" + m.Run
			if m.File != "" {
				src, v.Where = files[m.File], "output:"+m.File
			}
			v.Context = around(src, m.Value)
		}
		out = append(out, v)
	}
	return out
}

// around is up to 300 bytes of out around the first number that matches
// value by the value check's rounding rule.
func around(out, value string) string {
	n, ok := parseNumber(value)
	if !ok {
		return ""
	}
	at := -1
	eachToken(out, func(start int, r *big.Rat) bool {
		if !n.integer {
			r = roundTo(r, n.places)
		}
		if r.Cmp(n.r) == 0 {
			at = start
			return false
		}
		return true
	})
	if at < 0 {
		return ""
	}
	start := max(0, at-150)
	for start < at && !utf8.RuneStart(out[start]) {
		start++
	}
	s, _ := Excerpt(out[start:], 300)
	return s
}

// leaseRenewal is how often a running review extends its scoring lease. A
// request, with the retry's waits, ends well inside one lease, so a review
// that renews before each request keeps it; a crashed one lets it run out.
const leaseRenewal = 2 * time.Minute

// reviewPass is one review's state across its requests: the one retry
// budget they share and when the lease was last extended.
type reviewPass struct {
	e       *Engine
	ws      uuid.UUID
	card    db.Card
	retry   *jev.Retry
	renewed time.Time
}

// keep extends the scoring lease once leaseRenewal has passed, while the
// study is still where it was read and still leased.
func (p *reviewPass) keep(ctx context.Context) error {
	now := p.e.now()
	if now.Sub(p.renewed) < leaseRenewal {
		return nil
	}
	p.renewed = now
	return p.e.Store.InWorkspace(ctx, p.ws, uuid.Nil, func(tx *store.Tx) error {
		c, err := lockCard(ctx, tx, p.card.ID)
		if err != nil || c.GatingUntil == nil || !current(c, p.card.BoardColumn, p.card.Round) {
			return err
		}
		until := now.Add(scoringLease)
		return tx.Q.SetGatingLease(ctx, db.SetGatingLeaseParams{GatingUntil: &until, ID: c.ID, WorkspaceID: tx.WS})
	})
}

// retryAsker asks Jev with the review's retry around each request, and says
// in its errors that Jev failed.
type retryAsker struct{ p *reviewPass }

func (a retryAsker) Noul(ctx context.Context, state string, qs map[string]string) (out map[string]float64, err error) {
	if err := a.p.keep(ctx); err != nil {
		return nil, fmt.Errorf("extending the scoring lease: %w", err)
	}
	err = a.p.retry.Do(ctx, func() (err error) {
		out, err = a.p.e.Jev.Noul(ctx, state, qs)
		return err
	})
	if err != nil {
		err = fmt.Errorf("asking Jev: %w", err)
	}
	return out, err
}

func (a retryAsker) Choice(ctx context.Context, state, instructions string, options map[string]string) (out jev.Answer, err error) {
	if err := a.p.keep(ctx); err != nil {
		return jev.Answer{}, fmt.Errorf("extending the scoring lease: %w", err)
	}
	err = a.p.retry.Do(ctx, func() (err error) {
		out, err = a.p.e.Jev.Choice(ctx, state, instructions, options)
		return err
	})
	if err != nil {
		err = fmt.Errorf("asking Jev: %w", err)
	}
	return out, err
}

// lookup says in its errors that the sources lookup failed; not-found
// answers stay recognisable. It keeps the review's lease as it goes.
type lookup struct {
	s review.Sources
	p *reviewPass
}

func (l lookup) Paper(ctx context.Context, id string) (sources.Hit, error) {
	if err := l.p.keep(ctx); err != nil {
		return sources.Hit{}, fmt.Errorf("extending the scoring lease: %w", err)
	}
	h, err := l.s.Paper(ctx, id)
	return h, lookupErr(err)
}

func (l lookup) Dataset(ctx context.Context, accession string) (sources.Hit, error) {
	if err := l.p.keep(ctx); err != nil {
		return sources.Hit{}, fmt.Errorf("extending the scoring lease: %w", err)
	}
	h, err := l.s.Dataset(ctx, accession)
	return h, lookupErr(err)
}

func (l lookup) Search(ctx context.Context, query string) ([]sources.Hit, error) {
	if err := l.p.keep(ctx); err != nil {
		return nil, fmt.Errorf("extending the scoring lease: %w", err)
	}
	hs, err := l.s.Search(ctx, query)
	return hs, lookupErr(err)
}

func lookupErr(err error) error {
	if err == nil || errors.Is(err, sources.ErrNotFound) {
		return err
	}
	return fmt.Errorf("sources lookup: %w", err)
}

// applyReview writes the review and its outcome and acts on the decision, as
// applyScore does: a study that left the column, or a column decided
// meanwhile, is left alone. It says whether a column now waits to start.
func (e *Engine) applyReview(ctx context.Context, ws uuid.UUID, in scoring, res review.Result, rerr error) (bool, error) {
	moved := false
	err := e.Store.InWorkspace(ctx, ws, uuid.Nil, func(tx *store.Tx) error {
		c, err := lockCard(ctx, tx, in.card.ID)
		if err != nil || c.ID == uuid.Nil {
			return err
		}
		if err := tx.Q.SetGatingLease(ctx, db.SetGatingLeaseParams{ID: c.ID, WorkspaceID: tx.WS}); err != nil {
			return err
		}
		if !current(c, in.card.BoardColumn, in.card.Round) {
			return nil
		}
		if n, err := tx.Q.CountRoundKindRecords(ctx, db.CountRoundKindRecordsParams{WorkspaceID: tx.WS, CardID: c.ID,
			BoardColumn: c.BoardColumn, Round: c.Round, Kind: "outcome"}); err != nil || n > 0 {
			return err
		}
		if rerr != nil {
			return scoringUnavailable(ctx, tx, c, rerr)
		}
		if err := resolveInbox(ctx, tx, c.ID, "scoring_unavailable"); err != nil {
			return err
		}
		res.Decision = review.Decide(res, c.Redos)
		if err := writeServer(ctx, tx, c, "review", res); err != nil {
			return err
		}
		if err := writeServer(ctx, tx, c, "outcome", map[string]any{"verdict": res.Decision, "attempt": in.attempt}); err != nil {
			return err
		}
		var items []board.Item
		next := db.SaveCardFlowParams{ID: c.ID, WorkspaceID: tx.WS, BoardColumn: c.BoardColumn, State: c.State,
			BlockKind: c.BlockKind, Round: c.Round, Redos: c.Redos}
		switch res.Decision {
		case review.DecideRedo:
			next.Redos, next.Round = 1, c.Round+1
			next.HandoffNote = review.RedoNote(res)
		case review.DecideBlock:
			e.drawRatingSafely(ctx, tx, c)
			next.State, next.BlockKind = "blocked", "low_score"
			items = append(items, board.Item{Kind: "blocked", Text: reviewBlockedText(c.Number, c.BoardColumn, failedLabels(res))})
		default:
			e.drawRatingSafely(ctx, tx, c)
			if i := board.StageIndex(c.BoardColumn); i < board.StageIndex(c.ScopeTo) {
				next.BoardColumn, next.Round, next.Redos = board.Stages[i+1], c.Round+1, 0
				if res.Decision == review.DecideCarry {
					next.HandoffNote = review.CarryNote(res)
				}
			} else {
				next.State = "done"
				items = append(items, board.Item{Kind: "done", Text: doneText(c.Number, c.BoardColumn)})
			}
		}
		if err := tx.Q.SaveCardFlow(ctx, next); err != nil {
			return err
		}
		for _, it := range items {
			if err := tx.Q.InsertInboxItem(ctx, db.InsertInboxItemParams{WorkspaceID: tx.WS, CardID: c.ID, UserID: c.OwnerID, Kind: it.Kind, Text: it.Text}); err != nil {
				return err
			}
		}
		moved = next.State == board.Working
		return tx.Emit(ctx, "card.steered", &c.ID, store.System, map[string]any{"column": next.BoardColumn, "state": next.State, "verdict": res.Decision})
	})
	return moved, err
}

// failedLabels lists the labels of the items that failed, missed papers
// among them.
func failedLabels(r review.Result) string {
	var out []string
	for _, it := range append(append([]review.Item{}, r.Items...), r.Missed...) {
		if it.Status == review.Fail {
			out = append(out, it.Label)
		}
	}
	return strings.Join(out, ", ")
}
