package github

import (
	"context"
	"encoding/json"
	"errors"
	"hash/maphash"
	"math/rand/v2"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

var ErrWebhookPayload = errors.New("invalid GitHub webhook payload")

type githubRepository struct {
	ID    int64
	Name  string
	Owner struct{ Login string }
}
type pullRequestData struct {
	Number               int32
	Title, Body, State   string
	HTMLURL              string `json:"html_url"`
	Draft, Merged        bool
	Additions, Deletions int32
	ChangedFiles         int32      `json:"changed_files"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
	ClosedAt             *time.Time `json:"closed_at"`
	MergedAt             *time.Time `json:"merged_at"`
	User                 struct {
		Login  string
		Avatar string `json:"avatar_url"`
	}
	Head struct{ Ref, SHA string }
}

func (d pullRequestData) pull(repo githubRepository) PullRequest {
	return PullRequest{
		Owner: repo.Owner.Login, Repo: repo.Name, Number: int(d.Number),
		Title: d.Title, Body: d.Body, State: d.State, HTMLURL: d.HTMLURL,
		Branch: d.Head.Ref, HeadSHA: d.Head.SHA, AuthorLogin: d.User.Login, AuthorAvatar: d.User.Avatar,
		Draft: d.Draft, Merged: d.Merged, Additions: int(d.Additions), Deletions: int(d.Deletions),
		ChangedFiles: int(d.ChangedFiles), CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt,
		ClosedAt: d.ClosedAt, MergedAt: d.MergedAt,
	}
}

type webhookPayload struct {
	Installation struct {
		ID          int64
		UpdatedAt   time.Time `json:"updated_at"`
		SuspendedAt time.Time `json:"suspended_at"`
	}
	SHA        string
	CheckSuite struct {
		HeadSHA string `json:"head_sha"`
	} `json:"check_suite"`
	CheckRun struct {
		HeadSHA string `json:"head_sha"`
	} `json:"check_run"`
	Action            string
	Repository        githubRepository
	PullRequest       pullRequestData    `json:"pull_request"`
	RepositoriesAdded []githubRepository `json:"repositories_added"`
}

var deliveryHash = maphash.MakeSeed()

func (s *Service) Handle(ctx context.Context, event, delivery string, body []byte) error {
	if delivery == "" || len(delivery) > 256 || event == "" {
		return ErrWebhookPayload
	}
	// Bound the number of locks while serializing duplicate deliveries in this service.
	guard := &s.deliveryLocks[maphash.String(deliveryHash, delivery)%uint64(len(s.deliveryLocks))]
	guard.Lock()
	defer guard.Unlock()
	q := db.New(s.Store.Pool)
	seen, err := q.HasGithubWebhookDelivery(ctx, delivery)
	if err != nil || seen {
		return err
	}
	var p webhookPayload
	if err = json.Unmarshal(body, &p); err != nil {
		return ErrWebhookPayload
	}
	id := p.Installation.ID
	if id <= 0 {
		return nil
	}
	var snapshotNumber int32
	var snapshotSHA string
	switch event {
	case "installation":
		switch p.Action {
		case "deleted", "suspend", "unsuspend":
		default:
			return nil
		}
		workspaces, err := q.GithubInstallationLifecycleWorkspaces(ctx, id)
		if err != nil || len(workspaces) == 0 {
			return err
		}
		version := p.Installation.UpdatedAt
		if p.Action == "suspend" && p.Installation.SuspendedAt.After(version) {
			version = p.Installation.SuspendedAt
		}
		if p.Action != "deleted" && version.IsZero() {
			return ErrWebhookPayload
		}
		for _, ws := range workspaces {
			if err := s.installationLifecycle(ctx, ws, id, p.Action, version); err != nil {
				return err
			}
		}
	case "pull_request":
		pr := p.PullRequest.pull(p.Repository)
		if err := s.mirror(ctx, id, pr); err != nil {
			if errors.Is(err, errNoMirrorTarget) {
				return nil
			}
			return err
		}
		if state := pr.state(); state == "open" || state == "draft" {
			snapshotNumber = p.PullRequest.Number
		}
	case "status", "check_run", "check_suite", "installation_repositories":
		workspaces, err := q.GithubInstallationWorkspaces(ctx, id)
		if err != nil || len(workspaces) == 0 {
			return err
		}
		switch event {
		case "status":
			snapshotSHA = p.SHA
		case "check_run":
			snapshotSHA = p.CheckRun.HeadSHA
		case "check_suite":
			snapshotSHA = p.CheckSuite.HeadSHA
		}
	default:
		return nil
	}
	_, err = q.InsertWebhookDelivery(ctx, delivery)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if snapshotNumber > 0 || snapshotSHA != "" {
		go func() {
			bg, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
			defer cancel()
			if snapshotNumber > 0 {
				s.refresh(bg, id, p.Repository.Owner.Login, p.Repository.Name, int(snapshotNumber))
				return
			}
			s.refreshSnapshots(bg, id, p.Repository.Owner.Login, p.Repository.Name, 0, snapshotSHA)
		}()
	}
	if rand.IntN(100) == 0 {
		_ = q.PruneWebhookDeliveries(ctx)
	}
	return nil
}

func (s *Service) installationLifecycle(ctx context.Context, ws uuid.UUID, id int64, action string, version time.Time) error {
	return s.Store.InWorkspace(ctx, ws, uuid.Nil, func(tx *store.Tx) error {
		if _, err := tx.Q.LockWorkspaceForMutation(ctx, ws); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			return err
		}
		var n int64
		var err error
		// The mutation itself locks the matching row, including a suspended connection.
		if action == "deleted" {
			n, err = tx.Q.DeleteGithubConnection(ctx, db.DeleteGithubConnectionParams{WorkspaceID: ws, InstallationID: id})
		} else {
			status := "active"
			if action == "suspend" {
				status = "suspended"
			}
			current, readErr := tx.Q.GetGithubConnection(ctx, db.GetGithubConnectionParams{WorkspaceID: ws, InstallationID: id})
			if errors.Is(readErr, pgx.ErrNoRows) {
				return nil
			}
			if readErr != nil {
				return readErr
			}
			// Timestamps can tie. Keep the installation suspended until a later unsuspend.
			if last := current.LifecycleAt; last != nil && (version.Before(*last) || version.Equal(*last) && (status == "active" || current.Status == status)) {
				return nil
			}
			n, err = tx.Q.SetGithubConnectionStatus(ctx, db.SetGithubConnectionStatusParams{WorkspaceID: ws, InstallationID: id, Status: status, LifecycleAt: version})
			if err != nil || current.Status == status {
				return err
			}
		}
		if err != nil || n == 0 {
			return err
		}
		// Task 6 resumes interrupted imports after unsuspend commits.
		return tx.Emit(ctx, "github.changed", nil, store.System, nil)
	})
}
