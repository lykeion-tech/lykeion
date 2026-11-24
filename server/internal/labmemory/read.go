package labmemory

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"lykeion/server/internal/fence"
	"lykeion/server/internal/report"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

var ErrNotFound = errors.New("lab fact not found")

// Fact is authorized evidence; manual decisions are creator-private evidence.
// Maintenance reads contain only workspace-shared evidence. Study facts include
// the assigned Team's
// id/name/stage/column already visible in the study directory to all members.
// Team definitions include active shipped/workspace-shared manifests only, even
// for an uploader; private manifests/methods are unavailable through this API.
// Shared facts have one baseline for citation versions and lesson maintenance.
// Private manual decisions are available only through an authorized member Read.
type Fact struct {
	Ref         Ref
	Title, Text string
	Version     string
	Links       []Ref
	Superseded  bool
}

// Citation pins a workspace-shared lesson to the exact shared fact baseline.
type Citation struct {
	Ref     string `json:"ref"`
	Version string `json:"version"`
}

func CanOpen(ctx context.Context, tx *store.Tx, user uuid.UUID, r Ref) (bool, error) {
	_, err := Read(ctx, tx, user, r)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

func Read(ctx context.Context, tx *store.Tx, user uuid.UUID, r Ref) (Fact, error) {
	if tx == nil || tx.Q == nil || tx.WS == uuid.Nil || user == uuid.Nil {
		return Fact{}, ErrNotFound
	}
	allowed, err := tx.Q.LabAsker(ctx, db.LabAskerParams{WorkspaceID: tx.WS, UserID: user})
	if err != nil {
		return Fact{}, err
	}
	if !allowed {
		return Fact{}, ErrNotFound
	}
	if r.Kind == "decision" {
		row, err := tx.Q.LabManualFact(ctx, r.ID)
		if err == nil {
			return project(ctx, tx, r, db.LabFactRow(row))
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return Fact{}, err
		}
	}
	return read(ctx, tx, r)
}

func load(ctx context.Context, tx *store.Tx, r Ref) (db.LabFactRow, error) {
	parsed, err := ParseRef(r.String())
	if err != nil || parsed != r {
		return db.LabFactRow{}, ErrNotFound
	}
	row, err := tx.Q.LabFact(ctx, db.LabFactParams{WorkspaceID: tx.WS, Kind: r.Kind, ID: r.ID})
	if errors.Is(err, pgx.ErrNoRows) {
		return row, ErrNotFound
	}
	return row, err
}

func read(ctx context.Context, tx *store.Tx, r Ref) (Fact, error) {
	if r.Kind == "report" {
		return readReport(ctx, tx, r)
	}
	if r.Kind == "lesson" && !tx.LessonsEnabled {
		return Fact{}, ErrNotFound
	}
	row, err := load(ctx, tx, r)
	if err != nil {
		return Fact{}, err
	}
	return project(ctx, tx, r, row)
}

func project(ctx context.Context, tx *store.Tx, r Ref, row db.LabFactRow) (Fact, error) {
	f := Fact{Ref: r, Title: row.Title, Text: row.Text, Version: row.Version, Superseded: row.Superseded}
	var links []string
	if err := json.Unmarshal(row.Links, &links); err != nil {
		return Fact{}, fmt.Errorf("fact links: %w", err)
	}
	for _, s := range links {
		link, err := ParseRef(s)
		if err != nil {
			return Fact{}, err
		}
		f.Links = append(f.Links, link)
	}
	// Projection versions cover every exposed field and link. The shared
	// baseline is identical for every eligible member and maintenance caller;
	// private Team definitions are excluded rather than mixed into shared citations.
	if r.Kind == "study" || r.Kind == "workspace" || r.Kind == "decision" {
		evidence, _ := json.Marshal(struct {
			Title, Text, Version string
			Links                []Ref
		}{f.Title, f.Text, f.Version, f.Links})
		f.Version = fmt.Sprintf("sha256:%x", sha256.Sum256(evidence))
	}

	if r.Kind == "lesson" {
		var cites []Citation
		if err := json.Unmarshal(row.Cites, &cites); err != nil || len(cites) == 0 {
			return Fact{}, ErrNotFound
		}
		for _, cite := range cites {
			ref, err := ParseRef(cite.Ref)
			if err != nil || ref.Kind == "lesson" || ref.Kind == "report" || ref.Kind == "proposal" || cite.Version == "" {
				return Fact{}, ErrNotFound
			}
			fact, err := read(ctx, tx, ref)
			if err != nil {
				return Fact{}, err
			}
			if fact.Version != cite.Version {
				return Fact{}, ErrNotFound
			}
			f.Links = append(f.Links, ref)
		}
	}
	return f, nil
}

// Version is an internal workspace-scoped primitive for version validation.
// It does not establish membership and must not be exposed as a public read tool.
// Call Read for evidence returned to an asker. Maintenance callers must first
// explicitly select their workspace on the transaction. Its fact baseline and
// versions match member Read results for shared facts. Private manual decisions
// have no maintenance version.
func Version(ctx context.Context, tx *store.Tx, r Ref) (string, error) {
	// A report is per-study evidence and never backs a workspace-shared lesson.
	if tx == nil || tx.Q == nil || tx.WS == uuid.Nil || r.Kind == "report" {
		return "", ErrNotFound
	}
	fact, err := read(ctx, tx, r)
	if err != nil {
		return "", err
	}
	return fact.Version, nil
}

// ReadMaintenance is an internal privileged shared-fact read. It requires an
// explicit workspace selected by the maintenance caller; no asker is impersonated.
// It uses the shared projection and excludes private manual decisions.
func ReadMaintenance(ctx context.Context, tx *store.Tx, r Ref) (Fact, error) {
	if tx == nil || !tx.IsMaintenance() || tx.Q == nil || tx.WS == uuid.Nil || r.Kind == "report" {
		return Fact{}, ErrNotFound
	}
	return read(ctx, tx, r)
}

// reportGraph is the part of a study's report that Aristotle reads: its
// nodes, the facts Jev accepted on its concepts, and its edges.
type reportGraph struct {
	Nodes     []reportNode  `json:"nodes"`
	Edges     []report.Edge `json:"edges"`
	Truncated bool          `json:"truncated,omitempty"`
}

type reportNode struct {
	ID     string        `json:"id"`
	Kind   string        `json:"kind"`
	Label  string        `json:"label"`
	Status string        `json:"status,omitempty"`
	Quote  string        `json:"quote,omitempty"`
	Facts  []report.Fact `json:"facts,omitempty"`
}

// readReport opens a study's report only for a reader who can open the study,
// and only when the study has the report its report page shows. A ref with a
// node reads that node alone.
func readReport(ctx context.Context, tx *store.Tx, r Ref) (Fact, error) {
	if _, err := read(ctx, tx, Ref{Kind: "study", ID: r.ID}); err != nil {
		return Fact{}, err
	}
	card, err := tx.Q.GetCard(ctx, db.GetCardParams{WorkspaceID: tx.WS, ID: r.ID})
	if errors.Is(err, pgx.ErrNoRows) {
		return Fact{}, ErrNotFound
	}
	if err != nil {
		return Fact{}, err
	}
	has, err := report.Has(ctx, tx, card)
	if err != nil {
		return Fact{}, err
	}
	if !has {
		return Fact{}, ErrNotFound
	}
	in, err := report.Load(ctx, tx, card)
	if err != nil {
		return Fact{}, err
	}
	rep := report.Build(in)
	g := reportGraph{Nodes: []reportNode{}, Edges: rep.Edges}
	for _, n := range rep.Nodes {
		g.Nodes = append(g.Nodes, reportNode{n.ID, n.Kind, n.Label, n.Status, n.Quote, n.Facts})
	}
	// The stored graph is exactly the page the read tool shows, so nothing
	// past the page can be cited.
	g = onePage(g)
	f := Fact{Ref: r, Title: fmt.Sprintf("Report of study #%d", card.Number), Links: []Ref{{Kind: "study", ID: r.ID}}}
	if r.Node != "" {
		i := slices.IndexFunc(g.Nodes, func(n reportNode) bool { return n.ID == r.Node })
		if i < 0 {
			return Fact{}, ErrNotFound
		}
		f.Title += " › " + g.Nodes[i].Label
		g = reportGraph{Nodes: g.Nodes[i : i+1], Edges: []report.Edge{}}
	}
	text, err := json.Marshal(g)
	if err != nil {
		return Fact{}, err
	}
	f.Text = string(text)
	f.Version = fmt.Sprintf("sha256:%x", sha256.Sum256(text))
	return f, nil
}

// fenceQuotes returns n with every quote fenced as untrusted data.
func fenceQuotes(n reportNode) reportNode {
	if n.Quote != "" {
		n.Quote = fence.Untrusted("Recorded text", n.Quote)
	}
	n.Facts = slices.Clone(n.Facts)
	for i := range n.Facts {
		n.Facts[i].Quote = fence.Untrusted("Quoted evidence", n.Facts[i].Quote)
	}
	return n
}

// onePage keeps nodes, then edges, while their fenced form fits one page.
// A fence's size does not depend on its nonce, so the measure is exact.
func onePage(g reportGraph) reportGraph {
	out := reportGraph{Nodes: []reportNode{}, Edges: []report.Edge{}}
	budget := pageSize - 64 // the envelope and the truncated flag
	fits := func(v any) bool {
		b, _ := json.Marshal(v)
		if len(b)+1 > budget {
			out.Truncated = true
			return false
		}
		budget -= len(b) + 1
		return true
	}
	for _, n := range g.Nodes {
		if !fits(fenceQuotes(n)) {
			break
		}
		out.Nodes = append(out.Nodes, n)
	}
	for _, e := range g.Edges {
		if out.Truncated || !fits(e) {
			break
		}
		out.Edges = append(out.Edges, e)
	}
	return out
}

// reportPage is what the read tool returns for a report: the stored page
// with every quote fenced.
func reportPage(text string) (string, error) {
	var g reportGraph
	if err := json.Unmarshal([]byte(text), &g); err != nil {
		return "", err
	}
	for i, n := range g.Nodes {
		g.Nodes[i] = fenceQuotes(n)
	}
	b, err := json.Marshal(g)
	return string(b), err
}
