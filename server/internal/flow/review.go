package flow

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
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
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/board"
	"lykeion/server/internal/handin"
	"lykeion/server/internal/jev"
	"lykeion/server/internal/records"
	"lykeion/server/internal/review"
	"lykeion/server/internal/sources"
	"lykeion/server/internal/storage"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

var (
	errNoSources      = errors.New("the server has no sources lookup")
	errSourceLookup   = errors.New("source lookup unavailable")
	errRecallLookup   = errors.New("recall search unavailable")
	errJevUnavailable = errors.New("Jev judgment unavailable")
)

// maxOutputText is the largest saved file read as text for the value check;
// larger files are left out. ponytail: whole-file read, stream the tokens if
// big text outputs must count.
const maxOutputText = 8 << 20

// reviewBlockedText is the Inbox item for a column that failed its review
// again after its redo; reasons are the lines review.Reasons gave.
func reviewBlockedText(number int32, column string, reasons []string) string {
	if len(reasons) == 0 {
		reasons = []string{"The review found problems with this column."}
	}
	return fmt.Sprintf("Study #%d's %s column failed review again after its redo. Still failing:\n- %s\nComment on the study to tell the Team what to fix.", number, title(column), strings.Join(reasons, "\n- "))
}

// usesReview says whether the study's current column is reviewed item by
// item, unless the column was already under way when item review shipped.
func usesReview(c db.Card) bool {
	return c.BoardColumn != c.LegacyColumn
}

// reviewing is what one pass read to review a column's items, with no
// transaction open. The auditor's findings are never part of it: Jev reviews
// the items and their evidence alone.
type reviewing struct {
	items    []db.Record            // this round's item records
	carried  []review.Item          // the last attempt's items that stand, withdrawn or failed unanswered
	cited    map[string]db.Record   // what the hypotheses cite, by the cite as written
	saved    []string               // names of the files saved in this column
	work     []db.Record            // this round's records that may report values, and the column's outputs
	runs     []db.Record            // the run records the work cites
	comments []string               // the researchers' comments on the study, oldest first
	declined map[string]string      // missed papers the Team declined this round, by normalised id, with its reason
	sources  map[string][]db.Record // the calls holding each source Validation's claims cite
}

// reviewColumn reviews the study's current column item by item, writes the
// review and its outcome, and moves the study on, carries its unclear items
// forward, redoes the failed items or blocks it. Jev and the sources are
// asked with no transaction open; when either cannot answer, the study stays
// where it is, as with scoring.
func (e *Engine) reviewColumn(ctx context.Context, ws uuid.UUID, in scoring) error {
	if in.card.BoardColumn == board.Grounding && !in.recheckStanding {
		// A keyless approval certifies current source checks for standing
		// papers too. Reading the owner’s key locally does not ask Jev.
		_, keyErr := e.ownerJev(ctx, in.card.OwnerID)
		in.recheckStanding = errors.Is(keyErr, errNoJev)
	}
	res, run, err := e.deterministicReview(ctx, ws, in)
	if err == nil {
		in.checkedAt = e.now()
	}
	if res.Column != "" {
		persistErr := e.persistChecks(ctx, ws, in, res)
		if errors.Is(persistErr, ErrNotWaiting) {
			return nil
		}
		err = errors.Join(err, persistErr)
	}
	if err == nil && (res.NeedsJudgment || res.Recall != nil && len(res.Recall.Unscreened) != 0) {
		j, jerr := e.ownerJev(ctx, in.card.OwnerID)
		if jerr == nil {
			judged, jerr := run(retryAsker{&reviewPass{e: e, ws: ws, card: in.card, jev: j, retry: e.retry(), renewed: e.now()}})
			if jerr == nil {
				res = judged
			}
			err = jerr
		} else {
			err = jerr
		}
		if err != nil {
			res.Judgment = "unavailable"
			if errors.Is(err, errNoJev) && in.card.BoardColumn == board.Grounding && !in.recheckStanding &&
				slices.ContainsFunc(res.Items, func(it review.Item) bool {
					return it.Carried && (it.Status == review.Pass || it.Status == review.Unclear)
				}) {
				// The key was removed while checking new sources. Finish the
				// standing checks before opening an owner decision.
				in.recheckStanding = true
				return e.reviewColumn(ctx, ws, in)
			}
			if errors.Is(err, errNoJev) && !res.NeedsJudgment {
				// Unscreened recall alone never holds a keyless column.
				err = nil
			}
		}
	}
	markReviewFailed(ctx, err)
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
	return e.readReviewInputs(ctx, ws, c, false)
}

