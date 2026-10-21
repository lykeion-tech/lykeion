package flow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/board"
	"lykeion/server/internal/fence"
	"lykeion/server/internal/jev"
	"lykeion/server/internal/pack"
	"lykeion/server/internal/records"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

// coreCriteria are asked of every column, word for word and in this order.
var coreCriteria = []string{
	"Does the work cite where its facts came from?",
	"Does it answer what the researcher asked?",
	"Does it say what it couldn't do?",
}

// scoreLevels are the five levels of each criterion, low to high; Jev's
// score s is level s+1.
var scoreLevels = []string{"Not at all", "Barely", "Partly", "Mostly", "Fully"}

// maxJevInput is Jev's limit for the state and the questions together: 32k
// tokens, estimated at 3 bytes a token, since escaped JSON packs more tokens
// per byte than prose. Jev refusing a state as too long halves the budget,
// down to minJevInput.
const (
	maxJevInput = 32 << 10 * 3
	minJevInput = 8 << 10
)

// scoringLease keeps other passes from scoring a column while one asks Jev:
// longer than the retry's waits plus three timed-out requests. A crashed pass
// lets go when it runs out.
const scoringLease = 10 * time.Minute

var errNoJev = errors.New("the lab has no TypeSafe key")

// The Inbox sentences below are read back by the dashboard (inboxText in
// lib/studies.ts), which lifts the quoted criterion out of a middle score;
// the other kinds it words itself. Change them together.
func weakCriterion(text string, level float64) string {
	return fmt.Sprintf("“%s” (level %s of 5)", text, strconv.FormatFloat(level, 'g', 3, 64))
}

// middleScoreText says whether the study went on to the next column or
// finished; the dashboard tells the two apart by "and the study finished".
func middleScoreText(number int32, column, weak string, finished bool) string {
	end := ""
	if finished {
		end = " and the study finished"
	}
	return fmt.Sprintf("Study #%d's %s column scored in the middle%s; its weakest criterion was %s.", number, title(column), end, weak)
}

func blockedText(number int32, column, weak string) string {
	return fmt.Sprintf("Study #%d scored low twice in its %s column, lowest on %s. Comment on the study to tell the Team what to fix.", number, title(column), weak)
}

func doneText(number int32, column string) string {
	return fmt.Sprintf("Study #%d is done, in its %s column.", number, title(column))
}

func unavailableText(number int32, column string) string {
	return fmt.Sprintf("Scoring is unavailable for study #%d's %s column. It stays there, and Lykeion keeps retrying.", number, title(column))
}

// Verdict reads a column's criterion levels: high when every level is 4 or
// more, low when any is 2 or less, middle otherwise.
func Verdict(levels []float64) string {
	high := true
	for _, l := range levels {
		if l <= 2 {
			return "low"
		}
		if l < 4 {
			high = false
		}
	}
	if high {
		return "high"
	}
	return "middle"
}

type criterion struct{ ID, Text, Source string }

func criteria(m *pack.Manifest) []criterion {
	var out []criterion
	for i, c := range coreCriteria {
		out = append(out, criterion{fmt.Sprintf("core%d", i+1), c, "core"})
	}
	if m != nil {
		for i, c := range m.Criteria {
			out = append(out, criterion{fmt.Sprintf("team%02d", i+1), c, "team"})
		}
	}
	return out
}

