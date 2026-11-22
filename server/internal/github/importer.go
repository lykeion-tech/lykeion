package github

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

var errImportStopped = errors.New("GitHub import connection changed")
var repoIdentity = regexp.MustCompile(`^[A-Za-z0-9-]+/[A-Za-z0-9_.-]+$`)

func (s *Service) background(ctx context.Context) context.Context {
	if s.Background != nil {
		return s.Background
	}
	return context.WithoutCancel(ctx)
}

func (s *Service) connection(ctx context.Context, ws uuid.UUID, id int64) (c db.GithubConnection, err error) {
	err = s.Store.InWorkspace(ctx, ws, uuid.Nil, func(tx *store.Tx) error {
		var err error
		c, err = tx.Q.GetGithubConnection(ctx, db.GetGithubConnectionParams{WorkspaceID: ws, InstallationID: id})
		if errors.Is(err, pgx.ErrNoRows) || err == nil && c.Status != "active" {
			return errImportStopped
		}
		return err
	})
	return
}

func (s *Service) Import(ctx context.Context, ws uuid.UUID, id int64) {
	c, err := s.connection(ctx, ws, id)
	if err == nil {
		s.runImport(ctx, c)
	} else if !errors.Is(err, errImportStopped) && ctx.Err() == nil {
		slog.Warn("GitHub import connection lookup failed", "error", err)
	}
}

func (s *Service) ResumeImports(ctx context.Context) {
	rows, err := db.New(s.Store.Pool).GithubPendingImports(ctx)
	if err != nil {
		slog.Warn("GitHub pending import lookup failed", "error", err)
		return
	}
	for _, row := range rows {
		if ctx.Err() != nil {
			return
		}
		s.Import(ctx, row.WorkspaceID, row.InstallationID)
	}
}

type importJob struct {
	s         *Service
	c         db.GithubConnection
	token     string
	remaining time.Duration
}

func (s *Service) runImport(ctx context.Context, c db.GithubConnection) {
	// A startup scan can race connect or unsuspend. Wait for the active run, then
	// recheck state: an interrupted run may still need the unsuspend trigger.
	key := struct {
		workspace    uuid.UUID
		installation int64
		created      int64
	}{c.WorkspaceID, c.InstallationID, c.CreatedAt.UnixMicro()}
	done := make(chan struct{})
	for {
		previous, loaded := s.initialImports.LoadOrStore(key, done)
		if !loaded {
			break
		}
		select {
		case <-ctx.Done():
			return
		case <-previous.(chan struct{}):
		}
	}
	defer func() { s.initialImports.Delete(key); close(done) }()
	j := importJob{s: s, c: c, remaining: 15 * time.Minute}
	if err := j.transition(ctx, "running"); err != nil {
		if !errors.Is(err, errImportStopped) && ctx.Err() == nil {
			slog.Warn("GitHub import start failed", "error", err)
		}
		return
	}
	state := "done"
	if err := j.run(ctx, nil); err != nil {
		if errors.Is(err, errImportStopped) || ctx.Err() != nil {
			return
		}
		slog.Warn("GitHub open pull request import failed", "error", scrub(err))
		state = "failed"
	}
	if err := j.transition(ctx, state); err != nil && !errors.Is(err, errImportStopped) && ctx.Err() == nil {
		slog.Warn("GitHub import state update failed", "error", err)
	}
}

func (j *importJob) lock(ctx context.Context, tx *store.Tx) (db.GithubConnection, error) {
	var c db.GithubConnection
	if _, err := tx.Q.LockWorkspaceForMutation(ctx, tx.WS); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return c, errImportStopped
		}
		return c, err
	}
	c, err := tx.Q.LockActiveGithubConnection(ctx, db.LockActiveGithubConnectionParams{WorkspaceID: tx.WS, InstallationID: j.c.InstallationID})
	if errors.Is(err, pgx.ErrNoRows) || err == nil && !c.CreatedAt.Equal(j.c.CreatedAt) {
		return c, errImportStopped
	}
	return c, err
}

