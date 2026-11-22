package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"reflect"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

type CheckRun struct{ Name, Status, Conclusion string }
type Status struct{ Context, State string }

func rollup(runs []CheckRun, statuses []Status) (state *string, passed, total int, failing []string) {
	total = len(runs) + len(statuses)
	for _, r := range runs {
		if r.Status != "completed" {
			continue
		}
		switch r.Conclusion {
		case "success", "neutral", "skipped":
			passed++
		case "failure", "timed_out", "cancelled", "action_required", "startup_failure":
			failing = append(failing, r.Name)
		}
	}
	for _, s := range statuses {
		switch s.State {
		case "success":
			passed++
		case "failure", "error":
			failing = append(failing, s.Context)
		}
	}
	value := "pending"
	switch {
	case total == 0:
		return nil, 0, 0, nil
	case len(failing) > 0:
		value = "failed"
	case total == passed:
		value = "passed"
	}
	slices.Sort(failing)
	return &value, passed, total, failing
}

func mergeability(state string) *string {
	switch state {
	case "clean":
		state = "mergeable"
	case "dirty":
		state = "conflicting"
	case "blocked", "behind":
	default:
		return nil
	}
	return &state
}

type snapshotTarget struct {
	ws uuid.UUID
	db.ListGithubSnapshotTargetsRow
}

func (s *Service) refresh(ctx context.Context, installationID int64, owner, repo string, number int) {
	if number <= 0 {
		return
	}
	s.refreshSnapshots(ctx, installationID, owner, repo, int32(number), "")
}

// A head event may refer to several PRs and each PR may be mirrored in several workspaces.
func (s *Service) refreshSnapshots(ctx context.Context, id int64, owner, repo string, number int32, sha string) {
	if owner == "" || repo == "" || (number == 0 && sha == "") {
		return
	}
	rows, err := db.New(s.Store.Pool).GithubInstallationWorkspaces(ctx, id)
	if err != nil {
		slog.Warn("GitHub snapshot workspace lookup failed", "error", err)
		return
	}
	targets := map[int32][]snapshotTarget{}
	for _, ws := range rows {
		err := s.Store.InWorkspace(ctx, ws.WorkspaceID, uuid.Nil, func(tx *store.Tx) error {
			prs, err := tx.Q.ListGithubSnapshotTargets(ctx, db.ListGithubSnapshotTargetsParams{WorkspaceID: tx.WS, InstallationID: id, RepoOwner: owner, RepoName: repo, PrNumber: number, Sha: sha})
			if err != nil {
				return err
			}
			for _, pr := range prs {
				targets[pr.Number] = append(targets[pr.Number], snapshotTarget{tx.WS, pr})
			}
			return nil
		})
		if err != nil {
			slog.Warn("GitHub snapshot target lookup failed", "error", err)
		}
	}
	for n, group := range targets {
		if ctx.Err() != nil {
			return
		}
		fetchCtx, cancel := context.WithTimeout(ctx, time.Minute)
		snap, head, err := s.fetchSnapshot(fetchCtx, id, owner, repo, n, group)
		cancel()
		if err != nil {
			slog.Warn("GitHub snapshot unavailable", "error", scrub(err))
		}
		for _, target := range group {
			if head != "" && head != target.HeadSha || err == nil && snap == nil {
				continue
			}
			if err := s.writeSnapshot(ctx, id, owner, repo, target, snap); err != nil {
				slog.Warn("GitHub snapshot write failed", "error", err)
			}
		}
	}
}

