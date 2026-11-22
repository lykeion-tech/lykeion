package slack

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	slackgo "github.com/slack-go/slack"

	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

// ClearConfirmAction is the action_id of the /clear confirmation button.
const ClearConfirmAction = "slack_clear_confirm"

// Command is a slash command, transport-neutral.
type Command struct{ Name, Text, User, Channel, ResponseURL string }

// Action is a Block Kit button press, transport-neutral.
type Action struct{ ID, User, Channel, ResponseURL string }

const genericError = "Something went wrong. Try again."

// HandleCommand runs a slash command as its linked sender. The transport posts
// the returned text (and blocks, when set) as an ephemeral reply.
func (s *Service) HandleCommand(ctx context.Context, inst db.SlackInstallation, c Command) (text string, blocks json.RawMessage, err error) {
	user, bound, link, err := s.resolve(ctx, inst, c.User)
	if err != nil {
		return genericError, nil, err
	}
	if !bound {
		return linkText(link), nil, nil
	}
	name := strings.TrimPrefix(c.Name, "/")
	if name == "help" {
		return helpText, nil, nil
	}
	if name == "clear" {
		b, err := json.Marshal([]slackgo.Block{
			slackgo.NewSectionBlock(slackgo.NewTextBlockObject("mrkdwn", "Delete your current conversation with Aristotle?", false, false), nil, nil),
			slackgo.NewActionBlock("", slackgo.NewButtonBlockElement(ClearConfirmAction, "clear", slackgo.NewTextBlockObject("plain_text", "Delete conversation", false, false)).WithStyle(slackgo.StyleDanger)),
		})
		return "Delete your current conversation?", b, err
	}
	if name == "study" && strings.TrimSpace(c.Text) == "" {
		return "Usage: /study <what you want to study>", nil, nil
	}
	if name != "study" && name != "new" {
		return "Unknown command. " + helpText, nil, nil
	}
	cl, err := s.session(inst)
	if err != nil {
		return genericError, nil, err
	}
	dm, err := cl.dmChannel(ctx, c.User)
	if err != nil {
		return genericError, nil, err
	}
	if name == "new" {
		err = s.Store.InWorkspace(ctx, inst.WorkspaceID, user, func(tx *store.Tx) error {
			return tx.Q.DeleteSlackDMChat(ctx, db.DeleteSlackDMChatParams{InstallationID: inst.ID, UserID: user, Channel: dm})
		})
		if err != nil {
			return genericError, nil, err
		}
		return "Your next message starts a new conversation.", nil, nil
	}
	if inst.DraftingAgentID == nil {
		return "Slack has no drafting agent yet. Ask the workspace owner to pick one.", nil, nil
	}
	id, err := s.Drafter.SlackDraft(ctx, inst.WorkspaceID, user, DraftIn{AgentID: *inst.DraftingAgentID, Prompt: strings.TrimSpace(c.Text), Priority: "none"})
	if err != nil {
		var refused UserError
		if errors.As(err, &refused) {
			return refused.Error(), nil, nil
		}
		return genericError, nil, err
	}
	err = s.Store.InWorkspace(ctx, inst.WorkspaceID, user, func(tx *store.Tx) error {
		_, err := tx.Q.CreateSlackPost(ctx, db.CreateSlackPostParams{InstallationID: inst.ID, UserID: user, Channel: dm, Kind: "draft", AutoresearchID: &id})
		return err
	})
	if err != nil {
		return genericError, nil, err
	}
	return "Drafting your study. I'll DM you the link.", nil, nil
}

// HandleAction runs a button press and returns the ephemeral reply text.
func (s *Service) HandleAction(ctx context.Context, inst db.SlackInstallation, a Action) (string, error) {
	if a.ID != ClearConfirmAction {
		return "", nil
	}
	user, bound, link, err := s.resolve(ctx, inst, a.User)
	if err != nil {
		return genericError, err
	}
	if !bound {
		return linkText(link), nil
	}
	cl, err := s.session(inst)
	if err != nil {
		return genericError, err
	}
	dm, err := cl.dmChannel(ctx, a.User)
	if err != nil {
		return genericError, err
	}
	text := "Conversation deleted."
	err = s.Store.InWorkspace(ctx, inst.WorkspaceID, user, func(tx *store.Tx) error {
		if _, err := tx.Q.LockWorkspaceExecution(ctx, tx.WS); err != nil {
			return err
		}
		sc, err := tx.Q.GetSlackChat(ctx, db.GetSlackChatParams{InstallationID: inst.ID, UserID: user, Channel: dm})
		if err == nil {
			if err = tx.Q.DeleteSlackDMChat(ctx, db.DeleteSlackDMChatParams{InstallationID: inst.ID, UserID: user, Channel: dm}); err != nil {
				return err
			}
			var chat db.LabChat
			if chat, err = tx.Q.GetPrivateLabChat(ctx, sc.LabChatID); err == nil {
				if s.DeleteChat == nil {
					return errors.New("slack: DeleteChat is not set")
				}
				return s.DeleteChat(ctx, tx, user, chat)
			}
		}
		if errors.Is(err, pgx.ErrNoRows) {
			text = "There is no conversation to delete."
			return nil
		}
		return err
	})
	if err != nil {
		return genericError, err
	}
	return text, nil
}

// dmChannel opens (or finds) the direct message channel with a Slack user.
func (c *session) dmChannel(ctx context.Context, slackUser string) (string, error) {
	ch, _, _, err := c.OpenConversationContext(ctx, &slackgo.OpenConversationParameters{Users: []string{slackUser}})
	if err != nil {
		return "", c.err(err)
	}
	return ch.ID, nil
}