func (j *importJob) transition(ctx context.Context, state string) error {
	return j.s.Store.InWorkspace(ctx, j.c.WorkspaceID, uuid.Nil, func(tx *store.Tx) error {
		c, err := j.lock(ctx, tx)
		if err != nil {
			return err
		}
		// Another server may also have resumed this generation. A completed full
		// import supersedes an overlapping failed attempt.
		if c.ImportState != "pending" && c.ImportState != "running" && !(c.ImportState == "failed" && state == "done") {
			return errImportStopped
		}
		if c.ImportState == state {
			return nil
		}
		if _, err := tx.Q.SetGithubImportState(ctx, db.SetGithubImportStateParams{WorkspaceID: tx.WS, InstallationID: c.InstallationID, ImportState: state}); err != nil {
			return err
		}
		return tx.Emit(ctx, "github.changed", nil, store.System, nil)
	})
}

func (s *Service) ImportRepos(ctx context.Context, id int64, repos []Repo) {
	if len(repos) == 0 {
		return
	}
	connections, err := s.importConnections(ctx, id)
	if err == nil {
		err = s.runRepoImports(ctx, connections, repos)
	}
	if err != nil && ctx.Err() == nil {
		slog.Warn("GitHub added repository import failed", "error", scrub(err))
	}
}

func (s *Service) importConnections(ctx context.Context, id int64) ([]db.GithubConnection, error) {
	rows, err := db.New(s.Store.Pool).GithubInstallationWorkspaces(ctx, id)
	if err != nil {
		return nil, err
	}
	var connections []db.GithubConnection
	for _, row := range rows {
		c, err := s.connection(ctx, row.WorkspaceID, id)
		if errors.Is(err, errImportStopped) {
			continue
		}
		if err != nil {
			return nil, err
		}
		connections = append(connections, c)
	}
	return connections, nil
}
func (s *Service) runRepoImports(ctx context.Context, connections []db.GithubConnection, repos []Repo) error {
	for _, c := range connections {
		j := importJob{s: s, c: c, remaining: 15 * time.Minute}
		if err := j.run(ctx, repos); err != nil && !errors.Is(err, errImportStopped) {
			return err
		}
	}
	return nil
}

// Every attempt, including retries, confirms that the initiating connection is still active.
func (j *importJob) request(ctx context.Context, read func() error) error {
	renewed := false
	for {
		c, err := j.s.connection(ctx, j.c.WorkspaceID, j.c.InstallationID)
		if err != nil {
			return err
		}
		if !c.CreatedAt.Equal(j.c.CreatedAt) {
			return errImportStopped
		}
		if j.token == "" {
			j.token, err = j.s.installationToken(ctx, j.c.InstallationID)
		}
		if err == nil {
			err = read()
		}
		var api *APIError
		if !errors.As(err, &api) {
			return err
		}
		if api.Status == 401 && j.token != "" && !renewed {
			renewed = true
			j.token = ""
			continue
		}
		if (api.Status != 403 && api.Status != 429) || (!api.RetryAfterSet && api.RateLimitReset.IsZero()) {
			return err
		}
		delay := max(api.RetryAfter, api.RateLimitReset.Sub(j.s.currentTime()), time.Second)
		if delay > j.remaining {
			return err
		}
		j.remaining -= delay
		sleep := j.s.sleep
		if sleep == nil {
			sleep = sleepImport
		}
		if err := sleep(ctx, delay); err != nil {
			return err
		}
	}
}
func (j *importJob) readPage(ctx context.Context, path string, out any) (string, error) {
	var next string
	err := j.request(ctx, func() error {
		var err error
		next, err = j.s.getPage(ctx, j.token, path, out)
		return err
	})
	return next, err
}

