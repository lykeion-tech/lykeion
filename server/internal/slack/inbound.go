package slack

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	slackgo "github.com/slack-go/slack"

	"lykeion/server/internal/labmemory"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

// Message is an app_mention or message.im, transport-neutral.
type Message struct {
	EventID, AppID, User, Channel, ChannelType, TS, ThreadTS, Text string
	BotID, Subtype                                                 string
	HasFiles                                                       bool
}

const (
	helpText = "Ask me a question about your lab and I'll answer from your workspace's evidence.\n" +
		"/study <request> drafts a study, /new starts a fresh chat, /clear deletes this chat, /help shows this.\n" +
		"An answer in a channel is visible to everyone in that channel, including people outside your workspace."
	offlineText = "Your machine is offline. Connect it in Lykeion and ask again."
	filesText   = "Aristotle can't read Slack files yet; I answered from your text only."
	maxQuestion = 8000
)

// reply is a Slack call to make once the transaction has committed.
type reply struct {
	ephemeral bool
	text      string
}

// HandleMessage turns a Slack message into an Ask Aristotle question as its
// linked sender, then reacts (or replies) in Slack after the commit.
func (s *Service) HandleMessage(ctx context.Context, inst db.SlackInstallation, m Message) (err error) {
	if m.AppID != inst.AppID || m.BotID != "" || m.Subtype != "" || m.User == inst.BotUserID {
		return nil
	}
	key := "lyk:slack:event:" + inst.ID.String() + ":" + m.EventID
	if ok, err := s.Redis.SetNX(ctx, key, 1, time.Hour).Result(); err != nil || !ok {
		return err
	}
	// A failure before the question commits lets Slack's retry run again.
	committed := false
	defer func() {
		if err != nil && !committed {
			s.Redis.Del(context.WithoutCancel(ctx), key)
		}
	}()
	thread := m.ThreadTS
	if thread == "" {
		thread = m.TS
	}
	var (
		user    uuid.UUID
		bound   bool
		link    string
		replies []reply
		react   bool
	)
	// Who is asking? Membership is re-checked on every event.
	err = s.Store.InWorkspace(ctx, inst.WorkspaceID, inst.InstalledBy, func(tx *store.Tx) error {
		b, err := tx.Q.GetSlackBinding(ctx, db.GetSlackBindingParams{InstallationID: inst.ID, SlackUserID: m.User})
		if err == nil {
			if _, err = tx.Q.LockMember(ctx, db.LockMemberParams{WorkspaceID: inst.WorkspaceID, UserID: b.UserID}); err == nil {
				user, bound = b.UserID, true
				return nil
			}
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		link, err = s.MintLink(ctx, tx, inst, m.User)
		return err
	})
	if err != nil {
		return err
	}
	c, err := s.session(inst)
	if err != nil {
		return err
	}
	opts := func(text string) []slackgo.MsgOption {
		o := []slackgo.MsgOption{slackgo.MsgOptionText(text, false)}
		if m.ThreadTS != "" || m.ChannelType != "im" {
			o = append(o, slackgo.MsgOptionTS(thread))
		}
		return o
	}
	say := func(r reply) error {
		if r.ephemeral {
			_, err := c.PostEphemeralContext(ctx, m.Channel, m.User, opts(r.text)...)
			return c.err(err)
		}
		_, _, err := c.PostMessageContext(ctx, m.Channel, append(opts(r.text), slackgo.MsgOptionTS(thread))...)
		return c.err(err)
	}
	if !bound {
		committed = true
		return say(reply{true, "Link your Slack account to Lykeion to ask Aristotle: " + link})
	}
	text := strings.TrimSpace(strings.ReplaceAll(m.Text, "<@"+inst.BotUserID+">", ""))
	switch {
	case text == "":
		return say(reply{true, helpText})
	case utf8.RuneCountInString(text) > maxQuestion:
		return say(reply{true, "Questions can be up to 8000 characters."})
	}
	err = s.Store.InWorkspace(ctx, inst.WorkspaceID, user, func(tx *store.Tx) error {
		harness := ""
		for _, h := range []string{"claude", "codex"} {
			ok, err := tx.Q.AristotleRuntimeAvailable(ctx, db.AristotleRuntimeAvailableParams{Harness: h, RequireHealthy: true})
			if err != nil {
				return err
			}
			if ok {
				harness = h
				break
			}
		}
		if harness == "" {
			replies = append(replies, reply{false, offlineText})
			return nil
		}
		key := db.GetSlackChatParams{InstallationID: inst.ID, UserID: user, Channel: m.Channel, ThreadTs: thread}
		if m.ChannelType == "im" && m.ThreadTS == "" {
			key.ThreadTs = ""
		}
		var chat db.LabChat
		sc, err := tx.Q.GetSlackChat(ctx, key)
		if errors.Is(err, pgx.ErrNoRows) {
			if chat, err = tx.Q.CreateLabChat(ctx); err == nil {
				_, err = tx.Q.UpsertSlackChat(ctx, db.UpsertSlackChatParams{InstallationID: inst.ID, UserID: user, Channel: key.Channel, ThreadTs: key.ThreadTs, LabChatID: chat.ID})
			}
		} else if err == nil {
			// A chat deleted in the dashboard is only soft-deleted: start a new one.
			if chat, err = tx.Q.GetPrivateLabChat(ctx, sc.LabChatID); errors.Is(err, pgx.ErrNoRows) {
				if chat, err = tx.Q.CreateLabChat(ctx); err == nil {
					_, err = tx.Q.UpsertSlackChat(ctx, db.UpsertSlackChatParams{InstallationID: inst.ID, UserID: user, Channel: key.Channel, ThreadTs: key.ThreadTs, LabChatID: chat.ID})
				}
			}
		}
		if err != nil {
			return err
		}
		run, err := labmemory.EnqueueWithContext(ctx, tx, s.AgentRuns, chat, user, text, nil, nil, harness, "")
		if err != nil {
			return err
		}
		title := []rune(text)
		if len(title) > 40 {
			title = append(title[:40], '…')
		}
		if err = tx.Q.TouchLabChat(ctx, db.TouchLabChatParams{ID: chat.ID, Title: string(title)}); err != nil {
			return err
		}
		_, err = tx.Q.CreateSlackPost(ctx, db.CreateSlackPostParams{InstallationID: inst.ID, UserID: user, Channel: m.Channel, ThreadTs: thread, Kind: "answer", AgentRunID: &run.ID})
		react = true
		return err
	})
	if err != nil {
		return err
	}
	committed = true
	for _, r := range replies {
		if err := say(r); err != nil {
			return err
		}
	}
	if react {
		if err := c.AddReactionContext(ctx, "hourglass_flowing_sand", slackgo.NewRefToMessage(m.Channel, m.TS)); err != nil {
			return c.err(err)
		}
		if m.HasFiles {
			return say(reply{false, filesText})
		}
	}
	return nil
}
