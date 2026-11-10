package flow

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"lykeion/server/internal/board"
	"lykeion/server/internal/handin"
	"lykeion/server/internal/records"
	"lykeion/server/internal/review"
	"lykeion/server/internal/service"
	"lykeion/server/internal/store"
	"lykeion/server/pkg/agent"
	db "lykeion/server/pkg/db/generated"
)

// modeRuns is the Validation mode of a study whose Experimentation lead
// handed in: its hypotheses are checked against its recorded runs.
const modeRuns = "runs"

// modeLines tell a Validation run which items its hand-in holds.
var modeLines = map[string]string{
	modeRuns: "This study has hypotheses and recorded script runs in Experimentation: hand in an `assessments` entry for every hypothesis, not verdicts or flagged errors.",
	"claims": "This study's hypotheses are not checked against recorded runs: hand in `verdicts` and `flagged_errors`, not assessments.",
}

// statusRank orders the statuses an objection can leave, lowest first.
var statusRank = map[string]int{review.Invalid: 0, review.Inconclusive: 1, review.Caveated: 2, review.Supported: 3, review.Refuted: 3}

// decideRouteBack is the decision of a Validation round that sends the
// study back to Experimentation.
const decideRouteBack = "route_back"

// otherFamily is the CLI family the objector runs on, given the column's.
var otherFamily = map[string]string{"claude": "codex", "codex": "claude"}

// objectorAssignment is what researchers read the objector was asked to do.
const objectorAssignment = "Look for errors in the study's Validation statuses."

// enqueueObjector queues the blind objector on the card's Validation round
// with brief as its whole prompt: a system run with no agent, the harness's
// default model and no skill. It runs on the other CLI family from the
// column's lead run; when no runtime that could claim the study's runs has
// that family online and one has the lead's, it runs on the lead's family,
// and sameFamily says so. With no lead run it takes whichever family is
// online, Claude Code first. When nothing that fits is online it waits like
// any run. Its session is always fresh, so it shares no history with any run.
func (e *Engine) enqueueObjector(ctx context.Context, tx *store.Tx, c db.Card, brief string) (sameFamily bool, err error) {
	runs, err := tx.Q.RoundAgentRuns(ctx, db.RoundAgentRunsParams{WorkspaceID: tx.WS, CardID: c.ID, BoardColumn: c.BoardColumn, Round: c.Round})
	if err != nil {
		return false, err
	}
	lead := ""
	for _, r := range runs {
		if handin.Leads(r.Role) {
			lead = r.Harness
			break
		}
	}
	online := func(h string) (bool, error) {
		return tx.Q.CardHasOnlineRuntime(ctx, db.CardHasOnlineRuntimeParams{WorkspaceID: tx.WS, CardID: c.ID, Harness: h})
	}
	harness := otherFamily[lead]
	if harness == "" {
		harness = "claude"
		if lead == "" {
			claude, err := online("claude")
			if err != nil {
				return false, err
			}
			codex, err := online("codex")
			if err != nil {
				return false, err
			}
			if !claude && codex {
				harness = "codex"
			}
		}
	}
	ok, err := online(harness)
	if err != nil {
		return false, err
	}
	if !ok && lead != "" {
		if sameFamily, err = online(lead); err != nil {
			return false, err
		}
		if sameFamily {
			harness = lead
		}
	}
	_, err = e.AgentRuns.Enqueue(ctx, tx, c.OwnerID, service.NewAgentRun{Card: c.ID, Column: c.BoardColumn, Round: c.Round,
		Role: "objector", Harness: harness, Prompt: brief, Assignment: objectorAssignment,
		ContinuityScope: ContinuityScope(c.ID, "objector", c.BoardColumn, c.Round) + "/objector:" + uuid.NewString()})
	return sameFamily, err
}

// studyRecords are the study's live records in every column.
func studyRecords(ctx context.Context, tx *store.Tx, card uuid.UUID) ([]db.Record, error) {
	var out []db.Record
	for _, col := range board.Stages {
		recs, err := tx.Q.ListRecords(ctx, db.ListRecordsParams{WorkspaceID: tx.WS, CardID: card, BoardColumn: col})
		if err != nil {
			return nil, err
		}
		out = append(out, recs...)
	}
	return out, nil
}

// validationMode is how Validation checks the study: modeRuns, or "claims".
func validationMode(ctx context.Context, tx *store.Tx, card uuid.UUID) (string, error) {
	study, err := studyRecords(ctx, tx, card)
	return handin.ModeOf(study), err
}

