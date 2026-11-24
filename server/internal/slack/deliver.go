package slack

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	slackgo "github.com/slack-go/slack"

	"lykeion/server/internal/labmemory"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

const (
	unavailableText = "This answer is unavailable because its evidence has changed."
	changedText     = "The sources of this answer have changed since it was written."
	maxAttempts     = 5
	// A post still pending after maxAge can no longer be useful.
	maxAge = 24 * time.Hour
	// settleGrace lets a failed run's failure message be recorded after the
	// run settles before a run without a result counts as finished.
	settleGrace = 10 * time.Minute
)

// draftReason maps the internal run reason to text that is safe to show.
func draftReason(code string) string {
	switch code {
	case "dispatch_failed":
		return "the agent could not start."
	case "assignee_unavailable":
		return "the drafting agent is unavailable."
	case "runtime_offline":
		return "your machine went offline."
	}
	return "the agent could not finish."
}

// compose builds the Slack text for a pending post. ready is false while the
// run or draft is still going; drop is true when it can never be delivered.
func (s *Service) compose(ctx context.Context, tx *store.Tx, p db.SlackPost) (text string, ready, drop bool, err error) {
	if p.Kind == "draft" {
		if p.AutoresearchID == nil {
			return "", false, true, nil
		}
		d, err := tx.Q.GetSlackDraftState(ctx, *p.AutoresearchID)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", false, true, nil
		}
		switch {
		case err != nil:
			return "", false, false, err
		case d.Completed && d.CardID == nil:
			return "", false, true, nil
		case d.Completed:
			return "Your study is ready: " + appURL(s.PublicURL) + "/studies/" + d.CardID.String(), true, false, nil
		case d.Failed:
			return "I couldn't draft that study: " + draftReason(d.Reason), true, false, nil
		}
		return "", false, false, nil
	}
	if p.AgentRunID == nil {
		return "", false, true, nil
	}
	m, err := tx.Q.GetLabResultForRun(ctx, p.AgentRunID)
	if errors.Is(err, pgx.ErrNoRows) {
		// A run that ended without a result (its chat was deleted, say) never
		// gets one.
		run, err := tx.Q.GetAgentRun(ctx, db.GetAgentRunParams{ID: *p.AgentRunID, WorkspaceID: tx.WS})
		if errors.Is(err, pgx.ErrNoRows) {
			return "", false, true, nil
		} else if err != nil {
			return "", false, false, err
		}
		ended := run.CreatedAt
		if run.CompletedAt != nil {
			ended = *run.CompletedAt
		}
		terminal := run.Status == "completed" || run.Status == "failed" || run.Status == "cancelled"
		return "", false, terminal && time.Since(ended) > settleGrace, nil
	}
	if err != nil {
		return "", false, false, err
	}
	if m.Kind == "failure" {
		return mrkdwnEscaper.Replace(m.Body), true, false, nil
	}
	reader := labmemory.NewDisplayReader(tx, p.UserID, m.ChatID)
	ss, ee, safe, err := reader.Answer(ctx, m)
	if err != nil {
		return "", false, false, err
	}
	if !safe {
		return unavailableText, true, false, nil
	}
	if reader.Changed(m.ID) {
		ss = slices.Insert(ss, 0, labmemory.DisplayStatement{Text: changedText})
	}
	return Render(ss, ee, s.PublicURL), true, false, nil
}

// DeliverOnce posts every finished answer and draft of this installation.
// A post is committed as 'sending' before Slack is called and rows left in
// that state are never retried: delivery is at most once.
func (s *Service) DeliverOnce(ctx context.Context, inst db.SlackInstallation) error {
	var posts []db.SlackPost
	err := s.Store.InWorkspace(ctx, inst.WorkspaceID, inst.InstalledBy, func(tx *store.Tx) (err error) {
		posts, err = tx.Q.ListPendingSlackPosts(ctx, inst.ID)
		return
	})
	if err != nil || len(posts) == 0 {
		return err
	}
	c, err := s.session(inst)
	if err != nil {
		return err
	}
	var errs []error
	for _, p := range posts {
		if err := s.deliver(ctx, inst, c, p); err != nil {
			errs = append(errs, err)
			// A bot token Slack no longer accepts revokes the installation.
			if authFailed(err) {
				return errors.Join(append(errs, c.err(s.revoke(context.WithoutCancel(ctx), inst)))...)
			}
		}
	}
	return errors.Join(errs...)
}

func (s *Service) deliver(ctx context.Context, inst db.SlackInstallation, c *session, p db.SlackPost) error {
	var text string
	var send, dropped bool
	err := s.Store.InWorkspaceSnapshot(ctx, inst.WorkspaceID, p.UserID, func(tx *store.Tx) error {
		text, send, dropped = "", false, false
		// Only the worker that moves the row out of 'pending' may act on it.
		claim := func(status string) error {
			n, err := tx.Q.ClaimSlackPost(ctx, db.ClaimSlackPostParams{ID: p.ID, Status: status})
			send, dropped = n == 1 && status == "sending", n == 1 && status == "dropped"
			return err
		}
		if _, err := tx.Q.LockMember(ctx, db.LockMemberParams{WorkspaceID: inst.WorkspaceID, UserID: p.UserID}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return claim("dropped")
			}
			return err
		}
		if time.Since(p.CreatedAt) > maxAge {
			return claim("dropped")
		}
		t, ready, drop, err := s.compose(ctx, tx, p)
		switch {
		case err != nil:
			return err
		case drop:
			return claim("dropped")
		case !ready:
			return nil
		}
		text = t
		return claim("sending")
	})
	if err != nil {
		return err
	}
	if dropped {
		s.clearReaction(ctx, c, p)
	}
	if !send {
		return nil
	}
	opts := []slackgo.MsgOption{slackgo.MsgOptionText(text, false)}
	if p.ThreadTs != "" {
		opts = append(opts, slackgo.MsgOptionTS(p.ThreadTs))
	}
	_, _, perr := c.PostMessageContext(ctx, p.Channel, opts...)
	// Only an error answered by Slack proves the message was not posted; after
	// a transport error it may have been, so it is dropped, never retried. The
	// row is already 'sending'; a failure to record the outcome must not make
	// it deliverable again.
	var apiErr slackgo.SlackErrorResponse
	var limited *slackgo.RateLimitedError
	retry := errors.As(perr, &apiErr) || errors.As(perr, &limited)
	ctx = context.WithoutCancel(ctx)
	final := "posted"
	err = s.Store.InWorkspace(ctx, inst.WorkspaceID, p.UserID, func(tx *store.Tx) error {
		final = "posted"
		if perr != nil && !retry {
			final = "dropped"
		} else if perr != nil {
			n, err := tx.Q.BumpSlackPostAttempts(ctx, p.ID)
			if err != nil {
				return err
			}
			final = "pending"
			if n >= maxAttempts {
				final = "dropped"
			}
		}
		return tx.Q.MarkSlackPost(ctx, db.MarkSlackPostParams{ID: p.ID, Status: final})
	})
	if perr == nil || final == "dropped" {
		s.clearReaction(ctx, c, p)
	}
	if perr != nil {
		return errors.Join(c.err(perr), err)
	}
	return err
}

// clearReaction removes the hourglass from the question; best effort.
func (s *Service) clearReaction(ctx context.Context, c *session, p db.SlackPost) {
	if p.MessageTs != "" {
		_ = c.RemoveReactionContext(ctx, "hourglass_flowing_sand", slackgo.NewRefToMessage(p.Channel, p.MessageTs))
	}
}
