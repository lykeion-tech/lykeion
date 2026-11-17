package flow

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"path"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/handin"
	"lykeion/server/internal/jev"
	"lykeion/server/internal/records"
	"lykeion/server/internal/report"
	"lykeion/server/internal/review"
	"lykeion/server/internal/sources"
	"lykeion/server/internal/storage"
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
// fails the build with nothing stored. A study whose owner saved no TypeSafe
// key keeps its report with no concepts, facts or links. A queued run whose pinned runtime is
// gone can never be claimed, so it is cancelled and the build fails.
func (e *Engine) judgeReport(ctx context.Context, ws, card uuid.UUID) error {
	var in report.Input
	var h handin.ReportHandIn
	var owner uuid.UUID
	ready := false
	err := e.Store.InWorkspace(ctx, ws, uuid.Nil, func(tx *store.Tx) error {
		c, err := lockCard(ctx, tx, card)
		if err != nil || c.ID == uuid.Nil {
			return err
		}
		owner = c.OwnerID
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
		if h, probs = handin.ParseReport(raw); raw == nil || len(probs) > 0 || h.Status != "done" {
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
	var judged report.Judged
	var jerr error
	if j := e.JevFor(ctx, owner); j != nil {
		judged, jerr = report.Judge(ctx, retryAsker{&reviewPass{e: e, ws: ws, jev: j, retry: e.retry(), renewed: e.now()}}, rep, report.Ground(rep, h))
	}
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
	var j *jev.Client
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
		if j = e.JevFor(ctx, c.OwnerID); j == nil {
			_, err = tx.Q.SetReportChecksStatus(ctx, db.SetReportChecksStatusParams{ChecksStatus: "unavailable", WorkspaceID: ws, CardID: card})
			return err
		}
		in, err = report.Load(ctx, tx, c)
		return err
	})
	if err != nil || j == nil {
		return err
	}
	rep := report.Build(in)
	ev, err := e.reportEvidence(ctx, in, rep)
	if err != nil {
		return err
	}
	checks, jerr := report.CheckFindings(ctx, retryAsker{&reviewPass{e: e, ws: ws, jev: j, retry: e.retry(), renewed: e.now()}}, rep, ev)
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
					Question: k.Question, Choice: k.Choice, P: k.P, Status: k.Status, Evidence: k.Evidence, Basis: k.Basis}); err != nil {
					return err
				}
			}
		}
		_, err = tx.Q.SetReportChecksStatus(ctx, db.SetReportChecksStatusParams{ChecksStatus: status, WorkspaceID: ws, CardID: card})
		return err
	})
}

// maxImageRead is the most of a saved image read to show Jev.
// ponytail: larger images are judged from text or script instead; raise it if real figures exceed it.
const maxImageRead = 32 << 20

// reportEvidence is what the report's checks are judged from: each output
// node's saved text, its image when it is a PNG or JPEG that supports a
// finding and fits Jev, and the script that produced it; and under
// "paper:<node id>" each paper's text as the calls it was fetched in
// recorded it.
func (e *Engine) reportEvidence(ctx context.Context, in report.Input, rep report.Report) (report.Evidence, error) {
	ev := report.Evidence{Images: map[string]jev.Image{}, Texts: map[string]string{}, Scripts: map[string]string{}}
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
		return ev, err
	}
	supports := map[string]bool{}
	for _, ed := range rep.Edges {
		if ed.Kind == "supports" {
			supports[ed.From] = true
		}
	}
	// The newest output under a name is its node, as the report keeps it.
	newest := map[string]db.Record{}
	for _, r := range outputs {
		newest["out:"+records.Body[struct{ Name string }](r).Name] = r
	}
	for id, r := range newest {
		if f, ok := files[r.Key]; ok {
			ev.Texts[id] = f
		}
		if !supports[id] {
			continue
		}
		body := records.Body[struct {
			Name        string `json:"name"`
			ContentType string `json:"content_type"`
		}](r)
		if format := report.ImageFormat(body.ContentType); format != "" {
			im, err := e.outputImage(ctx, r, format)
			if err != nil {
				return ev, err
			}
			if im != nil {
				ev.Images[id] = *im
			}
		}
		if s := producingScript(in.Records, r, body.Name); s != "" {
			ev.Scripts[id] = s
		}
	}
	if e.Sources == nil {
		return ev, nil
	}
	retry := e.retry()
	for _, n := range rep.Nodes {
		if n.Kind != "paper" {
			continue
		}
		id := records.Body[review.PaperBody](byKey[strings.TrimPrefix(n.ID, "rec:")]).ID
		if abstract := e.paperAbstract(ctx, retry, id); abstract != "" {
			ev.Texts["paper:"+n.ID] = abstract
		}
	}
	return ev, nil
}

// paperAbstract is the abstract the sources server holds for paper id,
// asked again as stage review asks Jev again. It is "" when the paper or its
// abstract cannot be found, so that paper is judged against nothing else.
func (e *Engine) paperAbstract(ctx context.Context, retry *jev.Retry, id string) string {
	if id == "" {
		return ""
	}
	var h sources.Hit
	err := retry.Do(ctx, func() (err error) {
		h, err = e.Sources.Paper(ctx, id)
		if errors.Is(err, sources.ErrNotFound) {
			return nil
		}
		return err
	})
	if err != nil {
		slog.Warn("report novelty: paper lookup failed", "paper", id, "err", err)
		return ""
	}
	return strings.TrimSpace(h.Abstract)
}

// outputImage is output r's image fitted for Jev, or nil when it is gone, too
// large, or not a decodable image of format.
func (e *Engine) outputImage(ctx context.Context, r db.Record, format string) (*jev.Image, error) {
	if r.BlobKey == "" || e.Blobs == nil || records.Body[struct {
		SizeBytes int64 `json:"size_bytes"`
	}](r).SizeBytes > maxImageRead {
		return nil, nil
	}
	rc, err := e.Blobs.Get(ctx, r.BlobKey)
	if errors.Is(err, storage.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	b, err := io.ReadAll(io.LimitReader(rc, maxImageRead+1))
	rc.Close()
	if err != nil {
		return nil, err
	}
	if len(b) > maxImageRead {
		return nil, nil
	}
	im, err := jev.FitImage(b, format)
	if err != nil {
		slog.Info("report figure not shown as an image", "record", r.Key, "err", err)
		return nil, nil
	}
	return &im, nil
}

// producingScript is the code of the run records of output's agent run that
// name the file, cut to review.MaxState, or "" when none does.
func producingScript(recs []db.Record, output db.Record, name string) string {
	if output.AgentRunID == nil {
		return ""
	}
	var named []string
	for _, r := range recs {
		if r.Kind != "run" || r.SupersededAt != nil || r.AgentRunID == nil || *r.AgentRunID != *output.AgentRunID {
			continue
		}
		b := records.Body[struct{ Path, Script, Code string }](r)
		code := b.Script + b.Code
		if strings.TrimSpace(code) == "" {
			continue
		}
		if b.Path != "" {
			code = b.Path + "\n" + code
		}
		if strings.Contains(code, path.Base(name)) {
			named = append(named, code)
		}
	}
	s, _ := Excerpt(strings.Join(named, "\n\n"), review.MaxState)
	return s
}