// claimSources are the records holding each source the verdicts and
// flagged errors among items cite: an attached file's call, a file
// Experimentation saved, or the calls that returned a source id, oldest
// first. A source no record holds is left out.
func claimSources(ctx context.Context, tx *store.Tx, card uuid.UUID, items []db.Record) (map[string][]db.Record, error) {
	study, err := studyRecords(ctx, tx, card)
	if err != nil {
		return nil, err
	}
	ev := handin.NewEvidence(study)
	var calls []db.Record
	for _, r := range study {
		if r.Kind == "call" {
			calls = append(calls, r)
		}
	}
	out := map[string][]db.Record{}
	for _, it := range items {
		src := records.Body[struct{ Source string }](it).Source
		if _, ok := out[src]; ok || src == "" {
			continue
		}
		var held []db.Record
		for _, r := range calls {
			b := records.Body[struct {
				Server string
				Args   struct{ Name string }
			}](r)
			if b.Server == records.AttachmentServer && b.Args.Name == src {
				held = append(held, r)
			}
		}
		if len(held) == 0 {
			for _, r := range study {
				if r.BoardColumn == board.Experimentation && r.Kind == "output" && records.Body[struct{ Name string }](r).Name == src {
					held = append(held, r)
				}
			}
		}
		if len(held) == 0 {
			refs := ev.Sources(src)
			for _, r := range calls {
				if slices.Contains(refs, "call:"+r.Key) {
					held = append(held, r)
				}
			}
		}
		if len(held) > 0 {
			out[src] = held
		}
	}
	return out, nil
}

// sourceTexts reads each source's records into its text: a call's answer,
// a saved file's text.
func (e *Engine) sourceTexts(ctx context.Context, sources map[string][]db.Record) (map[string]string, error) {
	out := map[string]string{}
	for src, recs := range sources {
		calls, err := LoadCalls(ctx, e.Blobs, recs)
		if err != nil {
			return nil, err
		}
		files, err := e.outputTexts(ctx, recs)
		if err != nil {
			return nil, err
		}
		var parts []string
		for _, r := range recs {
			if c, ok := calls[r.Key]; ok {
				parts = append(parts, c.Answer)
			} else if f, ok := files[r.Key]; ok {
				parts = append(parts, f)
			}
		}
		out[src] = strings.Join(parts, "\n\n")
	}
	return out, nil
}

// statusBody is a server `validation_status` record: one hypothesis's
// status, the code's before the objector (final false) and after its
// objections are judged (final true).
type statusBody struct {
	Hypothesis string   `json:"hypothesis"`
	Status     string   `json:"status"`
	Reasons    []string `json:"reasons"`
	Cites      []string `json:"cites"`
	Final      bool     `json:"final"`
}

// validating is what one pass read of a Validation round that checks the
// study's runs, with no transaction open.
type validating struct {
	hyps        []db.Record // the study's standing hypotheses, in order
	hs          []review.HypothesisBody
	assessments map[string]review.Assessment // by hypothesis text, trimmed
	values      []db.Record                  // Experimentation's newest round's values
	outputs     []db.Record                  // and the files it saved
	statuses    []review.StatusResult        // the code's, one per hypothesis
	objections  []db.Record                  // the objector's, not yet judged
	sameFamily  bool
	objector    review.ObjectorRun
}

