package pack

import (
	"context"
	"fmt"
	"io/fs"

	"lykeion/server/internal/storage"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

// InstallShipped gives the transaction's workspace a row for every shipped
// pack it lacks. A workspace Team that already holds a shipped name is renamed
// "<name> (custom)" first, since a workspace keeps one row per name. It takes
// the workspace lock, so concurrent installs leave one row.
func InstallShipped(ctx context.Context, tx *store.Tx, blobs storage.Storage, shipped fs.FS) error {
	names := ShippedNames(shipped)
	if len(names) == 0 {
		return nil
	}
	if err := tx.Q.LockWorkspaceEvents(ctx, tx.WS); err != nil {
		return err
	}
	for _, name := range names {
		have, err := tx.Q.ShippedPackExists(ctx, db.ShippedPackExistsParams{WorkspaceID: tx.WS, Name: name})
		if err != nil {
			return err
		}
		if have {
			continue
		}
		zipBytes, p, err := ReadShipped(shipped, name)
		if err != nil {
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