// afterDone runs once a column holds runs: on a Team column declared done it
// enqueues the auditor, once, and it says whether the column is ready to
// score, which is when the auditor has settled or no Team works the column.
func (e *Engine) afterDone(ctx context.Context, tx *store.Tx, c db.Card, runs []db.AgentRun) (bool, error) {
	summary, ok, err := roundSummary(ctx, tx, c)
	if err != nil || !ok {
		return false, err
	}
	packID, m, err := columnTeam(ctx, tx, c.ID, c.BoardColumn)
	if errors.Is(err, pgx.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	for _, r := range runs {
		if r.Role == "auditor" {
			return !inFlight(r.Status), nil
		}
	}
	a, err := teamAgent(ctx, tx, packID, c.OwnerID, m.Auditor.Name)
	if err != nil {
		return false, err
	}
	if clash, err := auditorClash(ctx, tx, packID, c.OwnerID, m, a); err != nil || clash != "" {
		return clash != "", err
	}
	cited, err := citedRecords(ctx, tx, c, summary)
	if err != nil {
		return false, err
	}
	note := "The leader declared the column done. Audit its summary and the records it cites:\n\n" +
		evidence(records.Body[struct{ Text string }](summary).Text, cited, maxJevInput)
	_, err = e.enqueue(ctx, tx, c, "auditor", m, a, note)
	return false, err
}

// auditorClash names the leader or member whose copy runs the same harness
// and model as the owner's copy of the auditor, a, or is empty when the
// auditor's copy differs from all of them. Copies are editable, so a Team
// that was valid when added can come to break the auditor rule.
func auditorClash(ctx context.Context, tx *store.Tx, packID, owner uuid.UUID, m *pack.Manifest, a db.Agent) (string, error) {
	for _, mem := range slices.Concat([]pack.Member{m.Leader}, m.Members) {
		x, err := teamAgent(ctx, tx, packID, owner, mem.Name)
		if err != nil {
			return "", err
		}
		if x.Harness == a.Harness && x.Model == a.Model {
			return mem.Name, nil
		}
	}
	return "", nil
}

// Audit hands a column declared done to its auditor, or, on a column no Team
// works, straight to scoring. It goes through Advance, which Tick repeats, so
// a crash between declaring done and auditing loses nothing.
func (e *Engine) Audit(_ context.Context, ws, card uuid.UUID) error {
	e.Kick(ws, card)
	return nil
}

// roundSummary is the summary that declared the study's current column and
// attempt done.
func roundSummary(ctx context.Context, tx *store.Tx, c db.Card) (db.Record, bool, error) {
	recs, err := tx.Q.ListRecords(ctx, db.ListRecordsParams{WorkspaceID: tx.WS, CardID: c.ID, BoardColumn: c.BoardColumn})
	if err != nil {
		return db.Record{}, false, err
	}
	for _, r := range slices.Backward(recs) {
		if r.Round == c.Round && r.Kind == "summary" {
			return r, true, nil
		}
	}
	return db.Record{}, false, nil
}

func citedRecords(ctx context.Context, tx *store.Tx, c db.Card, summary db.Record) ([]db.Record, error) {
	keys := records.Body[struct{ Cites []string }](summary).Cites
	if len(keys) == 0 {
		return nil, nil
	}
	keys = slices.Clone(keys)
	for i, k := range keys {
		keys[i] = strings.TrimPrefix(k, "call:")
	}
	return tx.Q.LiveRecordsByKeys(ctx, db.LiveRecordsByKeysParams{WorkspaceID: tx.WS, CardID: c.ID, Keys: keys})
}

// scoring is what one pass read to score a column, with no transaction open.
type scoring struct {
	card      db.Card
	criteria  []criterion
	summary   string
	cited     []db.Record
	work      []db.Record // the records whose quotes and values are checked
	calls     []db.Record // the call records they cite
	runs      []db.Record // the run records they cite
	audit     string
	auditNote string // why there is no audit
	attempt   int
}

var (
	callCite = regexp.MustCompile(`"call:([a-z0-9]+)"`)
	runCite  = regexp.MustCompile(`"run:(x[0-9]+)"`)
)

// Score asks Jev to score the study's current column once it is declared
// done and its auditor settled, then moves the study on, redoes the column or
// blocks it. Jev is asked with no transaction open. When Jev cannot be reached
// the study stays where it is, its owner hears once, and the next pass retries.
func (e *Engine) Score(ctx context.Context, ws, card uuid.UUID) error {
	in, ok, err := e.readScoring(ctx, ws, card)
	if err != nil || !ok {
		return err
	}
	calls, err := LoadCalls(ctx, e.Blobs, in.calls)
	var runs map[string]Run
	if err == nil {
		runs, err = LoadRuns(ctx, e.Blobs, in.runs)
	}
	var answers map[string]jev.ScoreAnswer
	if err == nil {
		answers, err = e.askJev(ctx, in, MatchQuotes(in.work, calls), MatchValues(in.work, runs))
	}
	moved, werr := e.applyScore(ctx, ws, in, answers, err)
	if werr != nil {
		return werr
	}
	if moved {
		e.Kick(ws, card)
	}
	return nil
}

func (e *Engine) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

func (e *Engine) readScoring(ctx context.Context, ws, id uuid.UUID) (scoring, bool, error) {
	var in scoring
	ok := false
	err := e.Store.InWorkspace(ctx, ws, uuid.Nil, func(tx *store.Tx) error {
		c, err := lockCard(ctx, tx, id)
		if err != nil || !working(c) || (c.GatingUntil != nil && c.GatingUntil.After(e.now())) {
			return err
		}
		// With no key nothing can change until the lab adds one; its owner
		// has already heard.
		if e.Jev == nil {
			open, err := tx.Q.CountOpenCardInbox(ctx, db.CountOpenCardInboxParams{WorkspaceID: tx.WS, CardID: c.ID, Kind: "scoring_unavailable"})
			if err != nil || open > 0 {
				return err
			}
		}
		recs, err := tx.Q.ListRecords(ctx, db.ListRecordsParams{WorkspaceID: tx.WS, CardID: c.ID, BoardColumn: c.BoardColumn})
		if err != nil {
			return err
		}
		var summary *db.Record
		in = scoring{card: c, attempt: 1}
		for _, r := range recs {
			switch {
			case r.Kind == "outcome" && r.Round == c.Round:
				return nil // already scored
			case r.Kind == "outcome":
				in.attempt++
			case r.Round != c.Round:
			case r.Kind == "summary":
				summary = &r
			case r.Kind == "audit":
				in.audit = string(r.Body)
			case r.Kind != "call" && r.Kind != "run":
				in.work = append(in.work, r)
			}
		}
		if summary == nil {
			return nil
		}
		packID, m, err := columnTeam(ctx, tx, c.ID, c.BoardColumn)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			in.auditNote = "No auditor checked this column: no Team works it."
		case err != nil:
			return err
		default:
			runs, err := tx.Q.RoundAgentRuns(ctx, db.RoundAgentRunsParams{WorkspaceID: tx.WS, CardID: c.ID, BoardColumn: c.BoardColumn, Round: c.Round})
			if err != nil {
				return err
			}
			i := slices.IndexFunc(runs, func(r db.AgentRun) bool { return r.Role == "auditor" })
			if i < 0 {
				a, err := teamAgent(ctx, tx, packID, c.OwnerID, m.Auditor.Name)
				if err != nil {
					return err
				}
				clash, err := auditorClash(ctx, tx, packID, c.OwnerID, m, a)
				if err != nil || clash == "" {
					return err
				}
				in.auditNote = fmt.Sprintf("No auditor checked this column: the researcher's copy of the auditor runs the same harness and model as %s's, so it would not be an independent check.", clash)
			} else if inFlight(runs[i].Status) {
				return nil
			} else if in.audit == "" {
				in.auditNote = fmt.Sprintf("The auditor's run ended %s without writing findings; this column is scored without an audit.", runs[i].Status)
			}
		}
		in.criteria = criteria(m)
		in.summary = records.Body[struct{ Text string }](*summary).Text
		if in.cited, err = citedRecords(ctx, tx, c, *summary); err != nil {
			return err
		}
		callKeys, runKeys := []string{}, []string{}
		for _, r := range in.cited {
			if r.Kind == "call" {
				callKeys = append(callKeys, r.Key)
			} else if r.Kind == "run" {
				runKeys = append(runKeys, r.Key)
			} else if !slices.ContainsFunc(in.work, func(w db.Record) bool { return w.ID == r.ID }) {
				in.work = append(in.work, r)
			}
		}
		for _, r := range in.work {
			for _, m := range callCite.FindAllStringSubmatch(string(r.Body), -1) {
				callKeys = append(callKeys, m[1])
			}
			for _, m := range runCite.FindAllStringSubmatch(string(r.Body), -1) {
				runKeys = append(runKeys, m[1])
			}
		}
		if len(callKeys) > 0 {
			if in.calls, err = tx.Q.LiveRecordsByKeys(ctx, db.LiveRecordsByKeysParams{WorkspaceID: tx.WS, CardID: c.ID, Keys: callKeys}); err != nil {
				return err
			}
			in.calls = slices.DeleteFunc(in.calls, func(r db.Record) bool { return r.Kind != "call" })
		}
		if len(runKeys) > 0 {
			if in.runs, err = tx.Q.LiveRecordsByKeys(ctx, db.LiveRecordsByKeysParams{WorkspaceID: tx.WS, CardID: c.ID, Keys: runKeys}); err != nil {
				return err
			}
			in.runs = slices.DeleteFunc(in.runs, func(r db.Record) bool { return r.Kind != "run" })
		}
		until := e.now().Add(scoringLease)
		ok = true
		return tx.Q.SetGatingLease(ctx, db.SetGatingLeaseParams{GatingUntil: &until, ID: c.ID, WorkspaceID: tx.WS})
	})
	return in, ok, err
}