// reviewValidation reviews the study's Validation round. A study with no
// recorded runs has its claims reviewed as the other columns' items are.
// Otherwise its hypotheses get statuses from code once the rerun has
// reported, the blind objector looks for errors in them, Jev judges each
// objection, and upheld ones lower the statuses. The study then ends, goes
// back to Experimentation once, or waits for its owner. Jev is asked with no
// transaction open; when it cannot answer the study stays where it is.
func (e *Engine) reviewValidation(ctx context.Context, ws uuid.UUID, in scoring) error {
	var v validating
	claims, judge := false, false
	err := e.Store.InWorkspace(ctx, ws, uuid.Nil, func(tx *store.Tx) error {
		c, err := lockCard(ctx, tx, in.card.ID)
		if err != nil || c.ID == uuid.Nil {
			return err
		}
		if c.Version != in.card.Version {
			return nil
		}
		if !current(c, in.card.BoardColumn, in.card.Round) {
			return tx.Q.SetGatingLease(ctx, db.SetGatingLeaseParams{ID: c.ID, WorkspaceID: tx.WS})
		}
		if pending, err := e.deferScoringForComments(ctx, tx, c); err != nil || pending {
			return err
		}
		study, err := studyRecords(ctx, tx, c.ID)
		if err != nil {
			return err
		}
		if handin.ModeOf(study) != modeRuns {
			claims = true
			return nil
		}
		// Every wait lets go of the lease: the run waited on kicks the next
		// pass when it settles.
		wait := func() error {
			return tx.Q.SetGatingLease(ctx, db.SetGatingLeaseParams{ID: c.ID, WorkspaceID: tx.WS})
		}
		if err := e.startRerun(ctx, tx, c); err != nil {
			return err
		}
		rerun, written, err := v.read(ctx, tx, c, study)
		if err != nil {
			return err
		}
		if rerun == nil {
			return wait()
		}
		runs, err := tx.Q.RoundAgentRuns(ctx, db.RoundAgentRunsParams{WorkspaceID: tx.WS, CardID: c.ID, BoardColumn: c.BoardColumn, Round: c.Round})
		if err != nil {
			return err
		}
		var objector *db.AgentRun
		for i := range runs {
			if runs[i].Role == "objector" {
				objector = &runs[i]
			}
		}
		if objector == nil {
			if !written {
				for _, s := range v.statuses {
					if err := writeServer(ctx, tx, c, "validation_status", statusBody{Hypothesis: s.Hypothesis, Status: s.Status,
						Reasons: s.Reasons, Cites: nonNil(s.Cites)}); err != nil {
						return err
					}
				}
			}
			brief, _ := review.ObjectorBrief(v.statuses, v.hs, names(v.values), names(v.outputs), int64(c.Round))
			if _, err := e.enqueueObjector(ctx, tx, c, brief); err != nil {
				return err
			}
			return wait()
		}
		if inFlight(objector.Status) {
			return wait()
		}
		lead := slices.IndexFunc(runs, func(r db.AgentRun) bool { return handin.Leads(r.Role) })
		v.sameFamily = lead >= 0 && runs[lead].Harness == objector.Harness
		if v.objector, err = objectorRan(ctx, tx, *objector); err != nil {
			return err
		}
		v.objector.SameFamily = v.sameFamily
		judge = true
		return nil
	})
	switch {
	case err != nil:
		return err
	case claims:
		return e.reviewColumn(ctx, ws, in)
	case !judge:
		return nil
	}
	judged, jerr := e.judgeObjections(ctx, ws, in, v)
	moved, err := e.applyValidation(ctx, ws, in, v, judged, jerr)
	if err != nil {
		return err
	}
	if moved {
		e.Kick(ws, in.card.ID)
	}
	return nil
}

// objectorRan says whether the settled objector handed in its objections
// and, when it did not, why, in words a researcher reads; the harness's own
// error is never shown.
func objectorRan(ctx context.Context, tx *store.Tx, run db.AgentRun) (review.ObjectorRun, error) {
	if run.Status == "completed" {
		recs, err := tx.Q.AgentRunRecords(ctx, db.AgentRunRecordsParams{WorkspaceID: tx.WS, AgentRunID: &run.ID})
		if err != nil {
			return review.ObjectorRun{}, err
		}
		for _, r := range recs {
			if r.Kind == "hand_in" {
				if records.Body[handInRecord](r).HandIn.Status == "done" {
					return review.ObjectorRun{Ran: true}, nil
				}
				return review.ObjectorRun{Reason: "the objector did not run: it said it could not do the review"}, nil
			}
		}
		return review.ObjectorRun{Reason: "the objector did not run: it ended without a hand-in"}, nil
	}
	reason := "the objector did not run: its run was cancelled"
	switch why := ptrOr(run.FailureReason); {
	case run.Status != "failed":
	case why == agent.ReasonHandInInvalid:
		reason = "the objector did not run: its hand-in was not valid"
	case why != "":
		reason = "the objector did not run: its run failed (" + strings.ReplaceAll(why, "_", " ") + ")"
	default:
		reason = "the objector did not run: its run failed"
	}
	return review.ObjectorRun{Reason: reason}, nil
}

