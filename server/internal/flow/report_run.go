package flow

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/handin"
	"lykeion/server/internal/records"
	"lykeion/server/internal/report"
	"lykeion/server/internal/review"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

// goReport runs f for a study's report off the caller's goroutine, one at a
// time per study and kind of work, so a slow Jev pass is not started twice.
// ponytail: the guard is per process; two servers can still both judge one
// report, and the status check before writing keeps that to wasted questions.
func (e *Engine) goReport(kind string, ws, card uuid.UUID, f func(context.Context, uuid.UUID, uuid.UUID) error) {
	key := kind + ":" + card.String()
	if _, busy := e.reporting.LoadOrStore(key, true); busy {
		return
	}
	run := func() {
		defer e.reporting.Delete(key)
		if err := f(context.Background(), ws, card); err != nil {
			slog.Error(kind, "card", card, "err", err)
		}
	}
	if e.Inline {
		run()
		return
	}
	go run()
}

// resumeReports picks up report builds a restart or a dropped kick left
// unfinished: it judges those whose run settled, and runs pending checks.
func (e *Engine) resumeReports(ctx context.Context) error {
	rows, err := e.Store.Global().ReportBuildsToResume(ctx)
	if err != nil {
		return err
	}
	for _, r := range rows {
		if r.Judging {
			e.goReport("judge report", r.WorkspaceID, r.CardID, e.judgeReport)
		}
		e.goReport("check report", r.WorkspaceID, r.CardID, e.checkReport)
	}
	return nil
}

// setBuild settles a build that is still queued or judging.
func setBuild(ctx context.Context, tx *store.Tx, b db.ReportBuild, status string) error {
	if b.Status != "queued" && b.Status != "judging" {
		return nil
	}
	_, err := tx.Q.SetReportBuildStatus(ctx, db.SetReportBuildStatusParams{Status: status, WorkspaceID: tx.WS, CardID: b.CardID})
	return err
}

// judgeReport turns a settled report run's hand-in into the report's
// concepts, facts and links: those its quotes and phrases ground in the
// study's text and Jev then accepts. Jev is asked with no transaction open. A
// run that failed or could not do the work, or a Jev that cannot answer,
// fails the build with nothing stored. A queued run whose pinned runtime is
// gone can never be claimed, so it is cancelled and the build fails.
func (e *Engine) judgeReport(ctx context.Context, ws, card uuid.UUID) error {
	var in report.Input
	var h handin.ReportHandIn
	ready := false
	err := e.Store.InWorkspace(ctx, ws, uuid.Nil, func(tx *store.Tx) error {
		c, err := lockCard(ctx, tx, card)
		if err != nil || c.ID == uuid.Nil {
			return err
		}
		b, err := tx.Q.GetReportBuild(ctx, db.GetReportBuildParams{WorkspaceID: ws, CardID: card})
		if errors.Is(err, pgx.ErrNoRows) || err == nil && b.Status != "queued" && b.Status != "judging" {
			return nil
		}
		if err != nil {
			return err
		}
		if b.AgentRunID == nil {
			return setBuild(ctx, tx, b, "failed")
		}
		run, err := tx.Q.LockAgentRun(ctx, db.LockAgentRunParams{WorkspaceID: ws, ID: *b.AgentRunID})
		if errors.Is(err, pgx.ErrNoRows) {
			return setBuild(ctx, tx, b, "failed")
		}
		if err != nil {
			return err
		}
		switch run.Status {
		case "completed":
		case "failed", "cancelled":
			return setBuild(ctx, tx, b, "failed")
		case "queued":
			if run.PinnedRuntimeID != nil {
				return nil
			}
			if err := e.AgentRuns.CancelInTx(ctx, tx, uuid.Nil, run); err != nil {
				return err
			}
			return setBuild(ctx, tx, b, "failed")
		default:
			return nil
		}
		raw, err := reportHandIn(ctx, tx, run)
		if err != nil {
			return err
		}
		var probs []string
		if h, probs = handin.ParseReport(raw); raw == nil || len(probs) > 0 || h.Status != "done" || e.Jev == nil {
			return setBuild(ctx, tx, b, "failed")
		}
		if in, err = report.Load(ctx, tx, c); err != nil {
			return err
		}
		ready = true
		return setBuild(ctx, tx, b, "judging")
	})
	if err != nil || !ready {
		return err
	}
	rep := report.Build(in)
	judged, jerr := report.Judge(ctx, retryAsker{&reviewPass{e: e, ws: ws, retry: e.retry(), renewed: e.now()}}, rep, report.Ground(rep, h))
	if jerr != nil {
		slog.Warn("report judging unavailable", "card", card, "err", jerr)
	}
	return e.Store.InWorkspace(ctx, ws, uuid.Nil, func(tx *store.Tx) error {
		c, err := lockCard(ctx, tx, card)
		if err != nil || c.ID == uuid.Nil {
			return err
		}
		b, err := tx.Q.GetReportBuild(ctx, db.GetReportBuildParams{WorkspaceID: ws, CardID: card})
		if errors.Is(err, pgx.ErrNoRows) || err == nil && b.Status != "judging" {
			return nil
		}
		if err != nil {
			return err
		}
		if jerr != nil {
			return setBuild(ctx, tx, b, "failed")
		}
		if err := storeJudged(ctx, tx, card, judged); err != nil {
			return err
		}
		return setBuild(ctx, tx, b, "done")
	})
}