func (e *Engine) askJev(ctx context.Context, in scoring, quotes []QuoteMatch, values []ValueMatch) (map[string]jev.ScoreAnswer, error) {
	if e.Jev == nil {
		return nil, errNoJev
	}
	qs := map[string]jev.ScoreQuestion{}
	for _, c := range in.criteria {
		qs[c.ID] = jev.ScoreQuestion{Instructions: c.Text, Levels: scoreLevels}
	}
	asked, err := json.Marshal(qs)
	if err != nil {
		return nil, err
	}
	retry := jev.NewRetry()
	if e.Retry != nil {
		retry = e.Retry()
	}
	var out map[string]jev.ScoreAnswer
	for budget := maxJevInput - len(asked) - 256; ; budget /= 2 {
		state := scoringState(in, quotes, values, budget)
		err = retry.Do(ctx, func() (err error) {
			out, err = e.Jev.Score(ctx, state, qs)
			return err
		})
		if !errors.Is(err, jev.ErrTooLong) || budget/2 < minJevInput {
			return out, err
		}
	}
}

// labelRule asks Jev to check what code cannot: that each item's kind fits
// its content and that its numbers sit where the value check finds them.
const labelRule = "\nItems of the kinds prediction, explanation, plan and limitation need no source. " +
	"Every other item states facts: its quotes cite the calls they come from, and its reported numbers sit in value fields and cite the script run that printed them. " +
	"Judge whether each item's kind fits its content, and whether its results sit in value fields.\n"