// read fills v from the study's records and says which rerun record the
// round has, if any, and whether its code statuses were written already.
func (v *validating) read(ctx context.Context, tx *store.Tx, c db.Card, study []db.Record) (*db.Record, bool, error) {
	var assessments []db.Record
	var rerun *db.Record
	written := false
	for _, r := range study {
		switch {
		case r.BoardColumn != board.Validation || r.Round != c.Round:
		case r.Kind == review.KindAssessment:
			assessments = append(assessments, r)
		case r.Kind == "rerun":
			rerun = &r
		case r.Kind == "validation_status":
			written = true
		case r.Kind == "objection" && r.Role == "objector":
			v.objections = append(v.objections, r)
		}
	}
	if rerun == nil {
		return nil, written, nil
	}
	for _, r := range handin.StandingHypotheses(study) {
		v.hyps = append(v.hyps, r)
		v.hs = append(v.hs, records.Body[review.HypothesisBody](r))
	}
	v.assessments = map[string]review.Assessment{}
	var as []review.Assessment
	for _, r := range latestItems(assessments) {
		a := records.Body[review.Assessment](r)
		v.assessments[strings.TrimSpace(a.Hypothesis)] = a
		as = append(as, a)
	}
	st, err := service.StandingExperimentation(ctx, tx, c.ID)
	if err != nil {
		return nil, written, err
	}
	v.values = st.Values
	for _, r := range st.Records {
		if r.Kind == "output" {
			v.outputs = append(v.outputs, r)
		}
	}
	v.statuses = review.Statuses(v.hs, as, measured(v.values, *rerun), testsPrinted(as, records.Body[rerunBody](*rerun).Printed))
	return rerun, written, nil
}

// measured is each standing value as the status rules read it: its number,
// if it is one, whether a script run made it, and whether the rerun
// reproduced it.
func measured(values []db.Record, rerun db.Record) map[string]review.Measured {
	rb := records.Body[rerunBody](rerun)
	m := map[string]review.Measured{}
	for _, r := range values {
		v := valueOf(r)
		n, err := strconv.ParseFloat(strings.TrimSpace(v.Value), 64)
		m[v.Name] = review.Measured{Value: n, Number: err == nil, Text: v.Value, FromScript: len(v.Cites) > 0, Reproduced: rb.Reproduced[v.Name],
			RerunNote: rb.Notes[v.Name], Key: r.Key, RerunKey: rerun.Key}
	}
	return m
}

