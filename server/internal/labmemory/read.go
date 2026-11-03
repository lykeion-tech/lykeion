package labmemory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

var ErrNotFound = errors.New("lab fact not found")

type Fact struct {
	Ref         Ref
	Title, Text string
	Version     string
	Links       []Ref
	Superseded  bool
}

// Citation pins a lesson to the exact live version of a fact.
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
	if _, err := tx.Q.GetMember(ctx, db.GetMemberParams{WorkspaceID: tx.WS, UserID: user}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Fact{}, ErrNotFound
		}
		return Fact{}, err
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
	row, err := load(ctx, tx, r)
	if err != nil {
		return Fact{}, err
	}
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
	if r.Kind == "lesson" {
		var cites []Citation
		if err := json.Unmarshal(row.Cites, &cites); err != nil || len(cites) == 0 {
			return Fact{}, ErrNotFound
		}
		for _, cite := range cites {
			ref, err := ParseRef(cite.Ref)
			if err != nil || ref.Kind == "lesson" || cite.Version == "" {
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
// explicitly select their workspace on the transaction.
func Version(ctx context.Context, tx *store.Tx, r Ref) (string, error) {
	if tx == nil || tx.Q == nil || tx.WS == uuid.Nil {
		return "", ErrNotFound
	}
	row, err := load(ctx, tx, r)
	if err != nil {
		return "", err
	}
	return row.Version, nil
}
