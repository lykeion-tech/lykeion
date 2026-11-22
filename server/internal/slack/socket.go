package slack

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"

	slackgo "github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"

	db "lykeion/server/pkg/db/generated"
)

var errRevoked = errors.New("slack installation revoked")

// toMessage maps an app_mention, or a direct message, to a Message.
func toMessage(e slackevents.EventsAPIEvent) (Message, bool) {
	m := Message{AppID: e.APIAppID}
	if cb, ok := e.Data.(*slackevents.EventsAPICallbackEvent); ok {
		m.EventID = cb.EventID
	}
	switch ev := e.InnerEvent.Data.(type) {
	case *slackevents.AppMentionEvent:
		m.User, m.Channel, m.ChannelType, m.TS, m.ThreadTS, m.Text, m.BotID = ev.User, ev.Channel, "channel", ev.TimeStamp, ev.ThreadTimeStamp, ev.Text, ev.BotID
		m.HasFiles = len(ev.Files) > 0
		if ev.Edited != nil {
			m.Subtype = "message_changed"
		}
	case *slackevents.MessageEvent:
		if ev.ChannelType != "im" {
			return Message{}, false
		}
		m.User, m.Channel, m.ChannelType, m.TS, m.ThreadTS, m.Text, m.BotID, m.Subtype = ev.User, ev.Channel, ev.ChannelType, ev.TimeStamp, ev.ThreadTimeStamp, ev.Text, ev.BotID, ev.SubType
		m.HasFiles = ev.Message != nil && len(ev.Message.Files) > 0
	default:
		return Message{}, false
	}
	return m, true
}

// revokes reports whether the event says the app lost its tokens.
func revokes(e slackevents.EventsAPIEvent) bool {
	t := slackevents.EventsAPIType(e.InnerEvent.Type)
	return t == slackevents.AppUninstalled || t == slackevents.TokensRevoked
}

func toCommand(c slackgo.SlashCommand) Command {
	return Command{Name: c.Command, Text: c.Text, User: c.UserID, Channel: c.ChannelID, ResponseURL: c.ResponseURL}
}

func toAction(ic slackgo.InteractionCallback) (Action, bool) {
	if ic.Type != slackgo.InteractionTypeBlockActions || len(ic.ActionCallback.BlockActions) == 0 {
		return Action{}, false
	}
	return Action{ID: ic.ActionCallback.BlockActions[0].ActionID, User: ic.User.ID, Channel: ic.Channel.ID, ResponseURL: ic.ResponseURL}, true
}

func authFailed(err error) bool {
	for _, code := range []string{"invalid_auth", "not_authed", "token_revoked", "account_inactive"} {
		if strings.Contains(err.Error(), code) {
			return true
		}
	}
	return false
}

// socket runs the installation's Socket Mode connection until ctx is done,
// the connection fails, or Slack revokes the app (which marks it revoked).
func (s *Service) socket(ctx context.Context, inst db.SlackInstallation) error {
	c, err := s.session(inst)
	if err != nil {
		return err
	}
	sm := socketmode.New(c.Client)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	ended := make(chan error, 1)
	go func() { ended <- sm.RunContext(ctx) }()
	revoke := func() error {
		if err := s.revoke(context.WithoutCancel(ctx), inst); err != nil {
			return c.err(err)
		}
		return errRevoked
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-ended:
			if err != nil && authFailed(err) {
				return revoke()
			}
			return c.err(err)
		case evt := <-sm.Events:
			if evt.Type == socketmode.EventTypeInvalidAuth {
				return revoke()
			}
			if evt.Request == nil {
				continue
			}
			// Ack first: Slack retries anything not acked within 3 seconds.
			if err := sm.AckCtx(ctx, evt.Request.EnvelopeID, nil); err != nil {
				slog.Warn("slack ack failed", "installation", inst.ID, "err", c.err(err))
			}
			if e, ok := evt.Data.(slackevents.EventsAPIEvent); ok && revokes(e) {
				return revoke()
			}
			go s.dispatch(ctx, c, inst, evt)
		}
	}
}

// dispatch handles one acked envelope against a fresh copy of the
// installation, so Settings changes apply without reconnecting.
func (s *Service) dispatch(ctx context.Context, c *session, inst db.SlackInstallation, evt socketmode.Event) {
	inst, err := s.load(ctx, inst.WorkspaceID, inst.ID)
	if err != nil || inst.Status != "active" {
		return
	}
	switch d := evt.Data.(type) {
	case slackevents.EventsAPIEvent:
		if m, ok := toMessage(d); ok {
			err = s.HandleMessage(ctx, inst, m)
		}
	case slackgo.SlashCommand:
		cmd := toCommand(d)
		text, blocks, herr := s.HandleCommand(ctx, inst, cmd)
		err = errors.Join(herr, s.respond(ctx, c, cmd.ResponseURL, text, blocks, false))
	case slackgo.InteractionCallback:
		a, ok := toAction(d)
		if !ok {
			return
		}
		text, herr := s.HandleAction(ctx, inst, a)
		if text != "" {
			herr = errors.Join(herr, s.respond(ctx, c, a.ResponseURL, text, nil, true))
		}
		err = herr
	}
	if err != nil && ctx.Err() == nil {
		slog.Warn("slack event failed", "installation", inst.ID, "type", string(evt.Type), "err", c.err(err))
	}
}

// respond posts an ephemeral reply through a response_url; an action's reply
// replaces the message holding the button.
func (s *Service) respond(ctx context.Context, c *session, url, text string, blocks json.RawMessage, replace bool) error {
	if url == "" || text == "" {
		return nil
	}
	opts := []slackgo.MsgOption{slackgo.MsgOptionText(text, false), slackgo.MsgOptionResponseURL(url, slackgo.ResponseTypeEphemeral)}
	if replace {
		opts = append(opts, slackgo.MsgOptionReplaceOriginal(url))
	}
	if blocks != nil {
		var b slackgo.Blocks
		if err := json.Unmarshal(blocks, &b); err != nil {
			return err
		}
		opts = append(opts, slackgo.MsgOptionBlocks(b.BlockSet...))
	}
	_, _, err := c.PostMessageContext(ctx, "", opts...)
	return c.err(err, url)
}