// names are the names values and saved files go by.
func names(recs []db.Record) []string {
	out := []string{}
	for _, r := range recs {
		out = append(out, records.Body[struct{ Name string }](r).Name)
	}
	return out
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// judgeObjections has Jev judge the objector's objections against what the
// objector was shown under each target: a hypothesis's block in its brief, a
// value as recorded, or a saved file's text.
func (e *Engine) judgeObjections(ctx context.Context, ws uuid.UUID, in scoring, v validating) ([]review.Objection, error) {
	if len(v.objections) == 0 {
		return nil, nil
	}
	if e.Jev == nil {
		return nil, errNoJev
	}
	texts := review.RivalTexts(v.statuses, v.hs, int64(in.card.Round))
	for _, r := range v.values {
		x := valueOf(r)
		if _, ok := texts[x.Name]; !ok {
			texts[x.Name] = fmt.Sprintf("%s = %s, shown in %s", x.Name, x.Value, x.File)
		}
	}
	files, err := e.outputTexts(ctx, v.outputs)
	if err != nil {
		return nil, fmt.Errorf("reading saved files: %w", err)
	}
	for _, r := range v.outputs {
		if name := records.Body[struct{ Name string }](r).Name; texts[name] == "" {
			texts[name] = files[r.Key]
		}
	}
	objs := make([]review.Objection, len(v.objections))
	for i, r := range v.objections {
		objs[i] = records.Body[review.Objection](r)
	}
	p := &reviewPass{e: e, ws: ws, card: in.card, retry: e.retry(), renewed: e.now()}
	out, err := review.JudgeObjections(ctx, retryAsker{p}, objs, texts)
	if err != nil {
		err = fmt.Errorf("validation review: %w", err)
	}
	return out, err
}

// target is what an objection's label names: the statuses it bears on, by
// index, and the key of the record it stands for. A hypothesis's label
// bears on its status; a value on every status it measures or controls; a
// saved file on every status whose values it shows.
func (v validating) target(label string, seed int64) ([]int, string) {
	label = strings.TrimSpace(label)
	_, labels := review.ObjectorBrief(v.statuses, v.hs, nil, nil, seed)
	if text, ok := labels[label]; ok {
		i := slices.IndexFunc(v.hs, func(h review.HypothesisBody) bool { return h.Text == text })
		return []int{i}, v.hyps[i].Key
	}
	uses := func(match func(valueRecord) bool) []int {
		var out []int
		for i, s := range v.statuses {
			a, ok := v.assessments[strings.TrimSpace(s.Hypothesis)]
			if !ok {
				continue
			}
			if slices.ContainsFunc(v.values, func(r db.Record) bool {
				x := valueOf(r)
				return match(x) && (x.Name == a.Measurement || a.Control != nil && x.Name == *a.Control)
			}) {
				out = append(out, i)
			}
		}
		return out
	}
	if i := slices.IndexFunc(v.values, func(r db.Record) bool { return valueOf(r).Name == label }); i >= 0 {
		return uses(func(x valueRecord) bool { return x.Name == label }), v.values[i].Key
	}
	if i := slices.IndexFunc(v.outputs, func(r db.Record) bool { return records.Body[struct{ Name string }](r).Name == label }); i >= 0 {
		return uses(func(x valueRecord) bool { return x.File == label }), v.outputs[i].Key
	}
	return nil, ""
}

// applyValidation writes the judged objections, the final statuses, the
// review and its outcome, and ends the study, routes it back or holds it, as
// applyReview does for the other columns: a study that left the round, or a
// round decided meanwhile, is left alone. It says whether a column now
// waits to start.
func (e *Engine) applyValidation(ctx context.Context, ws uuid.UUID, in scoring, v validating, judged []review.Objection, jerr error) (bool, error) {
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
		if !current(c, in.card.BoardColumn, in.card.Round) {
			return nil
		}
		if pending, err := e.deferScoringForComments(ctx, tx, c); err != nil || pending {
			return err
		}
		if n, err := tx.Q.CountRoundKindRecords(ctx, db.CountRoundKindRecordsParams{WorkspaceID: tx.WS, CardID: c.ID,
			BoardColumn: c.BoardColumn, Round: c.Round, Kind: "outcome"}); err != nil || n > 0 {
			return err
		}
		if jerr != nil {
			return scoringUnavailable(ctx, tx, c, jerr)
		}
		if err := resolveInbox(ctx, tx, c.ID, "scoring_unavailable"); err != nil {
			return err
		}
		statuses := make([]review.StatusResult, len(v.statuses))
		for i, s := range v.statuses {
			s.Reasons, s.Cites = slices.Clone(s.Reasons), slices.Clone(s.Cites)
			statuses[i] = s
		}
		// The judged objections replace the ones the objector filed.
		for i, o := range judged {
			if err := tx.Q.SupersedeRecord(ctx, db.SupersedeRecordParams{WorkspaceID: tx.WS, ID: v.objections[i].ID}); err != nil {
				return err
			}
			var on []int
			on, o.TargetKey = v.target(o.Label, int64(c.Round))
			o.SameFamily = v.sameFamily
			key, err := writeServerKey(ctx, tx, c, "objection", o)
			if err != nil {
				return err
			}
			if !o.Upheld {
				continue
			}
			// Upheld objections don't stack: each lowers the code's status
			// on its own, and the lowest result stands.
			for _, j := range on {
				s := &statuses[j]
				if lower := review.Lower(v.statuses[j].Status, o.Type); lower != v.statuses[j].Status {
					if statusRank[lower] < statusRank[s.Status] {
						s.Status = lower
					}
					s.Reasons = append(s.Reasons, "an upheld "+strings.ReplaceAll(o.Type, "_", " ")+" objection")
					s.Cites = append(s.Cites, key)
				}
			}
		}
		objector := v.objector
		res := review.Result{Column: c.BoardColumn, Attempt: in.attempt, Items: []review.Item{}, Objector: &objector, Decision: review.DecidePass}
		var invalid []string
		for i, s := range statuses {
			if err := writeServer(ctx, tx, c, "validation_status", statusBody{Hypothesis: s.Hypothesis, Status: s.Status,
				Reasons: s.Reasons, Cites: nonNil(s.Cites), Final: true}); err != nil {
				return err
			}
			h := v.hyps[i]
			res.Items = append(res.Items, review.Item{Key: h.Key, Kind: h.Kind, Label: review.LabelOf(h), Identity: review.IdentityOf(h),
				Status: s.Status, Checks: []review.Check{}})
			if s.Status == review.Invalid {
				invalid = append(invalid, review.LabelOf(h)+": "+strings.Join(s.Reasons, "; "))
			}
		}
		recs, err := tx.Q.ListRecords(ctx, db.ListRecordsParams{WorkspaceID: tx.WS, CardID: c.ID, BoardColumn: c.BoardColumn})
		if err != nil {
			return err
		}
		switch {
		case len(invalid) == 0:
		case !slices.ContainsFunc(recs, func(r db.Record) bool { return r.Kind == "route_back" }):
			res.Decision = decideRouteBack
		default:
			res.Decision = review.DecideAsk
		}
		if err := writeServer(ctx, tx, c, "review", res); err != nil {
			return err
		}
		if err := writeServer(ctx, tx, c, "outcome", map[string]any{"verdict": res.Decision, "attempt": in.attempt}); err != nil {
			return err
		}
		switch res.Decision {
		case decideRouteBack:
			moved = true
			return e.routeBack(ctx, tx, c, board.Experimentation, "Validation sent this back: "+strings.Join(invalid, "; "), invalid)
		case review.DecideAsk:
			// Held like a block, it is offered for rating as a block is.
			e.drawRatingSafely(ctx, tx, c)
			return e.holdInvalid(ctx, tx, c, invalid)
		}
		e.drawRatingSafely(ctx, tx, c)
		if err := tx.Q.SaveCardFlow(ctx, db.SaveCardFlowParams{ID: c.ID, WorkspaceID: tx.WS, BoardColumn: c.BoardColumn, State: "done",
			Round: c.Round, Redos: c.Redos}); err != nil {
			return err
		}
		if err := e.finishStudy(ctx, tx, c); err != nil {
			return err
		}
		finished = true
		return tx.Emit(ctx, "card.steered", &c.ID, store.System, map[string]any{"column": c.BoardColumn, "state": "done", "verdict": res.Decision})
	})
	if err == nil && finished {
		e.goReport("check report", ws, in.card.ID, e.checkReport)
	}
	return moved, err
}

