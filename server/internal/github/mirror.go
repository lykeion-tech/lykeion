package github

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"lykeion/server/internal/board"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

type PullRequest struct {
	Owner, Repo                                                             string
	Number                                                                  int
	Title, Body, State, HTMLURL, Branch, AuthorLogin, AuthorAvatar, HeadSHA string
	Draft, Merged                                                           bool
	Additions, Deletions, ChangedFiles                                      int
	CreatedAt, UpdatedAt                                                    time.Time
	MergedAt, ClosedAt                                                      *time.Time
}

func (p PullRequest) state() string {
	switch {
	case p.Merged || p.MergedAt != nil:
		return "merged"
	case p.State == "closed":
		return "closed"
	case p.Draft:
		return "draft"
	default:
		return "open"
	}
}

var errNoMirrorTarget = errors.New("GitHub installation has no active connection")

func (s *Service) mirror(ctx context.Context, installation int64, pr PullRequest) error {
	targets, err := s.mirrorTargets(ctx, installation, pr)
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		return errNoMirrorTarget
	}
	if pr.Number <= 0 || pr.Owner == "" || pr.Repo == "" || pr.CreatedAt.IsZero() || pr.UpdatedAt.IsZero() {
		return ErrWebhookPayload
	}
	for _, target := range targets {
		if err := s.mirrorTargetPR(ctx, installation, pr, target); err != nil {
			return err
		}
	}
	return nil
}

type mirrorTarget struct {
	workspace uuid.UUID
	prefix    string
	cards     map[string]uuid.UUID
}

func (s *Service) mirrorTargets(ctx context.Context, installation int64, p PullRequest) ([]mirrorTarget, error) {
	rows, err := db.New(s.Store.Pool).GithubInstallationWorkspaces(ctx, installation)
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	targets := make([]mirrorTarget, 0, len(rows))
	for _, row := range rows {
		target := mirrorTarget{workspace: row.WorkspaceID, prefix: board.DisplayPrefix(row.CardPrefix, row.Slug), cards: map[string]uuid.UUID{}}
		keys := Match(target.prefix, p.Branch, p.Title, p.Body)
		if len(keys) > 0 {
			numbers := make([]int32, 0, len(keys))
			for _, k := range keys {
				v, _ := strconv.ParseInt(strings.TrimPrefix(k, target.prefix+"-"), 10, 32)
				numbers = append(numbers, int32(v))
			}
			err = s.Store.InWorkspace(ctx, row.WorkspaceID, uuid.Nil, func(tx *store.Tx) error {
				cards, err := tx.Q.GithubMatchCards(ctx, db.GithubMatchCardsParams{WorkspaceID: tx.WS, Numbers: numbers})
				if err != nil {
					return err
				}
				for _, c := range cards {
					key := fmt.Sprintf("%s-%d", target.prefix, c.Number)
					target.cards[key] = c.ID
					counts[key]++
				}
				return nil
			})
			if err != nil {
				return nil, err
			}
		}
		targets = append(targets, target)
	}
	for _, t := range targets {
		for k := range t.cards {
			if counts[k] > 1 {
				delete(t.cards, k)
			}
		}
	}
	return targets, nil
}

// mirrorTargetPR writes a single workspace after installation-wide identifier resolution.
// Imports can also check the locked connection's creation time before calling applyMirror.
func (s *Service) mirrorTargetPR(ctx context.Context, installation int64, p PullRequest, target mirrorTarget) error {
	return s.Store.InWorkspace(ctx, target.workspace, uuid.Nil, func(tx *store.Tx) error {
		if _, err := tx.Q.LockWorkspaceForMutation(ctx, tx.WS); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			return err
		}
		_, err := tx.Q.LockActiveGithubConnection(ctx, db.LockActiveGithubConnectionParams{WorkspaceID: tx.WS, InstallationID: installation})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		return applyMirror(ctx, tx, installation, p, target)
	})
}

func applyMirror(ctx context.Context, tx *store.Tx, installation int64, p PullRequest, t mirrorTarget) error {
	ws, err := tx.Q.GetWorkspace(ctx, tx.WS)
	if err != nil {
		return err
	}
	if board.DisplayPrefix(ws.CardPrefix, ws.Slug) != t.prefix {
		return errors.New("workspace prefix changed during GitHub delivery")
	}
	old, err := tx.Q.GetGithubPullRequestByRepo(ctx, db.GetGithubPullRequestByRepoParams{WorkspaceID: tx.WS, RepoOwner: p.Owner, RepoName: p.Repo, Number: int32(p.Number)})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	row, err := tx.Q.UpsertGithubPullRequest(ctx, db.UpsertGithubPullRequestParams{
		WorkspaceID: tx.WS, InstallationID: installation, RepoOwner: p.Owner, RepoName: p.Repo, Number: int32(p.Number),
		Title: p.Title, State: p.state(), HtmlUrl: p.HTMLURL, Branch: p.Branch,
		BodyKeys: Match(t.prefix, "", "", p.Body), AuthorLogin: p.AuthorLogin, AuthorAvatarUrl: p.AuthorAvatar,
		Additions: int32(p.Additions), Deletions: int32(p.Deletions), ChangedFiles: int32(p.ChangedFiles),
		HeadSha: p.HeadSHA, MergedAt: p.MergedAt, ClosedAt: p.ClosedAt,
		PrCreatedAt: p.CreatedAt, PrUpdatedAt: p.UpdatedAt,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	// Bookkeeping and matched body keys aren't displayed on a linked study.
	old.UpdatedAt = row.UpdatedAt
	old.BodyKeys = row.BodyKeys
	changed := !reflect.DeepEqual(row, old)
	links, err := tx.Q.ListLinksForPR(ctx, db.ListLinksForPRParams{WorkspaceID: tx.WS, PrID: row.ID})
	if err != nil {
		return err
	}
	auto, err := tx.Q.GetGithubAutoLink(ctx, tx.WS)
	if err != nil {
		return err
	}
	affected := map[uuid.UUID]bool{}
	for _, l := range links {
		if changed {
			affected[l.CardID] = true
		}
		if auto && l.Source == "auto" && strings.HasPrefix(l.MatchedKey, t.prefix+"-") && t.cards[l.MatchedKey] != l.CardID {
			_, err := tx.Q.DeleteGithubLink(ctx, db.DeleteGithubLinkParams{WorkspaceID: tx.WS, PrID: row.ID, CardID: l.CardID})
			if err != nil {
				return err
			}
			affected[l.CardID] = true
		}
	}
	if auto {
		for key, card := range t.cards {
			skip, err := tx.Q.IsGithubUnlinked(ctx, db.IsGithubUnlinkedParams{WorkspaceID: tx.WS, PrID: row.ID, CardID: card})
			if err != nil {
				return err
			}
			if skip {
				continue
			}
			n, err := tx.Q.InsertGithubLink(ctx, db.InsertGithubLinkParams{WorkspaceID: tx.WS, PrID: row.ID, CardID: card, Source: "auto", MatchedKey: key})
			if err != nil {
				return err
			}
			if n > 0 {
				affected[card] = true
			}
		}
	}
	for c := range affected {
		if err := tx.Emit(ctx, "github.pr.changed", &c, store.System, nil); err != nil {
			return err
		}
	}
	return nil
}