// reportHandIn is the hand-in a report run recorded for its attempt, or nil.
func reportHandIn(ctx context.Context, tx *store.Tx, run db.AgentRun) (json.RawMessage, error) {
	recs, err := tx.Q.AgentRunRecords(ctx, db.AgentRunRecordsParams{WorkspaceID: tx.WS, AgentRunID: &run.ID})
	if err != nil {
		return nil, err
	}
	for _, r := range recs {
		b := records.Body[struct {
			HandIn  json.RawMessage `json:"hand_in"`
			Attempt int32           `json:"attempt"`
		}](r)
		if r.Kind == "hand_in" && r.SupersededAt == nil && b.Attempt == run.Attempt {
			return b.HandIn, nil
		}
	}
	return nil, nil
}

// storeJudged replaces the report's concepts, facts and links with j.
func storeJudged(ctx context.Context, tx *store.Tx, card uuid.UUID, j report.Judged) error {
	ws := tx.WS
	if err := tx.Q.DeleteReportConcepts(ctx, db.DeleteReportConceptsParams{WorkspaceID: ws, CardID: card}); err != nil {
		return err
	}
	if err := tx.Q.DeleteReportFacts(ctx, db.DeleteReportFactsParams{WorkspaceID: ws, CardID: card}); err != nil {
		return err
	}
	if err := tx.Q.DeleteReportLinks(ctx, db.DeleteReportLinksParams{WorkspaceID: ws, CardID: card}); err != nil {
		return err
	}
	for _, c := range j.Concepts {
		if err := tx.Q.InsertReportConcept(ctx, db.InsertReportConceptParams{WorkspaceID: ws, CardID: card, Key: c.Key, Label: c.Label, Type: c.Type}); err != nil {
			return err
		}
	}
	for _, f := range j.Facts {
		if err := tx.Q.InsertReportFact(ctx, db.InsertReportFactParams{WorkspaceID: ws, CardID: card, Subject: f.Subject, Relation: f.Relation,
			Object: f.Object, Quote: f.Quote, SourceNode: f.SourceNode, P: f.P}); err != nil {
			return err
		}
	}
	for _, l := range j.Links {
		if err := tx.Q.InsertReportLink(ctx, db.InsertReportLinkParams{WorkspaceID: ws, CardID: card, StartByte: int32(l.Start), EndByte: int32(l.End),
			NodeID: l.Node, P: l.P}); err != nil {
			return err
		}
	}
	return nil
}

