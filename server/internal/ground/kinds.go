// Package ground is the Grounding column: the records its roles write and the
// gate that checks them.
package ground

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"lykeion/server/internal/board"
	"lykeion/server/internal/records"
	"lykeion/server/internal/service"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

const (
	Scoper     = "scoper"
	Literature = "literature-scout"
	Contrarian = "contrarian"
	DataScout  = "data-scout"
	Methods    = "methods-scout"
	Reviewer   = "grounding-reviewer"
)

var Phases = [][]string{{Scoper}, {Literature, Contrarian, DataScout, Methods}}

type textBody struct {
	Text string `json:"text"`
}

type Evidence struct {
	Claim    string `json:"claim"`
	SourceID string `json:"source_id"`
	Quote    string `json:"quote"`
	Stance   string `json:"stance"`
}

type Dataset struct {
	Accession string `json:"accession"`
	Note      string `json:"note"`
	SHA256    string `json:"sha256,omitempty"`
	BlobKey   string `json:"blob_key,omitempty"`
}

type ReviewItem struct {
	Check  string `json:"check"`
	Result string `json:"result"`
	Choice string `json:"choice,omitempty"`
	Note   string `json:"note"`
}

type Verdict struct {
	Result string `json:"result"`
	Step   string `json:"step,omitempty"`
	To     string `json:"to,omitempty"`
	Reason string `json:"reason"`
}

type Fetch struct {
	SourceKind string `json:"source_kind,omitempty"`
	SourceURL  string `json:"source_url,omitempty"`
	Truncated  bool   `json:"truncated,omitempty"`
	SourceID   string `json:"source_id"`
	FullText   bool   `json:"full_text"`
	Retracted  bool   `json:"retracted"`
}

type Search struct {
	Database string `json:"database"`
	Query    string `json:"query"`
}

// Decode reads a record body strictly: an unknown field is refused.
func Decode[T any](raw json.RawMessage) (T, error) {
	var v T
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.DisallowUnknownFields()
	if err := d.Decode(&v); err != nil {
		return v, service.Invalid("bad body: " + err.Error())
	}
	return v, nil
}

func needText(raw json.RawMessage) error {
	b, err := Decode[textBody](raw)
	if err == nil && strings.TrimSpace(b.Text) == "" {
		return service.Invalid("text required")
	}
	return err
}

func liveRecord(ctx context.Context, tx *store.Tx, w service.Writer, kind, key string) (db.Record, bool, error) {
	recs, err := tx.Q.ListRecords(ctx, db.ListRecordsParams{WorkspaceID: tx.WS, CardID: w.Card, BoardColumn: w.Column})
	if err != nil {
		return db.Record{}, false, err
	}
	for i := len(recs) - 1; i >= 0; i-- {
		if recs[i].Kind == kind && recs[i].Key == key {
			return recs[i], true, nil
		}
	}
	return db.Record{}, false, nil
}

func prepareEvidence(ctx context.Context, tx *store.Tx, w service.Writer, raw json.RawMessage) (json.RawMessage, error) {
	e, err := Decode[Evidence](raw)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(e.Claim) == "" || e.SourceID == "" || strings.TrimSpace(e.Quote) == "" {
		return nil, service.Invalid("claim, source_id and quote are required")
	}
	switch {
	case w.Role == Literature && (e.Stance == "supports" || e.Stance == "challenges"):
	case w.Role == Contrarian && e.Stance == "challenges":
	default:
		return nil, service.Invalid(w.Role + " may not record " + e.Stance + " evidence")
	}
	if _, ok, err := liveRecord(ctx, tx, w, "fetch", e.SourceID); err != nil {
		return nil, err
	} else if !ok {
		return nil, service.Invalid(e.SourceID + " was never fetched through the sources server")
	}
	return raw, nil
}

func prepareDataset(ctx context.Context, tx *store.Tx, w service.Writer, raw json.RawMessage) (json.RawMessage, error) {
	d, err := Decode[Dataset](raw)
	if err != nil {
		return nil, err
	}
	o, ok, err := liveRecord(ctx, tx, w, "open_dataset", d.Accession)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, service.Invalid(d.Accession + " was never opened through the sources server")
	}
	d.SHA256 = records.Body[struct {
		SHA256 string `json:"sha256"`
	}](o).SHA256
	d.BlobKey = o.BlobKey
	return json.Marshal(d)
}

func validReviewItem(raw json.RawMessage) error {
	it, err := Decode[ReviewItem](raw)
	if err == nil && (it.Check == "" || (it.Result != "pass" && it.Result != "fail")) {
		return service.Invalid("check and a pass or fail result are required")
	}
	return err
}

func validVerdict(raw json.RawMessage) error {
	v, err := Decode[Verdict](raw)
	switch {
	case err != nil:
		return err
	case v.Result != "pass" && v.Result != "fail":
		return service.Invalid("result must be pass or fail")
	case v.Result == "fail" && strings.TrimSpace(v.Reason) == "":
		return service.Invalid("a failing verdict needs a reason")
	}
	return nil
}

func needReason(raw json.RawMessage) error {
	b, err := Decode[struct {
		Reason string `json:"reason"`
	}](raw)
	if err == nil && strings.TrimSpace(b.Reason) == "" {
		return service.Invalid("a reason is required")
	}
	return err
}

func prepareVerdict(_ context.Context, _ *store.Tx, w service.Writer, raw json.RawMessage) (json.RawMessage, error) {
	v, err := Decode[Verdict](raw)
	if err != nil {
		return nil, err
	}
	if v.To != "" {
		if err := board.RouteTarget(w.Column, v.To); err != nil {
			return nil, service.Invalid("to: " + err.Error() + "; leave it empty to redo work in this stage")
		}
	}
	return raw, nil
}

func Kinds() map[string]service.Kind {
	return map[string]service.Kind{
		"question":       {Roles: []string{Scoper}, Validate: needText},
		"notes":          {Roles: []string{Methods}, Validate: needText},
		"evidence":       {Roles: []string{Literature, Contrarian}, KeyPrefix: "E", Prepare: prepareEvidence},
		"dataset":        {Roles: []string{DataScout}, KeyPrefix: "D", Prepare: prepareDataset},
		"cannot_proceed": {Roles: []string{Scoper}, Validate: needReason},
		"review_item":    {Roles: []string{Reviewer}, Validate: validReviewItem},
		"verdict":        {Roles: []string{Reviewer}, Validate: validVerdict, Prepare: prepareVerdict},
	}
}

func RoleKinds(role string) []string {
	var out []string
	for name, k := range Kinds() {
		if slices.Contains(k.Roles, role) {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}