func (s *Service) fetchSnapshot(ctx context.Context, id int64, owner, repo string, n int32, targets []snapshotTarget) (*db.SetGithubSnapshotParams, string, error) {
	token, err := s.installationToken(ctx, id)
	if err != nil {
		return nil, "", err
	}
	path := "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo)
	prPath := fmt.Sprintf("%s/pulls/%d", path, n)
	var sha, mergeableState string
	for attempt := 0; ; attempt++ {
		var pr struct {
			State          string
			Head           struct{ SHA string }
			Mergeable      json.RawMessage
			MergeableState string `json:"mergeable_state"`
		}
		if err := s.get(ctx, token, prPath, &pr); err != nil {
			return nil, sha, err
		}
		if pr.State == "closed" {
			return nil, "", nil
		}
		sha = pr.Head.SHA
		if sha == "" || pr.State != "open" {
			return nil, "", errors.New("invalid GitHub snapshot pull request")
		}
		if !slices.ContainsFunc(targets, func(t snapshotTarget) bool { return t.HeadSha == sha }) {
			return nil, sha, nil
		}
		if string(pr.Mergeable) != "null" {
			mergeableState = pr.MergeableState
			break
		}
		// An explicit null starts GitHub's mergeability calculation. Give it one follow-up read.
		if attempt == 1 {
			break
		}
		select {
		case <-ctx.Done():
			return nil, sha, ctx.Err()
		case <-time.After(time.Second):
		}
	}
	commit := path + "/commits/" + url.PathEscape(sha)
	var runs []CheckRun
	var statuses []Status
	for _, endpoint := range []string{"/check-runs", "/status"} {
		seen := map[string]bool{}
		for page := commit + endpoint + "?per_page=100"; page != ""; {
			if seen[page] {
				return nil, sha, errors.New("GitHub snapshot pagination repeated a page")
			}
			seen[page] = true
			var data struct {
				Runs     []CheckRun `json:"check_runs"`
				Statuses []Status
			}
			next, err := s.getPage(ctx, token, page, &data)
			if err != nil {
				return nil, sha, err
			}
			if endpoint == "/check-runs" && data.Runs == nil || endpoint == "/status" && data.Statuses == nil {
				return nil, sha, errors.New("invalid GitHub snapshot page")
			}
			runs = append(runs, data.Runs...)
			statuses = append(statuses, data.Statuses...)
			page = next
		}
	}
	ci, passed, total, failing := rollup(runs, statuses)
	return &db.SetGithubSnapshotParams{CiState: ci, CiPassed: int32(passed), CiTotal: int32(total), CiFailing: failing, Mergeability: mergeability(mergeableState)}, sha, nil
}

func (s *Service) writeSnapshot(ctx context.Context, id int64, owner, repo string, target snapshotTarget, snap *db.SetGithubSnapshotParams) error {
	return s.Store.InWorkspace(ctx, target.ws, uuid.Nil, func(tx *store.Tx) error {
		if _, err := tx.Q.LockWorkspaceForMutation(ctx, tx.WS); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			return err
		}
		conn, err := tx.Q.LockActiveGithubConnection(ctx, db.LockActiveGithubConnectionParams{WorkspaceID: tx.WS, InstallationID: id})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if !conn.CreatedAt.Equal(target.ConnectedAt) {
			return nil
		}
		current, err := tx.Q.GetGithubPullRequestByRepo(ctx, db.GetGithubPullRequestByRepoParams{WorkspaceID: tx.WS, RepoOwner: owner, RepoName: repo, Number: target.Number})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if current.ID != target.ID || current.HeadSha != target.HeadSha || current.InstallationID != id || (current.State != "open" && current.State != "draft") {
			return nil
		}
		var n int64
		var changed bool
		if snap == nil {
			n, err = tx.Q.MarkGithubSnapshotStale(ctx, db.MarkGithubSnapshotStaleParams{WorkspaceID: tx.WS, InstallationID: id, ID: target.ID, HeadSha: target.HeadSha, ObservedAt: target.ObservedAt})
			changed = current.SnapshotAt != nil && !current.SnapshotStale
		} else {
			params := *snap
			params.ObservedAt = target.ObservedAt
			params.WorkspaceID, params.InstallationID, params.ID, params.HeadSha = tx.WS, id, target.ID, target.HeadSha
			n, err = tx.Q.SetGithubSnapshot(ctx, params)
			changed = current.SnapshotStale || !reflect.DeepEqual(current.CiState, params.CiState) || current.CiPassed != params.CiPassed || current.CiTotal != params.CiTotal || !slices.Equal(current.CiFailing, params.CiFailing) || !reflect.DeepEqual(current.Mergeability, params.Mergeability)
		}
		if err != nil || n == 0 || !changed {
			return err
		}
		links, err := tx.Q.ListLinksForPR(ctx, db.ListLinksForPRParams{WorkspaceID: tx.WS, PrID: target.ID})
		if err != nil {
			return err
		}
		for _, l := range links {
			if err := tx.Emit(ctx, "github.pr.changed", &l.CardID, store.System, nil); err != nil {
				return err
			}
		}
		return nil
	})
}
