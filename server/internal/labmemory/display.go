package labmemory

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

type DisplayRef struct {
	Ref     string       `json:"ref"`
	Title   string       `json:"title"`
	Via     string       `json:"via,omitempty"`
	StudyID *uuid.UUID   `json:"study_id,omitempty"`
	Facts   []DisplayRef `json:"facts,omitempty"`
}

type DisplayStatement struct {
	Kind  string       `json:"kind"`
	Text  string       `json:"text"`
	Cites []DisplayRef `json:"cites"`
}

// DisplayReader resolves this page against one member snapshot. A gap can
// repeat any earlier answer used at launch, so check those dependencies too.
type DisplayReader struct {
	tx          *store.Tx
	asker, chat uuid.UUID
	facts       map[string]*Fact
	safe        map[uuid.UUID]bool
	changed     map[uuid.UUID]bool
}

func NewDisplayReader(tx *store.Tx, asker, chat uuid.UUID) *DisplayReader {
	return &DisplayReader{tx: tx, asker: asker, chat: chat, facts: map[string]*Fact{}, safe: map[uuid.UUID]bool{}, changed: map[uuid.UUID]bool{}}
}

func (d *DisplayReader) fact(ctx context.Context, s string) (*Fact, error) {
	if f, ok := d.facts[s]; ok {
		return f, nil
	}
	ref, err := ParseRef(s)
	if err != nil {
		return nil, nil
	}
	f, err := Read(ctx, d.tx, d.asker, ref)
	if errors.Is(err, ErrNotFound) {
		d.facts[s] = nil
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	d.facts[s] = &f
	return &f, nil
}

func (d *DisplayReader) visible(ctx context.Context, id uuid.UUID) (bool, error) {
	if ok, seen := d.safe[id]; seen {
		return ok, nil
	}
	d.safe[id] = false // malformed cycles fail closed
	row, err := d.tx.Q.GetPrivateAnswerSources(ctx, db.GetPrivateAnswerSourcesParams{ChatID: d.chat, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if row.LaunchSources == nil {
		return false, nil
	}
	for _, src := range row.LaunchSources {
		ok, err := d.visible(ctx, src)
		if err != nil || !ok {
			return false, err
		}
	}
	_, ok, changed, err := checkedHistory(db.LabPriorTurnsRow(row), func(e answerEvidence) (bool, bool, error) {
		f, err := d.fact(ctx, e.Ref)
		if f == nil || err != nil {
			return false, false, err
		}
		return stands(ctx, d.tx, *f, e)
	})
	for _, src := range row.LaunchSources {
		changed = changed || d.changed[src]
	}
	d.safe[id], d.changed[id] = ok, changed
	return ok, err
}

// Changed reports whether an answer already checked by Answer rests on sources
// that have changed since it was written, directly or through earlier answers.
func (d *DisplayReader) Changed(id uuid.UUID) bool { return d.changed[id] }

// The study route is derived from the current readable projection. The client
// never has to search other studies or infer the owner of a record.
func displayRef(f *Fact) DisplayRef {
	result := DisplayRef{Ref: f.Ref.String(), Title: f.Title}
	switch f.Ref.Kind {
	case "study", "report":
		result.StudyID = &f.Ref.ID
	case "record", "comment", "decision":
		for _, link := range f.Links {
			if link.Kind == "study" {
				id := link.ID
				result.StudyID = &id
				break
			}
		}
	}
	return result
}

func (d *DisplayReader) Answer(ctx context.Context, m db.LabMessage) ([]DisplayStatement, []DisplayRef, bool, error) {
	safe, err := d.visible(ctx, m.ID)
	if !safe || err != nil {
		return nil, nil, false, err
	}
	var statements []answerStatement
	var evidence []answerEvidence
	if err = json.Unmarshal(m.Statements, &statements); err != nil {
		return nil, nil, false, err
	}
	if err = json.Unmarshal(m.Evidence, &evidence); err != nil {
		return nil, nil, false, err
	}
	ss := make([]DisplayStatement, 0, len(statements))
	for _, s := range statements {
		stmt := DisplayStatement{Kind: s.Kind, Text: s.Text, Cites: []DisplayRef{}}
		for _, c := range s.Cites {
			ref := displayRef(d.facts[c.Ref])
			if s.Kind == "lesson" {
				for _, link := range d.facts[c.Ref].Links {
					f, err := d.fact(ctx, link.String())
					if err != nil || f == nil {
						return nil, nil, false, err
					}
					ref.Facts = append(ref.Facts, displayRef(f))
				}
			}
			stmt.Cites = append(stmt.Cites, ref)
		}
		ss = append(ss, stmt)
	}
	ee := make([]DisplayRef, 0, len(evidence))
	for _, e := range evidence {
		switch e.Via {
		case "search", "links", "read":
		default:
			f, err := d.fact(ctx, e.Via)
			if err != nil || f == nil {
				return nil, nil, false, err
			}
		}
		ref := displayRef(d.facts[e.Ref])
		ref.Via = e.Via
		ee = append(ee, ref)
	}
	return ss, ee, true, nil
}
