package pack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"

	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/storage"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

// InstallShipped gives the transaction's workspace a row for every shipped
// pack it lacks, and brings a row whose pack has changed up to date. A
// workspace Team that already holds a shipped name is renamed "<name>
// (custom)" first, since a workspace keeps one row per name. It takes the
// workspace lock, so concurrent installs leave one row.
func InstallShipped(ctx context.Context, tx *store.Tx, blobs storage.Storage, shipped fs.FS) error {
	names := ShippedNames(shipped)
	if len(names) == 0 {
		return nil
	}
	if err := tx.Q.LockWorkspaceEvents(ctx, tx.WS); err != nil {
		return err
	}
	for _, name := range names {
		zipBytes, p, err := ReadShipped(shipped, name)
		if err != nil {
			return err
		}
		row, err := tx.Q.ShippedPack(ctx, db.ShippedPackParams{WorkspaceID: tx.WS, Name: name})
		if err == nil {
			if row.DeletedAt == nil && row.Sha256 != p.SHA256 {
				if err := refreshShipped(ctx, tx, blobs, row, zipBytes, p); err != nil {
					return err
				}
			}
			continue
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		renamed := name + " (custom)"
		for n := 2; ; n++ {
			used, err := tx.Q.PackNameInUse(ctx, db.PackNameInUseParams{WorkspaceID: tx.WS, Name: renamed})
			if err != nil {
				return err
			}
			if !used {
				break
			}
			renamed = fmt.Sprintf("%s (custom %d)", name, n)
		}
		if err := tx.Q.RenameFieldPack(ctx, db.RenameFieldPackParams{WorkspaceID: tx.WS, Name: name, NewName: renamed}); err != nil {
			return err
		}
		key, err := PutBlob(ctx, blobs, tx.WS, zipBytes, p.SHA256)
		if err != nil {
			return err
		}
		if _, err := Insert(ctx, tx, key, p, nil); err != nil {
			return err
		}
	}
	return nil
}

// refreshShipped points a shipped row at the pack Lykeion now ships. Shipped
// Teams are read-only, so the row holds no edits to lose. Each agent added
// from a member takes the member's new instructions only while it still holds
// the old ones: an owner's edit stands.
func refreshShipped(ctx context.Context, tx *store.Tx, blobs storage.Storage, row db.FieldPack, zipBytes []byte, p *Pack) error {
	var old Manifest
	if err := json.Unmarshal(row.Manifest, &old); err != nil {
		return fmt.Errorf("pack: stored manifest of %s: %w", row.Name, err)
	}
	manifest, err := checkedManifest(ctx, tx, p)
	if err != nil {
		return err
	}
	key, err := PutBlob(ctx, blobs, tx.WS, zipBytes, p.SHA256)
	if err != nil {
		return err
	}
	m := p.Manifest
	if err := tx.Q.RefreshShippedPack(ctx, db.RefreshShippedPackParams{Version: m.Version, Sha256: p.SHA256, Manifest: manifest,
		BlobKey: key, Domain: m.Domain, Stage: m.Stage, WorkspaceID: tx.WS, ID: row.ID}); err != nil {
		return err
	}
	// Added agents are linked to their member by its name.
	was := map[string]string{}
	for _, mem := range append([]Member{old.Leader, old.Auditor}, old.Members...) {
		was[mem.Name] = mem.Agent.Instructions
	}
	for _, mem := range append([]Member{m.Leader, m.Auditor}, m.Members...) {
		before, ok := was[mem.Name]
		if !ok || before == mem.Agent.Instructions {
			continue
		}
		if err := tx.Q.RefreshTeamAgentInstructions(ctx, db.RefreshTeamAgentInstructionsParams{NewInstructions: mem.Agent.Instructions,
			WorkspaceID: tx.WS, PackID: row.ID, Member: mem.Name, OldInstructions: before}); err != nil {
			return err
		}
	}
	return nil
}
