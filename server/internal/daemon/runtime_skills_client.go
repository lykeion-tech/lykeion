package daemon

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"sort"
	"time"

	"github.com/google/uuid"
	"lykeion/server/internal/apiclient"
	"lykeion/server/internal/skill"
)

type runtimeSkillJob struct {
	ID            uuid.UUID `json:"id"`
	WorkspaceID   uuid.UUID `json:"workspace_id"`
	RuntimeID     uuid.UUID `json:"runtime_id"`
	DaemonID      string    `json:"daemon_id"`
	AcquisitionID uuid.UUID `json:"acquisition_id"`
	Operation     string    `json:"operation"`
	Status        string    `json:"status"`
	Harness       string    `json:"harness"`
	Keys          []string  `json:"keys"`
	ExpiresAt     time.Time `json:"expires_at"`
}

// Polling only asks for explicit owner-created operations. Personal folders are
// opened only after the server returns an acquired operation for this runtime.
func (d *daemon) runtimeSkills(ctx context.Context) {
	for _, ref := range d.references() {
		api := *d.api
		api.Workspace = ref.WorkspaceID.String()
		for _, operation := range []string{"list", "import"} {
			if ctx.Err() != nil {
				return
			}
			route := "/api/daemon/runtimes/" + ref.RuntimeID.String() + "/local-skills"
			if operation == "import" {
				route += "/import"
			}
			var jobs []runtimeSkillJob
			if err := api.Do(ctx, "POST", route, map[string]string{"daemon_id": d.id}, &jobs); err != nil {
				slog.Warn("runtime skill claim unavailable", "runtime_id", ref.RuntimeID, "err", err)
				continue
			}
			for _, job := range jobs {
				if job.WorkspaceID != ref.WorkspaceID || job.RuntimeID != ref.RuntimeID || job.DaemonID != d.id || job.AcquisitionID == uuid.Nil || job.ID == uuid.Nil || job.Operation != operation || job.Status != "running" || operation == "import" && (len(job.Keys) < 1 || len(job.Keys) > 10) {
					slog.Warn("invalid runtime skill claim", "runtime_id", ref.RuntimeID)
					continue
				}
				jobCtx, cancel := context.WithDeadline(ctx, job.ExpiresAt)
				err := d.executeRuntimeSkill(jobCtx, &api, route, job)
				cancel()
				if err != nil {
					slog.Warn("runtime skill result pending", "request_id", job.ID, "err", err)
				}
			}
		}
	}
}
func (d *daemon) executeRuntimeSkill(ctx context.Context, api *apiclient.Client, route string, job runtimeSkillJob) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	q := url.Values{"daemon_id": {d.id}, "acquisition_id": {job.AcquisitionID.String()}}
	resultPath := route + "/" + job.ID.String() + "/result"
	home, homeErr := os.UserHomeDir()
	if job.Operation == "list" {
		var inventory []localSkillSummary
		err := homeErr
		if err == nil {
			inventory, err = listLocalSkills(job.Harness, home, os.Getenv("CODEX_HOME"))
		}
		if err != nil {
			return reportRuntimeSkill(ctx, api, resultPath+"?"+q.Encode(), map[string]any{"status": "failed", "error": err.Error()})
		}
		return reportRuntimeSkill(ctx, api, resultPath+"?"+q.Encode(), map[string]any{"status": "completed", "skills": inventory})
	}
	for _, key := range job.Keys {
		if err := ctx.Err(); err != nil {
			return err
		}
		q.Set("key", key)
		var b skill.Bundle
		err := homeErr
		if err == nil {
			b, _, err = loadLocalSkill(job.Harness, home, os.Getenv("CODEX_HOME"), key)
		}
		var data []byte
		if err == nil {
			data, err = localSkillZIP(b)
		}
		var input any = apiclient.Zip(data)
		if err != nil {
			input = map[string]string{"error": err.Error()}
		}
		if err = reportRuntimeSkill(ctx, api, resultPath+"?"+q.Encode(), input); err != nil {
			return err
		}
	}
	return nil
}
func localSkillZIP(b skill.Bundle) ([]byte, error) {
	if err := skill.Validate(b); err != nil {
		return nil, err
	}
	var data bytes.Buffer
	w := zip.NewWriter(&data)
	names := make([]string, 0, len(b.Files))
	for name := range b.Files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		f, err := w.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err != nil {
			return nil, err
		}
		if _, err = f.Write(b.Files[name]); err != nil {
			return nil, err
		}
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	if data.Len() > skill.ArchiveBytes {
		return nil, fmt.Errorf("skill archive exceeds size limit")
	}
	return data.Bytes(), nil
}

// Retries keep exactly the same bytes and acquisition identity. A rejected
// authorization or expired operation is permanent and stops further reads.
func reportRuntimeSkill(ctx context.Context, api *apiclient.Client, route string, input any) error {
	var err error
	for _, delay := range []time.Duration{0, 500 * time.Millisecond, 2 * time.Second, 4 * time.Second} {
		if delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
		if err = api.Do(ctx, "POST", route, input, nil); err == nil {
			return nil
		}
		var response *apiclient.HTTPError
		if errors.As(err, &response) && response.Status >= 400 && response.Status < 500 {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return err
}