// checkReport asks Jev the report's figure, table and novelty checks, with
// no transaction open, and stores them. With no Jev, or one that cannot
// answer, the checks are unavailable and nothing is stored.
func (e *Engine) checkReport(ctx context.Context, ws, card uuid.UUID) error {
	var in report.Input
	ready := false
	err := e.Store.InWorkspace(ctx, ws, uuid.Nil, func(tx *store.Tx) error {
		c, err := lockCard(ctx, tx, card)
		if err != nil || c.ID == uuid.Nil {
			return err
		}
		b, err := tx.Q.GetReportBuild(ctx, db.GetReportBuildParams{WorkspaceID: ws, CardID: card})
		if errors.Is(err, pgx.ErrNoRows) || err == nil && b.ChecksStatus != "pending" {
			return nil
		}
		if err != nil {
			return err
		}
		if ready = e.Jev != nil; !ready {
			_, err = tx.Q.SetReportChecksStatus(ctx, db.SetReportChecksStatusParams{ChecksStatus: "unavailable", WorkspaceID: ws, CardID: card})
			return err
		}
		in, err = report.Load(ctx, tx, c)
		return err
	})
	if err != nil || !ready {
		return err
	}
	rep := report.Build(in)
	texts, err := e.reportTexts(ctx, in, rep)
	if err != nil {
		return err
	}
	checks, jerr := report.CheckFindings(ctx, retryAsker{&reviewPass{e: e, ws: ws, retry: e.retry(), renewed: e.now()}}, rep, texts)
	if jerr != nil {
		slog.Warn("report checks unavailable", "card", card, "err", jerr)
	}
	return e.Store.InWorkspace(ctx, ws, uuid.Nil, func(tx *store.Tx) error {
		c, err := lockCard(ctx, tx, card)
		if err != nil || c.ID == uuid.Nil {
			return err
		}
		b, err := tx.Q.GetReportBuild(ctx, db.GetReportBuildParams{WorkspaceID: ws, CardID: card})
		if errors.Is(err, pgx.ErrNoRows) || err == nil && b.ChecksStatus != "pending" {
			return nil
		}
		if err != nil {
			return err
		}
		status := "done"
		if jerr != nil {
			status = "unavailable"
		} else {
			if err := tx.Q.DeleteReportChecks(ctx, db.DeleteReportChecksParams{WorkspaceID: ws, CardID: card}); err != nil {
				return err
			}
			for _, k := range checks {
				if err := tx.Q.InsertReportCheck(ctx, db.InsertReportCheckParams{WorkspaceID: ws, CardID: card, NodeID: k.Node, CheckID: k.CheckID,
					Question: k.Question, Choice: k.Choice, P: k.P, Status: k.Status, Evidence: k.Evidence}); err != nil {
					return err
				}
			}
		}
		_, err = tx.Q.SetReportChecksStatus(ctx, db.SetReportChecksStatusParams{ChecksStatus: status, WorkspaceID: ws, CardID: card})
		return err
	})
}

// reportTexts is each output node's saved text, and under "paper:<node id>"
// each paper's text as the calls it was fetched in recorded it.
func (e *Engine) reportTexts(ctx context.Context, in report.Input, rep report.Report) (map[string]string, error) {
	var outputs []db.Record
	byKey := map[string]db.Record{}
	for _, r := range in.Records {
		if r.SupersededAt != nil {
			continue
		}
		byKey[r.Key] = r
		if r.Kind == "output" {
			outputs = append(outputs, r)
		}
	}
	files, err := e.outputTexts(ctx, outputs)
	if err != nil {
		return nil, err
	}
	texts := map[string]string{}
	for _, r := range outputs {
		// The newest output under a name is its node, as the report keeps it.
		id := "out:" + records.Body[struct{ Name string }](r).Name
		if f, ok := files[r.Key]; ok {
			texts[id] = f
		} else {
			delete(texts, id)
		}
	}
	sources := map[string][]db.Record{}
	for _, n := range rep.Nodes {
		if n.Kind != "paper" {
			continue
		}
		for _, k := range records.Body[review.PaperBody](byKey[strings.TrimPrefix(n.ID, "rec:")]).Cites {
			if r, ok := byKey[recordKey(k)]; ok {
				sources["paper:"+n.ID] = append(sources["paper:"+n.ID], r)
			}
		}
	}
	papers, err := e.sourceTexts(ctx, sources)
	if err != nil {
		return nil, err
	}
	for k, v := range papers {
		texts[k] = v
	}
	return texts, nil
}