const cutMark = "\n[cut: the rest is left out to stay within Jev's limit]"

// scoringState is what Jev reads: the study, the leader's summary and the
// records it cites, the code's quote and value checks and the audit, at most
// budget bytes. Every agent's text is fenced as data.
func scoringState(in scoring, quotes []QuoteMatch, values []ValueMatch, budget int) string {
	var head, tail strings.Builder
	question, cut := Excerpt(asked(in.card), budget/8)
	if cut {
		question += cutMark
	}
	fmt.Fprintf(&head, "Lykeion study #%d, its %s column.\n%s\n", in.card.Number, title(in.card.BoardColumn), fence.Untrusted("What the researcher asked", question))

	tail.WriteString("\nQuote check, done by Lykeion's code: each quoted passage looked up word for word, ignoring whitespace, in the answers of the database calls its record cites.\n")
	if len(quotes) == 0 {
		tail.WriteString("No record quotes a passage.\n")
	}
	var qb strings.Builder
	for _, q := range quotes {
		text, cut := Excerpt(q.Quote, 300)
		if cut {
			text += "…"
		}
		if q.Matched {
			fmt.Fprintf(&qb, "- %s: found in call %s: %s\n", q.Record, q.Call, strconv.Quote(text))
		} else if q.Unchecked != "" {
			fmt.Fprintf(&qb, "- %s: could not be checked: call %s's full answer is unreadable: %s\n", q.Record, q.Unchecked, strconv.Quote(text))
		} else {
			fmt.Fprintf(&qb, "- %s: not found in any call it cites: %s\n", q.Record, strconv.Quote(text))
		}
	}
	if qb.Len() > 0 {
		list, cut := Excerpt(qb.String(), budget/8)
		if cut {
			list += cutMark + "\n"
		}
		tail.WriteString(fence.Untrusted("The quotes, as the agents wrote them", list))
	}

	tail.WriteString("\nValue check, done by Lykeion's code: each reported value looked up in the output of the runs its item cites, " +
		"every printed number rounded to the decimal places the value states; an integer must be printed exactly.\n")
	if len(values) == 0 {
		tail.WriteString("No record reports a value.\n")
	}
	var vb strings.Builder
	for _, v := range values {
		text, cut := Excerpt(v.Value, 100)
		if cut {
			text += "…"
		}
		switch {
		case v.Matched:
			fmt.Fprintf(&vb, "- %s: found in script run %s: %s\n", v.Record, v.Run, strconv.Quote(text))
		case v.Unchecked != "":
			fmt.Fprintf(&vb, "- %s: could not be checked: run %s's full output is unreadable: %s\n", v.Record, v.Unchecked, strconv.Quote(text))
		case v.Failed:
			fmt.Fprintf(&vb, "- %s: found only in a script run that failed (exit status %d) %s: %s\n", v.Record, v.ExitStatus, v.Run, strconv.Quote(text))
		case v.Exploration:
			fmt.Fprintf(&vb, "- %s: found only in exploration (run_code) %s, not in a script run: %s\n", v.Record, v.Run, strconv.Quote(text))
		case v.CallsOnly:
			fmt.Fprintf(&vb, "- %s: cites only database calls; values are checked against script runs only: %s\n", v.Record, strconv.Quote(text))
		case v.Uncited:
			fmt.Fprintf(&vb, "- %s: cites no run: %s\n", v.Record, strconv.Quote(text))
		default:
			fmt.Fprintf(&vb, "- %s: not found in any run it cites: %s\n", v.Record, strconv.Quote(text))
		}
	}
	if vb.Len() > 0 {
		list, cut := Excerpt(vb.String(), budget/8)
		if cut {
			list += cutMark + "\n"
		}
		tail.WriteString(fence.Untrusted("The values, as the agents wrote them", list))
	}
	tail.WriteString(labelRule)
	tail.WriteString("\nThe auditor's findings:\n")
	if in.audit != "" {
		audit, cut := Excerpt(in.audit, budget/8)
		if cut {
			audit += cutMark
		}
		tail.WriteString(fence.Untrusted("The auditor's findings", audit))
	} else {
		tail.WriteString(in.auditNote + "\n")
	}
	return head.String() + evidence(in.summary, in.cited, budget-head.Len()-tail.Len()) + tail.String()
}

