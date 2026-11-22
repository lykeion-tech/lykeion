package slack

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

var (
	ErrLinkExpired   = errors.New("This link has expired. Message Aristotle in Slack for a new one.")
	ErrEmailMismatch = errors.New("Your Slack email doesn't match your Lykeion email.")
)

const linkTTL = 15 * time.Minute

func hashToken(raw string) string { h := sha256.Sum256([]byte(raw)); return hex.EncodeToString(h[:]) }

// MintLink stores a single-use link token for a Slack user and returns the
// dashboard URL that redeems it. Only the token's hash is stored.
func (s *Service) MintLink(ctx context.Context, tx *store.Tx, inst db.SlackInstallation, slackUser string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	raw := base64.RawURLEncoding.EncodeToString(b)
	err := tx.Q.CreateSlackLinkToken(ctx, db.CreateSlackLinkTokenParams{InstallationID: inst.ID, SlackUserID: slackUser,
		TokenHash: hashToken(raw), ExpiresAt: time.Now().Add(linkTTL - time.Minute)})
	if err != nil {
		return "", err
	}
	return strings.TrimRight(s.PublicURL, "/") + "/app/#/slack/link?workspace=" + inst.WorkspaceID.String() + "&token=" + raw, nil
}

// Redeem spends a link token, checks that the Slack profile's confirmed email is
// the member's, and binds the Slack user to the member. A mismatch still spends
// the token: the spend commits before Slack is asked.
func (s *Service) Redeem(ctx context.Context, ws, user uuid.UUID, userEmail, raw string) (string, error) {
	var tok db.SlackLinkToken
	var inst db.SlackInstallation
	err := s.Store.InWorkspace(ctx, ws, user, func(tx *store.Tx) (err error) {
		if tok, err = tx.Q.UseSlackLinkToken(ctx, hashToken(raw)); err != nil {
			return err
		}
		inst, err = tx.Q.GetSlackInstallation(ctx)
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrLinkExpired
	} else if err != nil {
		return "", err
	}
	c, err := s.session(inst)
	if err != nil {
		return "", err
	}
	u, err := c.GetUserInfoContext(ctx, tok.SlackUserID)
	if err != nil {
		return "", c.err(err, raw)
	}
	if !u.IsEmailConfirmed || u.Profile.Email == "" || !strings.EqualFold(u.Profile.Email, userEmail) {
		return "", ErrEmailMismatch
	}
	err = s.Store.InWorkspace(ctx, ws, user, func(tx *store.Tx) error {
		_, err := tx.Q.CreateSlackBinding(ctx, db.CreateSlackBindingParams{InstallationID: inst.ID, SlackUserID: tok.SlackUserID, UserID: user})
		return err
	})
	return tok.SlackUserID, err
}