func (e *Engine) readReviewInputs(ctx context.Context, ws uuid.UUID, c db.Card, recheck bool) (reviewing, error) {
	var rv reviewing
	err := e.Store.InWorkspace(ctx, ws, uuid.Nil, func(tx *store.Tx) error {
		recs, err := tx.Q.ListRecords(ctx, db.ListRecordsParams{WorkspaceID: tx.WS, CardID: c.ID, BoardColumn: c.BoardColumn})
		if err != nil {
			return err
		}
		cs, err := tx.Q.ListComments(ctx, db.ListCommentsParams{WorkspaceID: tx.WS, CardID: c.ID})
		if err != nil {
			return err
		}
		for _, cm := range cs {
			rv.comments = append(rv.comments, strings.ReplaceAll(cm.Body, "\n", " "))
		}
		var declared *db.Record
		var withdrawn []db.Record
		for _, r := range recs {
			col, item := review.ColumnOf(r.Kind)
			switch {
			case r.Kind == "server_checks" && r.Round == c.Round:
				refresh := records.Body[struct {
					ApprovalRefresh bool `json:"approval_refresh"`
				}](r)
				recheck = recheck || refresh.ApprovalRefresh
			case r.Kind == "review" && r.Round == c.Round-1: // read by lastReview
			case r.Kind == review.KindWithdrawn:
				if r.Round == c.Round {
					withdrawn = append(withdrawn, r)
				}
			case r.Kind == "declined":
				if r.Round == c.Round {
					d := records.Body[struct{ Item, Reason string }](r)
					if rv.declined == nil {
						rv.declined = map[string]string{}
					}
					rv.declined[handin.NormID(d.Item)] = d.Reason
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
			case !bookkeeping(r.Kind):
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
		prev, err := lastReview(recs, c.Round)
		if err != nil {
			return err
		}
		attempts := map[uuid.UUID]int32{}
		if slices.ContainsFunc(prev.Items, func(it review.Item) bool { return it.Kind == "value" && it.Status == review.Fail }) {
			for _, r := range recs {
				if r.Kind != "hand_in" || r.Round != c.Round || !handin.Leads(r.Role) || r.AgentRunID == nil {
					continue
				}
				if _, ok := attempts[*r.AgentRunID]; ok {
					continue
				}
				run, err := tx.Q.GetAgentRun(ctx, db.GetAgentRunParams{WorkspaceID: tx.WS, ID: *r.AgentRunID})
				if errors.Is(err, pgx.ErrNoRows) {
					continue
				}
				if err != nil {
					return err
				}
				attempts[run.ID] = run.Attempt
			}
		}
		for _, it := range prev.Items {
			if it.Kind == review.KindOutputs || it.Status != review.Pass && it.Status != review.Unclear {
				continue
			}
			if (it.Unjudged || recheck) && it.Key != "" {
				// A deterministic pass still needs judgment. Recheck that
				// input on a redo, or any standing input on owner approval.
				if rv.addStandingInput(recs, it) {
					continue
				}
				it.Status = review.Fail
				it.Checks = append(slices.Clone(it.Checks), review.Check{ID: "record_current", By: "server", Status: review.Fail, Detail: "the reviewed record is no longer current"})
			}
			rv.carried = append(rv.carried, it)
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
				slices.ContainsFunc(rv.items, func(r db.Record) bool { return sameItem(it, r) }) ||
				(it.Kind == "value" && correctedValue(it, prev, recs, rv.work, c, attempts)) {
				continue
			}
			if reason, ok := withdrawal(it, withdrawn, recs); ok {
				it.Status = review.Withdrawn
				it.Checks = []review.Check{{ID: "withdrawn", By: "agent", Detail: reason, Status: review.Withdrawn}}
			}
			rv.carried = append(rv.carried, it)
		}

		if c.BoardColumn == board.Validation {
			if rv.sources, err = claimSources(ctx, tx, c.ID, rv.items); err != nil {
				return err
			}
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
				if r.Kind == "call" && records.Body[struct{ Error string }](r).Error == "" || review.GroundingSource(r) {
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

func (rv *reviewing) addStandingInput(recs []db.Record, item review.Item) bool {
	if item.Kind == "value" && slices.ContainsFunc(rv.work, func(r db.Record) bool { return r.Key == item.Key }) ||
		item.Kind != "value" && slices.ContainsFunc(rv.items, func(r db.Record) bool { return sameItem(item, r) }) {
		return true
	}
	i := slices.IndexFunc(recs, func(r db.Record) bool { return r.Key == item.Key })
	if i < 0 {
		return false
	}
	if item.Kind == "value" {
		rv.work = append(rv.work, recs[i])
	} else {
		rv.items = append(rv.items, recs[i])
	}
	return true
}

// correctedValue accepts only a lead's recorded hand-in explicitly answering
// the failed value and re-declaring it in that same run.
func correctedValue(item review.Item, prev review.Result, recs, work []db.Record, c db.Card, attempts map[uuid.UUID]int32) bool {
	norm := func(s string) string { return strings.Join(strings.Fields(handin.NormID(s)), " ") }
	aliases := map[string]string{}
	for _, it := range prev.Items {
		if it.Status != review.Fail || it.Kind == review.KindOutputs {
			continue
		}
		ids := []string{it.Identity}
		if it.Identity == "" {
			ids = []string{it.Label}
		}
		if it.Kind == "value" {
			ids = append(ids, it.Label)
			if i := slices.IndexFunc(recs, func(r db.Record) bool { return r.Key == it.Key }); i >= 0 {
				ids = append(ids, valueOf(recs[i]).Name)
			}
		}
		if needsRetryAlias(it) {
			ids = append(ids, retryAlias(c, c.Round-1, it))
		}
		for _, id := range ids {
			id = norm(id)
			if id == "" {
				continue
			}
			if old, ok := aliases[id]; ok && old != it.Key {
				aliases[id] = ""
			} else if !ok {
				aliases[id] = it.Key
			}
		}
	}
	i := slices.IndexFunc(recs, func(r db.Record) bool { return r.Key == item.Key && r.Kind == "value" })
	if i < 0 || norm(valueOf(recs[i]).Name) == "" {
		return false
	}
	name := norm(valueOf(recs[i]).Name)
	for _, r := range recs {
		if r.Kind != "hand_in" || r.Round != c.Round || !handin.Leads(r.Role) || r.AgentRunID == nil {
			continue
		}
		var body struct {
			HandIn           handin.HandIn `json:"hand_in"`
			Attempt          int32         `json:"attempt"`
			ConvertedRecords []uuid.UUID   `json:"converted_records"`
		}
		attempt, ok := attempts[*r.AgentRunID]
		if !ok || json.Unmarshal(r.Body, &body) != nil || body.Attempt != attempt || body.HandIn.Status != "done" {
			continue
		}
		answered := slices.ContainsFunc(body.HandIn.Replies, func(reply handin.Reply) bool {
			return reply.Action == "corrected" && aliases[norm(reply.Item)] == item.Key
		})
		if !answered {
			continue
		}
		for _, v := range body.HandIn.Values {
			if norm(v.Name) != name {
				continue
			}
			if slices.ContainsFunc(work, func(w db.Record) bool {
				return w.Kind == "value" && w.Round == c.Round && w.AgentRunID != nil &&
					*w.AgentRunID == *r.AgentRunID && slices.Contains(body.ConvertedRecords, w.ID) &&
					norm(valueOf(w).Name) == name && valueOf(w).Value == v.Value
			}) {
				return true
			}
		}
	}
	return false
}

// lastReview is the review that ended the round before round, among a
// column's records; the zero Result when there is none.
func lastReview(recs []db.Record, round int32) (review.Result, error) {
	var prev review.Result
	for _, r := range slices.Backward(recs) {
		if r.Kind == "review" && r.Round == round-1 {
			if err := json.Unmarshal(r.Body, &prev); err != nil {
				return prev, fmt.Errorf("review %s: %w", r.Key, err)
			}
			break
		}
	}
	return prev, nil
}

// SentBack is what the review before the study's round sent back in its
// column: the failed items a reply may answer, by identity, and the papers
// it found missing. Outputs are checked afresh each attempt, so they are
// never sent back to answer.
func (e *Engine) SentBack(ctx context.Context, tx *store.Tx, c db.Card) (handin.Review, error) {
	out := handin.Review{Failed: map[string]string{}, Aliases: map[string]string{}}
	recs, err := tx.Q.ListRecords(ctx, db.ListRecordsParams{WorkspaceID: tx.WS, CardID: c.ID, BoardColumn: c.BoardColumn})
	if err != nil {
		return out, err
	}
	prev, err := lastReview(recs, c.Round)
	if err != nil {
		return out, err
	}
	addFailed := func(id, key string) {
		if old, exists := out.Failed[id]; exists && old != key {
			out.Failed[id] = ""
		} else if !exists {
			out.Failed[id] = key
		}
	}
	for _, it := range prev.Items {
		if it.Status == review.Fail && it.Kind != review.KindOutputs {
			id := it.Identity
			if id == "" {
				id = it.Label // reviews written before items carried their identity
			}
			addFailed(id, it.Key)
			// A reply names a value by the name it was handed in with.
			if it.Kind == "value" {
				if i := slices.IndexFunc(recs, func(r db.Record) bool { return r.Key == it.Key }); i >= 0 {
					if name := valueOf(recs[i]).Name; name != "" {
						addFailed(name, it.Key)
					}
				}
				addFailed(it.Label, it.Key)
			}
		}
	}
	for _, it := range prev.Items {
		if it.Status == review.Fail && needsRetryAlias(it) {
			alias := retryAlias(c, c.Round-1, it)
			addFailed(alias, it.Key)
			if old, exists := out.Aliases[alias]; exists && old != it.Identity {
				out.Aliases[alias] = ""
			} else if !exists {
				out.Aliases[alias] = it.Identity
			}
		}
	}
	for _, it := range prev.Missed {
		out.Missed = append(out.Missed, it.Label)
	}
	return out, nil
}

func needsRetryAlias(it review.Item) bool {
	return it.Kind != review.KindOutputs && utf8.RuneCountInString(it.Identity) > 2000
}

func retryAlias(c db.Card, round int32, it review.Item) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%d\x00%s\x00%s", c.ID, c.BoardColumn, round, it.Key, it.Identity)))
	return fmt.Sprintf("retry-ref-%x", sum[:16])
}

// redoNote names failed items by identities accepted by the reply converter.
func redoNote(ctx context.Context, tx *store.Tx, c db.Card, res review.Result) (string, error) {
	recs, err := tx.Q.ListRecords(ctx, db.ListRecordsParams{WorkspaceID: tx.WS, CardID: c.ID, BoardColumn: c.BoardColumn})
	if err != nil {
		return "", err
	}
	res.Items = slices.Clone(res.Items)
	for i, it := range res.Items {
		if it.Status != review.Fail || it.Kind == review.KindOutputs {
			continue
		}
		if it.Identity != "" {
			res.Items[i].Label = oneLine(it.Identity)
		}
		if needsRetryAlias(it) {
			res.Items[i].Label = retryAlias(c, c.Round, it)
		}
		if it.Kind == review.KindHypothesis && !needsRetryAlias(it) {
			if j := slices.IndexFunc(recs, func(r db.Record) bool { return r.Key == it.Key }); j >= 0 {
				res.Items[i].Label = oneLine(records.Body[review.HypothesisBody](recs[j]).Text)
			}
		}
	}
	return review.RedoNote(res), nil
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

// prepareReview pins the inputs used by both deterministic checks and judgment.
func (e *Engine) prepareReview(ctx context.Context, ws uuid.UUID, in scoring, rv reviewing) (func(review.Asker) (review.Result, error), error) {
	switch in.card.BoardColumn {
	case board.Grounding:
		if e.Sources == nil {
			return nil, errNoSources
		}
		p := &reviewPass{e: e, ws: ws, card: in.card, renewed: e.now()}
		src := &reviewSources{lookup: lookup{e.Sources, p}}
		return func(a review.Asker) (review.Result, error) {
			return review.Grounding(ctx, a, src, review.GroundingInput{Question: asked(in.card),
				Comments: rv.comments, Declined: rv.declined, Items: rv.items, Carried: rv.carried, Attempt: in.attempt})
		}, nil
	case board.Hypotheses:
		return func(a review.Asker) (review.Result, error) {
			return review.Hypotheses(ctx, a, review.HypothesesInput{Question: asked(in.card), Items: rv.items, Cited: rv.cited,
				Carried: rv.carried, Attempt: in.attempt})
		}, nil
	case board.Experimentation:
		runs, err := LoadRuns(ctx, e.Blobs, rv.runs)
		if err != nil {
			return nil, fmt.Errorf("reading run output: %w", err)
		}
		files, err := e.outputTexts(ctx, rv.work)
		if err != nil {
			return nil, fmt.Errorf("reading saved files: %w", err)
		}
		values := slices.DeleteFunc(valueChecks(MatchValuesIn(rv.work, runs, files), rv.work, runs, files), func(v review.ValueCheck) bool {
			return slices.ContainsFunc(rv.carried, func(it review.Item) bool {
				if it.Identity == "" {
					return it.Label == review.ValueLabel(v)
				}
				return it.Identity == review.ValueIdentity(v)
			})
		})
		return func(a review.Asker) (review.Result, error) {
			return review.Experimentation(ctx, a, review.ExperimentationInput{Declared: rv.items, Saved: rv.saved,
				Values: values, Carried: rv.carried, Attempt: in.attempt})
		}, nil
	case board.Validation:
		texts, err := e.sourceTexts(ctx, rv.sources)
		if err != nil {
			return nil, fmt.Errorf("reading sources: %w", err)
		}
		return func(a review.Asker) (review.Result, error) {
			return review.Claims(ctx, a, review.ClaimsInput{Items: rv.items, Sources: texts, Carried: rv.carried, Attempt: int32(in.attempt)})
		}, nil
	}
	return nil, fmt.Errorf("no item review for the %s column", in.card.BoardColumn)
}

// One live lookup per reference, reused when judging the checked
// snapshot. A recall result must not change between checks and judgment.
type lookupResult[T any] struct {
	value T
	err   error
}

type reviewSources struct {
	lookup
	papers, datasets map[string]lookupResult[sources.Hit]
	searches         map[string]lookupResult[[]sources.Hit]
}

func cachedLookup[T any](cache *map[string]lookupResult[T], key string, fetch func() (T, error)) (T, error) {
	if *cache == nil {
		*cache = make(map[string]lookupResult[T])
	}
	r, ok := (*cache)[key]
	if !ok {
		r.value, r.err = fetch()
		(*cache)[key] = r
	}
	return r.value, r.err
}

func (s *reviewSources) Paper(ctx context.Context, id string) (sources.Hit, error) {
	return cachedLookup(&s.papers, id, func() (sources.Hit, error) { return s.lookup.Paper(ctx, id) })
}
func (s *reviewSources) Dataset(ctx context.Context, id string) (sources.Hit, error) {
	return cachedLookup(&s.datasets, id, func() (sources.Hit, error) { return s.lookup.Dataset(ctx, id) })
}
func (s *reviewSources) Search(ctx context.Context, query string) ([]sources.Hit, error) {
	return cachedLookup(&s.searches, query, func() ([]sources.Hit, error) { return s.lookup.Search(ctx, query) })
}

func (e *Engine) deterministicReview(ctx context.Context, ws uuid.UUID, in scoring) (review.Result, func(review.Asker) (review.Result, error), error) {
	rv, err := e.readReviewInputs(ctx, ws, in.card, in.recheck || in.recheckStanding)
	if err != nil {
		return review.Result{}, nil, err
	}
	run, err := e.prepareReview(ctx, ws, in, rv)
	if err != nil {
		return review.Result{}, nil, err
	}
	res, err := run(nil)
	return res, run, err
}

func (e *Engine) persistChecks(ctx context.Context, ws uuid.UUID, in scoring, body any) error {
	return e.Store.InWorkspace(ctx, ws, uuid.Nil, func(tx *store.Tx) error {
		c, err := lockCard(ctx, tx, in.card.ID)
		if err != nil {
			return err
		}
		if c.Version != in.card.Version || !in.isCurrent(c) {
			return ErrNotWaiting
		}
		return writeServerChecks(ctx, tx, c, body)
	})
}

// outputTexts reads the text of each saved file among recs, by record key.
// Files that are not text, too large or gone are left out.
func (e *Engine) outputTexts(ctx context.Context, recs []db.Record) (map[string]string, error) {
	files := map[string]string{}
	for _, r := range recs {
		if r.Kind != "output" && r.Kind != "rerun_output" || r.BlobKey == "" || e.Blobs == nil ||
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
		// A handed-in value has a name, not a sentence.
		b := records.Body[struct{ Text, Name string }](r)
		text[r.Key] = cmp.Or(b.Text, b.Name)
	}
	var out []review.ValueCheck
	for _, m := range ms {
		v := review.ValueCheck{Record: m.Record, Value: m.Value, Found: m.Matched, Sentence: text[m.Record], Textual: !numberText.MatchString(m.Value)}
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

// around is up to 300 bytes around the first matching number or textual outcome.
func around(out, value string) string {
	n, ok := parseNumber(value)
	at := -1
	if ok {
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
	} else if value != "" && utf8.RuneCountInString(value) <= 2000 {
		at = wordIndex(out, value)
	}
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
	jev     *jev.Client // the study owner's; nil asks every question errNoJev
	retry   *jev.Retry
	renewed time.Time
}

// keep extends the scoring lease once leaseRenewal has passed, while the
// study is still where it was read and still leased.
func (p *reviewPass) keep(ctx context.Context) error {
	if p.card.ID == uuid.Nil {
		return nil // a pass that reviews no study holds no lease
	}
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
	if a.p.jev == nil {
		return nil, errNoJev
	}
	if err := a.p.keep(ctx); err != nil {
		return nil, fmt.Errorf("extending the scoring lease: %w", err)
	}
	err = a.p.retry.Do(ctx, func() (err error) {
		out, err = a.p.jev.Noul(ctx, state, qs)
		return err
	})
	if err != nil {
		err = fmt.Errorf("%w: %w", errJevUnavailable, err)
	}
	return out, err
}

// NoulImages is Noul with images; only a report's figure checks show Jev images.
func (a retryAsker) NoulImages(ctx context.Context, state string, qs map[string]string, images map[string]jev.Image) (out map[string]float64, err error) {
	if a.p.jev == nil {
		return nil, errNoJev
	}
	if err := a.p.keep(ctx); err != nil {
		return nil, fmt.Errorf("extending the scoring lease: %w", err)
	}
	err = a.p.retry.Do(ctx, func() (err error) {
		out, err = a.p.jev.NoulImages(ctx, state, qs, images)
		return err
	})
	if err != nil {
		err = fmt.Errorf("%w: %w", errJevUnavailable, err)
	}
	return out, err
}

func (a retryAsker) Choice(ctx context.Context, state, instructions string, options map[string]string) (out jev.Answer, err error) {
	if a.p.jev == nil {
		return jev.Answer{}, errNoJev
	}
	if err := a.p.keep(ctx); err != nil {
		return jev.Answer{}, fmt.Errorf("extending the scoring lease: %w", err)
	}
	err = a.p.retry.Do(ctx, func() (err error) {
		out, err = a.p.jev.Choice(ctx, state, instructions, options)
		return err
	})
	if err != nil {
		err = fmt.Errorf("%w: %w", errJevUnavailable, err)
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
	return h, lookupErr(errSourceLookup, err)
}

func (l lookup) Dataset(ctx context.Context, accession string) (sources.Hit, error) {
	if err := l.p.keep(ctx); err != nil {
		return sources.Hit{}, fmt.Errorf("extending the scoring lease: %w", err)
	}
	h, err := l.s.Dataset(ctx, accession)
	return h, lookupErr(errSourceLookup, err)
}

func (l lookup) Search(ctx context.Context, query string) ([]sources.Hit, error) {
	if err := l.p.keep(ctx); err != nil {
		return nil, fmt.Errorf("extending the scoring lease: %w", err)
	}
	hs, err := l.s.Search(ctx, query)
	return hs, lookupErr(errRecallLookup, err)
}

func lookupErr(kind, err error) error {
	if err == nil || errors.Is(err, sources.ErrNotFound) {
		return err
	}
	return fmt.Errorf("%w: %w", kind, err)
}

// applyReview writes the review and its outcome and acts on the decision, as
// applyScore does: a study that left the column, or a column decided
// meanwhile, is left alone. It says whether a column now waits to start.
func (e *Engine) applyReview(ctx context.Context, ws uuid.UUID, in scoring, res review.Result, rerr error) (bool, error) {
	moved, finished := false, false
	err := e.Store.InWorkspace(ctx, ws, uuid.Nil, func(tx *store.Tx) error {
		c, err := lockCard(ctx, tx, in.card.ID)
		if err != nil || c.ID == uuid.Nil {
			return err
		}
		if c.Version != in.card.Version {
			return nil
		}
		if err := tx.Q.SetGatingLease(ctx, db.SetGatingLeaseParams{ID: c.ID, WorkspaceID: tx.WS}); err != nil {
			return err
		}
		if !in.isCurrent(c) {
			return nil
		}
		if pending, err := e.deferScoringForComments(ctx, tx, c); err != nil || pending {
			return err
		}
		if n, err := tx.Q.CountRoundKindRecords(ctx, db.CountRoundKindRecordsParams{WorkspaceID: tx.WS, CardID: c.ID,
			BoardColumn: c.BoardColumn, Round: c.Round, Kind: "outcome"}); err != nil || n > 0 {
			return err
		}
		if in.recheck {
			if err := in.checkApprovalEvidence(ctx, tx); err != nil {
				return err
			}
			if err := dismissKeylessApproval(ctx, tx, c.ID, in.decision); err != nil {
				return err
			}
		}
		if rerr != nil && !(errors.Is(rerr, errNoJev) && stopsBeforeApproval(res)) {
			return e.gateUnjudged(ctx, tx, c, in, rerr, checkSummary(res), res)
		}
		if err := resolveInbox(ctx, tx, c.ID, "scoring_unavailable"); err != nil {
			return err
		}
		res.Decision = review.Decide(res, c.Redos)
		if review.AllWithdrawn(res) {
			res.Decision = review.DecideAsk
		}
		if err := writeServer(ctx, tx, c, "review", res); err != nil {
			return err
		}
		if err := writeServer(ctx, tx, c, "outcome", map[string]any{"verdict": res.Decision, "attempt": in.attempt}); err != nil {
			return err
		}
		next := db.SaveCardFlowParams{ID: c.ID, WorkspaceID: tx.WS, BoardColumn: c.BoardColumn, State: c.State,
			BlockKind: c.BlockKind, Round: c.Round, Redos: c.Redos}
		switch {
		case res.Decision == review.DecideAsk:
			// Nothing is left to review or redo: the owner decides, and the
			// redo count stays.
			next.State = "blocked"
			if err := holdForDecision(ctx, tx, c, "needs_decision", "all_withdrawn", allWithdrawnText(c.BoardColumn, review.WithdrawalReasons(res)), e.now()); err != nil {
				return err
			}
			if err := tx.Emit(ctx, "inbox.created", &c.ID, store.System, map[string]any{"card_id": c.ID}); err != nil {
				return err
			}
		case res.Decision == review.DecideRedo:
			next.State, next.BlockKind = board.Working, ""
			next.Redos, next.Round = 1, c.Round+1
			if next.HandoffNote, err = redoNote(ctx, tx, c, res); err != nil {
				return err
			}
		case res.Decision == review.DecideBlock:
			next.State, next.BlockKind = "blocked", "low_score"
			if err := holdForDecision(ctx, tx, c, "blocked", "low_score", reviewBlockedText(c.Number, c.BoardColumn, review.Reasons(res)), e.now()); err != nil {
				return err
			}
		default:
			if finished = advance(c, &next); !finished && res.Decision == review.DecideCarry {
				next.HandoffNote = review.CarryNote(res)
			}
		}
		if err := tx.Q.SaveCardFlow(ctx, next); err != nil {
			return err
		}
		if finished {
			if err := e.finishStudy(ctx, tx, c); err != nil {
				return err
			}
		}
		moved = next.State == board.Working
		return tx.Emit(ctx, "card.steered", &c.ID, store.System, map[string]any{"column": next.BoardColumn, "state": next.State, "verdict": res.Decision})
	})
	if err == nil && finished {
		e.goReport("check report", ws, in.card.ID, e.checkReport)
	}
	return moved, err
}

func stopsBeforeApproval(r review.Result) bool {
	d := review.Decide(r, 0)
	return d == review.DecideRedo || review.AllWithdrawn(r)
}

func checkSummary(r review.Result) string {
	switch r.Column {
	case board.Grounding:
		text := "Source existence and retraction checks ran on the submitted items."
		if r.Recall != nil {
			text += fmt.Sprintf(" Recall search ran. %d candidate(s) were not screened.", len(r.Recall.Unscreened))
		}
		return text
	case board.Hypotheses:
		return "Citation provenance and control declarations were checked."
	case board.Experimentation:
		return "Declared output names and value traces were checked."
	case board.Validation:
		return "Quotes were checked against the study's recorded sources."
	}
	return "Server checks could not be completed."
}

// Do not append identical evidence on every retry while a provider or saved
// key remains unavailable. A changed snapshot remains part of the history.
func writeServerChecks(ctx context.Context, tx *store.Tx, c db.Card, body any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	same, err := tx.Q.SameServerChecks(ctx, db.SameServerChecksParams{
		WorkspaceID: tx.WS, CardID: c.ID, BoardColumn: c.BoardColumn, Round: c.Round, Snapshot: raw,
	})
	if err != nil || same {
		return err
	}
	return writeServer(ctx, tx, c, "server_checks", json.RawMessage(raw))
}
