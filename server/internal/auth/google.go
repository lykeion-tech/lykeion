package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const googleResponseLimit = 1 << 20

type Google struct {
	ClientID, Secret, RedirectURL  string
	AuthURL, TokenURL, UserinfoURL string
	HTTP                           *http.Client
}

type GoogleUser struct {
	Sub, Email, Name string
}

// GoogleDefaults fills Google's public OAuth and userinfo endpoints.
func GoogleDefaults(clientID, secret, redirect string) Google {
	return Google{ClientID: clientID, Secret: secret, RedirectURL: redirect,
		AuthURL:     "https://accounts.google.com/o/oauth2/v2/auth",
		TokenURL:    "https://oauth2.googleapis.com/token",
		UserinfoURL: "https://openidconnect.googleapis.com/v1/userinfo"}
}

func (g Google) Enabled() bool { return g.ClientID != "" && g.Secret != "" }

func (g Google) AuthCodeURL(state string) string {
	v := url.Values{
		"client_id":     {g.ClientID},
		"redirect_uri":  {g.RedirectURL},
		"response_type": {"code"},
		"scope":         {"openid email profile"},
		"state":         {state},
	}
	return g.AuthURL + "?" + v.Encode()
}

func (g Google) client() *http.Client {
	if g.HTTP != nil {
		return g.HTTP
	}
	return &http.Client{Timeout: 15 * time.Second}
}

func decodeGoogleResponse(body io.Reader, target any) error {
	data, err := io.ReadAll(io.LimitReader(body, googleResponseLimit+1))
	if err != nil {
		return err
	}
	if len(data) > googleResponseLimit {
		return errors.New("Google response exceeds size limit")
	}
	return json.Unmarshal(data, target)
}

// Exchange trades an authorization code for a Google identity.
func (g Google) Exchange(ctx context.Context, code string) (GoogleUser, error) {
	if strings.TrimSpace(code) == "" || !g.Enabled() || g.TokenURL == "" || g.UserinfoURL == "" {
		return GoogleUser{}, errors.New("google OAuth configuration or code is incomplete")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	form := url.Values{
		"code":          {code},
		"client_id":     {g.ClientID},
		"client_secret": {g.Secret},
		"redirect_uri":  {g.RedirectURL},
		"grant_type":    {"authorization_code"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return GoogleUser{}, fmt.Errorf("create Google token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := g.client().Do(req)
	if err != nil {
		return GoogleUser{}, fmt.Errorf("Google token request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return GoogleUser{}, fmt.Errorf("Google token endpoint returned %s", resp.Status)
	}
	var token struct {
		AccessToken string `json:"access_token"`
	}
	if err := decodeGoogleResponse(resp.Body, &token); err != nil {
		return GoogleUser{}, fmt.Errorf("decode Google token response: %w", err)
	}
	if strings.TrimSpace(token.AccessToken) == "" {
		return GoogleUser{}, errors.New("Google token response has no access token")
	}

	req, err = http.NewRequestWithContext(ctx, http.MethodGet, g.UserinfoURL, nil)
	if err != nil {
		return GoogleUser{}, fmt.Errorf("create Google userinfo request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token.AccessToken)
	resp, err = g.client().Do(req)
	if err != nil {
		return GoogleUser{}, fmt.Errorf("Google userinfo request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return GoogleUser{}, fmt.Errorf("Google userinfo endpoint returned %s", resp.Status)
	}
	var user struct {
		Sub           string `json:"sub"`
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
		Name          string `json:"name"`
	}
	if err := decodeGoogleResponse(resp.Body, &user); err != nil {
		return GoogleUser{}, fmt.Errorf("decode Google userinfo response: %w", err)
	}
	user.Sub = strings.TrimSpace(user.Sub)
	user.Email = strings.ToLower(strings.TrimSpace(user.Email))
	if user.Sub == "" {
		return GoogleUser{}, errors.New("Google userinfo has no subject")
	}
	if !user.EmailVerified || user.Email == "" {
		return GoogleUser{}, errors.New("Google account email is not verified")
	}
	return GoogleUser{Sub: user.Sub, Email: user.Email, Name: user.Name}, nil
}