// evidence is the leader's summary and the records it cites, each fenced, in
// at most budget bytes: the longest records are cut first, to a common
// length, and each cut is marked. A summary too long to leave the records
// room is cut inside its fence, and when even empty fences overflow, the
// last cited records are left out, saying how many.
func evidence(summary string, cited []db.Record, budget int) string {
	dropped := 0
	build := func(summary string, limit int) string {
		var b strings.Builder
		b.WriteString(fence.Untrusted("The summary declaring the column done", summary))
		if len(cited) == 0 && dropped == 0 {
			b.WriteString("The summary cites no records.\n")
		} else {
			b.WriteString("\nThe records it cites:\n")
		}
		for _, r := range cited {
			body := string(r.Body)
			if cut, more := Excerpt(body, limit); more {
				body = cut + fmt.Sprintf("\n[cut: %d of %d bytes left out to stay within Jev's limit]", len(body)-len(cut), len(body))
			}
			b.WriteString(fence.Untrusted(fmt.Sprintf("Record %s (%s, written by the %s)", r.Key, r.Kind, r.Role), body))
		}
		if dropped > 0 {
			fmt.Fprintf(&b, "[%d more cited records left out to stay within Jev's limit]\n", dropped)
		}
		return b.String()
	}
	for len(cited) > 0 && len(build("", 0)) > budget/2 {
		cited = cited[:len(cited)-1]
		dropped++
	}
	if room := (budget - len(build("", 0)) - len(cutMark)) / 2; len(summary) > room {
		summary, _ = Excerpt(summary, max(room, 0))
		summary += cutMark
	}
	longest := 0
	for _, r := range cited {
		longest = max(longest, len(r.Body))
	}
	if s := build(summary, longest); len(s) <= budget {
		return s
	}
	lo, hi := 0, longest
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if len(build(summary, mid)) <= budget {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return build(summary, lo)
}

// applyScore writes the scores and the verdict, and moves the study on,
// restarts its column or blocks it. It re-reads the study first: a study that
// left the column, or a column scored meanwhile, is left alone. It says
// whether a column now waits to start.
func (e *Engine) applyScore(ctx context.Context, ws uuid.UUID, in scoring, answers map[string]jev.ScoreAnswer, jerr error) (bool, error) {
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
		if jerr != nil {
			slog.Warn("scoring unavailable", "card", c.ID, "column", c.BoardColumn, "err", jerr)
			open, err := tx.Q.CountOpenCardInbox(ctx, db.CountOpenCardInboxParams{WorkspaceID: tx.WS, CardID: c.ID, Kind: "scoring_unavailable"})
			if err != nil || open > 0 {
				return err
			}
			return tx.Q.InsertInboxItem(ctx, db.InsertInboxItemParams{WorkspaceID: tx.WS, CardID: c.ID, UserID: c.OwnerID, Kind: "scoring_unavailable",
				Text: unavailableText(c.Number, c.BoardColumn)})
		}
		if err := resolveInbox(ctx, tx, c.ID, "scoring_unavailable"); err != nil {
			return err
		}
		var levels []float64
		lowest := in.criteria[0]
		lowestLevel := 6.0
		for _, cr := range in.criteria {
			a := answers[cr.ID]
			level := a.Score + 1
			levels = append(levels, level)
			if level < lowestLevel {
				lowest, lowestLevel = cr, level
			}
			if err := writeServer(ctx, tx, c, "score", map[string]any{"criterion": cr.Text, "source": cr.Source,
				"level": level, "probabilities": a.Probabilities}); err != nil {
				return err
			}
		}
		verdict := Verdict(levels)
		if err := writeServer(ctx, tx, c, "outcome", map[string]any{"verdict": verdict, "attempt": in.attempt}); err != nil {
			return err
		}
		weak := weakCriterion(lowest.Text, lowestLevel)
		next := db.SaveCardFlowParams{ID: c.ID, WorkspaceID: tx.WS, BoardColumn: c.BoardColumn, State: c.State,
			BlockKind: c.BlockKind, Round: c.Round, Redos: c.Redos}
		var items []board.Item
		switch {
		case verdict == "low" && c.Redos == 0:
			next.Redos, next.Round = 1, c.Round+1
			next.HandoffNote = fmt.Sprintf("Jev scored the last attempt at this column low on %s. Redo the column, and fix that first.", weak)
		case verdict == "low":
			if err := e.drawRating(ctx, tx, c); err != nil {
				return err
			}
			next.State, next.BlockKind = "blocked", "low_score"
			items = append(items, board.Item{Kind: "blocked", Text: blockedText(c.Number, c.BoardColumn, weak)})
		default:
			if err := e.drawRating(ctx, tx, c); err != nil {
				return err
			}
			last := board.StageIndex(c.BoardColumn) >= board.StageIndex(c.ScopeTo)
			if verdict == "middle" {
				items = append(items, board.Item{Kind: "middle_score", Text: middleScoreText(c.Number, c.BoardColumn, weak, last)})
			}
			if i := board.StageIndex(c.BoardColumn); !last {
				next.BoardColumn, next.Round, next.Redos = board.Stages[i+1], c.Round+1, 0
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
		return tx.Emit(ctx, "card.steered", &c.ID, store.System, map[string]any{"column": next.BoardColumn, "state": next.State, "verdict": verdict})
	})
	return moved, err
}

// writeServer stores a record the server writes itself, under the study's
// next key, on its current column and round.
func writeServer(ctx context.Context, tx *store.Tx, c db.Card, kind string, body any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	key, err := records.NextRecordKey(ctx, tx, c.ID)
	if err != nil {
		return err
	}
	rec, err := tx.Q.InsertRecord(ctx, db.InsertRecordParams{WorkspaceID: tx.WS, CardID: c.ID, BoardColumn: c.BoardColumn,
		Round: c.Round, Kind: kind, Role: "server", Key: key, Body: b})
	if err != nil {
		return err
	}
	return tx.Emit(ctx, "record.created", &c.ID, store.System, map[string]any{"record_id": rec.ID, "kind": kind, "key": key, "role": "server"})
}

func resolveInbox(ctx context.Context, tx *store.Tx, card uuid.UUID, kind string) error {
	ids, err := tx.Q.ResolveCardInboxKind(ctx, db.ResolveCardInboxKindParams{WorkspaceID: tx.WS, CardID: card, Kind: kind})
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := tx.Emit(ctx, "inbox.resolved", &card, store.System, map[string]string{"inbox_item_id": id.String()}); err != nil {
			return err
		}
	}
	return nil
}

// unblock restarts the column of a study blocked by a second low score when
// its owner comments: the comment becomes the leader's (or agent's) note, and
// the redo count stays.
func unblock(ctx context.Context, tx *store.Tx, c db.Card, author uuid.UUID, comment string) (bool, error) {
	if c.State != "blocked" || c.BlockKind != "low_score" || c.Ending != "" || author != c.OwnerID {
		return false, nil
	}
	if err := tx.Q.SaveCardFlow(ctx, db.SaveCardFlowParams{ID: c.ID, WorkspaceID: tx.WS, BoardColumn: c.BoardColumn, State: board.Working,
		Round: c.Round + 1, Redos: c.Redos, HandoffNote: "The researcher asked for this column to be redone:\n" + comment}); err != nil {
		return false, err
	}
	if err := resolveInbox(ctx, tx, c.ID, "blocked"); err != nil {
		return false, err
	}
	return true, tx.Emit(ctx, "card.steered", &c.ID, store.User(author), map[string]any{"column": c.BoardColumn, "state": board.Working})
}