// routeBack sends the study from its current column back to column, as a
// fresh round with no redo spent and note for its first run, and records
// that it did and why, one reason a line.
func (e *Engine) routeBack(ctx context.Context, tx *store.Tx, c db.Card, column, note string, reasons []string) error {
	if err := writeServer(ctx, tx, c, "route_back", map[string]any{"from": c.BoardColumn, "to": column, "reasons": reasons}); err != nil {
		return err
	}
	if err := tx.Q.SaveCardFlow(ctx, db.SaveCardFlowParams{ID: c.ID, WorkspaceID: tx.WS, BoardColumn: column, State: board.Working,
		Round: c.Round + 1, HandoffNote: note}); err != nil {
		return err
	}
	return tx.Emit(ctx, "card.steered", &c.ID, store.System, map[string]any{"column": column, "state": board.Working, "verdict": decideRouteBack})
}

// holdInvalid holds a study whose Validation found invalid statuses after
// it had already been routed back once, and asks its owner; the owner's
// comment restarts Validation through unblock.
func (e *Engine) holdInvalid(ctx context.Context, tx *store.Tx, c db.Card, invalid []string) error {
	if err := tx.Q.SaveCardFlow(ctx, db.SaveCardFlowParams{ID: c.ID, WorkspaceID: tx.WS, BoardColumn: c.BoardColumn, State: "blocked",
		Round: c.Round, Redos: c.Redos, HandoffNote: c.HandoffNote}); err != nil {
		return err
	}
	if err := holdForDecision(ctx, tx, c, "needs_decision", "validation_invalid", validationInvalidText(c.Number, invalid), e.now()); err != nil {
		return err
	}
	if err := tx.Emit(ctx, "inbox.created", &c.ID, store.System, map[string]any{"card_id": c.ID}); err != nil {
		return err
	}
	return tx.Emit(ctx, "card.steered", &c.ID, store.System, map[string]any{"column": c.BoardColumn, "state": "blocked", "verdict": review.DecideAsk})
}

func validationInvalidText(number int32, invalid []string) string {
	return fmt.Sprintf("Study #%d's Validation found invalid statuses again after sending it back to Experimentation once: %s. "+
		"Comment with guidance to redo Validation, or close the study.", number, strings.Join(invalid, "; "))
}
