// Package slack connects a workspace's own Slack app to Aristotle.
package slack

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	slackgo "github.com/slack-go/slack"

	"lykeion/server/internal/service"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

// DraftIn is a study draft asked for from Slack.
type DraftIn struct {
	AgentID          uuid.UUID
	Prompt, Priority string
}

// Drafter queues a study draft for a member; handler.H implements it.
type Drafter interface {
	SlackDraft(ctx context.Context, ws, user uuid.UUID, in DraftIn) (uuid.UUID, error)
}

type Service struct {
	Store     *store.Store
	Key       []byte
	APIURL    string // "" = Slack itself
	Redis     *redis.Client
	AgentRuns *service.AgentRuns
	Drafter   Drafter
	PublicURL string
}

// Identity is what the two tokens say about the Slack app.
type Identity struct{ TeamID, TeamName, AppID, BotUserID string }

func (s *Service) newClient(bot, app string) *slackgo.Client {
	opts := []slackgo.Option{slackgo.OptionAppLevelToken(app)}
	if s.APIURL != "" {
		opts = append(opts, slackgo.OptionAPIURL(strings.TrimRight(s.APIURL, "/")+"/"))
	}
	return slackgo.New(bot, opts...)
}

// client opens the installation's sealed bot token.
func (s *Service) client(inst db.SlackInstallation) (*slackgo.Client, error) {
	bot, err := s.Open(inst.WorkspaceID, inst.ID, "bot", inst.BotTokenCiphertext)
	if err != nil {
		return nil, err
	}
	app, err := s.Open(inst.WorkspaceID, inst.ID, "app", inst.AppTokenCiphertext)
	if err != nil {
		return nil, err
	}
	return s.newClient(bot, app), nil
}

// Verify checks the bot token with auth.test and the app token with
// apps.connections.open. Errors never contain either token.
func (s *Service) Verify(ctx context.Context, botToken, appToken string) (Identity, error) {
	// An app token reads xapp-1-<app id>-...; the app id keys the installation.
	parts := strings.Split(appToken, "-")
	if !strings.HasPrefix(botToken, "xoxb-") {
		return Identity{}, errors.New("The bot token starts with xoxb-")
	}
	if len(parts) < 4 || parts[0] != "xapp" || parts[2] == "" {
		return Identity{}, errors.New("The app token starts with xapp-")
	}
	c := s.newClient(botToken, appToken)
	a, err := c.AuthTestContext(ctx)
	if err != nil {
		return Identity{}, slackError(err, "bot", botToken, appToken)
	}
	if _, _, err := c.StartSocketModeContext(ctx); err != nil {
		return Identity{}, slackError(err, "app", botToken, appToken)
	}
	return Identity{TeamID: a.TeamID, TeamName: a.Team, AppID: parts[2], BotUserID: a.UserID}, nil
}

// slackError turns a Slack failure into a message that is safe to show.
func slackError(err error, which string, secrets ...string) error {
	switch msg := err.Error(); {
	case strings.Contains(msg, "invalid_auth"), strings.Contains(msg, "not_authed"),
		strings.Contains(msg, "token_revoked"), strings.Contains(msg, "account_inactive"):
		return errors.New("Slack rejected the " + which + " token")
	}
	return scrub(errors.New("Slack could not check the "+which+" token: "+err.Error()), secrets...)
}

// scrub removes every secret from err's text.
func scrub(err error, secrets ...string) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	for _, s := range secrets {
		if s != "" {
			msg = strings.ReplaceAll(msg, s, "[redacted]")
		}
	}
	return errors.New(msg)
}
