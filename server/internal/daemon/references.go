package daemon

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"lykeion/server/internal/apiclient"
)

func (d *daemon) references() []runtimeRef {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]runtimeRef(nil), d.refs...)
}

// A deleted runtime stays deleted for this process incarnation. Only newly
// joined workspaces register; stale memberships and runtime IDs are pruned.
func (d *daemon) refreshReferences(ctx context.Context) error {
	if d.id == "" {
		return nil
	}
	var workspaces []struct {
		ID uuid.UUID `json:"id"`
	}
	if err := d.api.Do(ctx, "GET", "/api/daemon/workspaces", nil, &workspaces); err != nil {
		return err
	}
	prior := d.references()
	var next []runtimeRef
	for _, ws := range workspaces {
		d.mu.Lock()
		known := d.knownWorkspaces[ws.ID]
		d.mu.Unlock()
		if !known {
			var rs []struct {
				ID uuid.UUID `json:"id"`
			}
			var harnesses []string
			for h := range d.cfg.Harnesses {
				harnesses = append(harnesses, h)
			}
			err := d.api.Do(ctx, "POST", "/api/daemon/register", map[string]any{"workspace_id": ws.ID, "daemon_id": d.id, "device_name": d.cfg.Device, "harnesses": harnesses, "versions": d.versions}, &rs)
			if err != nil {
				slog.Warn("workspace registration pending", "err", err)
				continue
			}
			d.mu.Lock()
			d.knownWorkspaces[ws.ID] = true
			d.mu.Unlock()
			for _, r := range rs {
				next = append(next, runtimeRef{ws.ID, r.ID})
			}
			continue
		}
		var rs []struct {
			ID       uuid.UUID `json:"id"`
			DaemonID string    `json:"daemon_id"`
			Harness  string    `json:"harness"`
		}
		api := *d.api
		api.Workspace = ws.ID.String()
		err := api.Do(ctx, "GET", "/api/runtimes", nil, &rs)
		if err != nil {
			var he *apiclient.HTTPError
			if !errors.As(err, &he) || he.Status != http.StatusNotFound {
				for _, ref := range prior {
					if ref.WorkspaceID == ws.ID {
						next = append(next, ref)
					}
				}
			}
			continue
		}
		for _, r := range rs {
			if r.DaemonID == d.id && d.cfg.Harnesses[r.Harness] != "" {
				next = append(next, runtimeRef{ws.ID, r.ID})
			}
		}
	}
	valid := map[runtimeRef]bool{}
	for _, ref := range next {
		valid[ref] = true
	}
	d.mu.Lock()
	d.refs = next
	var cancel []context.CancelFunc
	for key, ref := range d.activeRefs {
		if !valid[ref] {
			cancel = append(cancel, d.activeCancels[key])
		}
	}
	d.mu.Unlock()
	for _, stop := range cancel {
		stop()
	}
	return nil
}