func sleepImport(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (j *importJob) run(ctx context.Context, repos []Repo) error {
	if repos != nil {
		return j.importRepoList(ctx, repos)
	}
	return j.pages(ctx, "/installation/repositories?per_page=100", func(path string) (string, error) {
		var page struct{ Repositories []Repo }
		next, err := j.readPage(ctx, path, &page)
		if err != nil {
			return "", err
		}
		if page.Repositories == nil {
			return "", errors.New("invalid GitHub repository page")
		}
		return next, j.importRepoList(ctx, page.Repositories)
	})
}

func (j *importJob) pages(ctx context.Context, path string, read func(string) (string, error)) error {
	seen := map[string]bool{}
	for path != "" {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if seen[path] || len(seen) >= 10000 {
			return errors.New("invalid GitHub import pagination")
		}
		seen[path] = true
		next, err := read(path)
		if err != nil {
			return err
		}
		path = next
	}
	return nil
}

func (j *importJob) importRepoList(ctx context.Context, repos []Repo) error {
	for _, repo := range repos {
		if repo.Owner.Login == "" || repo.Name == "" {
			repo.Owner.Login, repo.Name, _ = strings.Cut(repo.FullName, "/")
		}
		if !repoIdentity.MatchString(repo.Owner.Login+"/"+repo.Name) || repo.Name == "." || repo.Name == ".." {
			return errors.New("invalid GitHub repository")
		}
		base := "/repos/" + url.PathEscape(repo.Owner.Login) + "/" + url.PathEscape(repo.Name) + "/pulls"
		if err := j.pages(ctx, base+"?state=open&per_page=100", func(path string) (string, error) {
			var pulls []struct{ Number int32 }
			next, err := j.readPage(ctx, path, &pulls)
			if err != nil {
				return "", err
			}
			if pulls == nil {
				return "", errors.New("invalid GitHub pull request page")
			}
			for _, pr := range pulls {
				if pr.Number <= 0 {
					return "", ErrWebhookPayload
				}
				if err := j.pull(ctx, repo, base, pr.Number); err != nil {
					return "", err
				}
			}
			return next, nil
		}); err != nil {
			return err
		}
	}
	return nil
}

func (j *importJob) pull(ctx context.Context, repo Repo, base string, n int32) error {
	var detail pullRequestData
	var observed time.Time
	// The reused detail is part of the snapshot. Order it by the time before the HTTP read.
	if err := j.s.Store.Pool.QueryRow(ctx, "select statement_timestamp()").Scan(&observed); err != nil {
		return err
	}
	if err := j.request(ctx, func() error { return j.s.get(ctx, j.token, fmt.Sprintf("%s/%d", base, n), &detail) }); err != nil {
		return err
	}
	if detail.State == "closed" || detail.Merged || detail.MergedAt != nil {
		return nil
	}
	if detail.State != "open" || detail.Number != n || detail.CreatedAt.IsZero() || detail.UpdatedAt.IsZero() || detail.Head.SHA == "" {
		return ErrWebhookPayload
	}
	pr := detail.pull(repo)
	id := j.c.InstallationID
	var target snapshotTarget
	err := j.s.withMirrorLock(ctx, id, pr, func(conn *pgxpool.Conn) error {
		targets, err := j.s.mirrorTargets(ctx, conn, id, pr)
		if err != nil {
			return err
		}
		for _, t := range targets {
			if t.workspace != j.c.WorkspaceID {
				continue
			}
			return j.s.Store.InWorkspaceOn(ctx, conn, t.workspace, uuid.Nil, func(tx *store.Tx) error {
				if _, err := j.lock(ctx, tx); err != nil {
					return err
				}
				if err := applyMirror(ctx, tx, id, pr, t); err != nil {
					if errors.Is(err, errStaleMirror) {
						return nil
					}
					return err
				}
				rows, err := tx.Q.ListGithubSnapshotTargets(ctx, db.ListGithubSnapshotTargetsParams{WorkspaceID: tx.WS, InstallationID: id, RepoOwner: pr.Owner, RepoName: pr.Repo, PrNumber: n})
				if err == nil && len(rows) == 1 {
					target = snapshotTarget{tx.WS, rows[0]}
					target.ObservedAt = observed
				}
				return err
			})
		}
		return errImportStopped
	})
	if err != nil || target.ws == uuid.Nil {
		return err
	}
	snap, head, err := j.s.fetchSnapshotDetail(ctx, pr.Owner, pr.Repo, n, []snapshotTarget{target}, &detail, func(path string, out any) (string, error) { return j.readPage(ctx, path, out) })

	if errors.Is(err, errImportStopped) || ctx.Err() != nil {
		return err
	}
	if (head == "" || head == target.HeadSha) && (snap != nil || err != nil) {
		if writeErr := j.s.writeSnapshot(ctx, id, pr.Owner, pr.Repo, target, snap); writeErr != nil {
			return writeErr
		}
	}
	var api *APIError
	if errors.As(err, &api) && (api.Status == 429 || api.Status == 403) && (api.RetryAfterSet || !api.RateLimitReset.IsZero()) {
		return err
	}
	if err != nil {
		slog.Warn("GitHub imported snapshot unavailable", "error", scrub(err))
	}
	return nil
}
