package slack

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	slackgo "github.com/slack-go/slack"

	"lykeion/server/internal/labmemory"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

const (
	unavailableText = "This answer is unavailable because its evidence has changed."
	maxAttempts     = 5
)

// draftReason maps the internal run reason to text that is safe to show.
func draftReason(code string) string {
	switch code {
	case "dispatch_failed":
		return "the agent could not start."
	case "assignee_unavailable":
		return "the drafting agent is unavailable."
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
		case d.Completed && d.CardID != nil:
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
		return "", false, false, nil
	}
	if err != nil {
		return "", false, false, err
	}
	if m.Kind == "failure" {
		return mrkdwnEscaper.Replace(m.Body), true, false, nil
	}
	ss, ee, safe, err := labmemory.NewDisplayReader(tx, p.UserID, m.ChatID).Answer(ctx, m)
	if err != nil {
		return "", false, false, err
	}
	if !safe {
		return unavailableText, true, false, nil
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
		}
	}
	return errors.Join(errs...)
}

func (s *Service) deliver(ctx context.Context, inst db.SlackInstallation, c *session, p db.SlackPost) error {
	var text string
	var send bool
	err := s.Store.InWorkspaceSnapshot(ctx, inst.WorkspaceID, p.UserID, func(tx *store.Tx) error {
		text, send = "", false
		mark := func(status string) error {
			return tx.Q.MarkSlackPost(ctx, db.MarkSlackPostParams{ID: p.ID, Status: status})
		}
		if _, err := tx.Q.LockMember(ctx, db.LockMemberParams{WorkspaceID: inst.WorkspaceID, UserID: p.UserID}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return mark("dropped")
			}
			return err
		}
		t, ready, drop, err := s.compose(ctx, tx, p)
		switch {
		case err != nil:
			return err
		case drop:
			return mark("dropped")
		case !ready:
			return nil
		}
		text, send = t, true
		return mark("sending")
	})
	if err != nil || !send {
		return err
	}
	opts := []slackgo.MsgOption{slackgo.MsgOptionText(text, false)}
	if p.ThreadTs != "" {
		opts = append(opts, slackgo.MsgOptionTS(p.ThreadTs))
	}
	_, _, perr := c.PostMessageContext(ctx, p.Channel, opts...)
	// The row is already 'sending'; a failure to record the outcome must not
	// make it deliverable again.
	ctx = context.WithoutCancel(ctx)
	err = s.Store.InWorkspace(ctx, inst.WorkspaceID, p.UserID, func(tx *store.Tx) error {
		status := "posted"
		if perr != nil {
			n, err := tx.Q.BumpSlackPostAttempts(ctx, p.ID)
			if err != nil {
				return err
			}
			status = "pending"
			if n >= maxAttempts {
				status = "dropped"
			}
		}
		return tx.Q.MarkSlackPost(ctx, db.MarkSlackPostParams{ID: p.ID, Status: status})
	})
	if perr != nil {
		return errors.Join(c.err(perr), err)
	}
	if p.Kind == "answer" && p.ThreadTs != "" {
		// Best effort. thread_ts is the question's own ts for a top-level
		// message; for a reply inside a thread it is the parent, which carries
		// no reaction, and Slack's no_reaction error is ignored.
		_ = c.RemoveReactionContext(ctx, "hourglass_flowing_sand", slackgo.NewRefToMessage(p.Channel, p.ThreadTs))
	}
	return err
}
