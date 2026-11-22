package github

import (
	"crypto/rsa"
	"errors"
	"strconv"

	"github.com/golang-jwt/jwt/v5"
)

const (
	defaultAPIURL = "https://api.github.com"
	defaultWebURL = "https://github.com"
)

type Config struct {
	AppID         int64
	PrivateKey    *rsa.PrivateKey
	Slug          string
	ClientID      string
	ClientSecret  string
	WebhookSecret string
	APIURL        string
	WebURL        string
}

func ConfigFromEnv(getenv func(string) string) (Config, error) {
	c := Config{
		Slug:          getenv("GITHUB_APP_SLUG"),
		ClientID:      getenv("GITHUB_APP_CLIENT_ID"),
		ClientSecret:  getenv("GITHUB_APP_CLIENT_SECRET"),
		WebhookSecret: getenv("GITHUB_WEBHOOK_SECRET"),
		APIURL:        defaultAPIURL,
		WebURL:        defaultWebURL,
	}
	if raw := getenv("GITHUB_APP_ID"); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			return Config{}, errors.New("GITHUB_APP_ID must be a positive integer")
		}
		c.AppID = id
	}
	if raw := getenv("GITHUB_APP_PRIVATE_KEY"); raw != "" {
		key, err := jwt.ParseRSAPrivateKeyFromPEM([]byte(raw))
		if err != nil {
			return Config{}, errors.New("GITHUB_APP_PRIVATE_KEY must be a PEM-encoded RSA private key")
		}
		c.PrivateKey = key
	}
	return c, nil
}

func (c Config) CanConnect() bool {
	return c.AppID > 0 && c.PrivateKey != nil && c.Slug != "" && c.ClientID != "" &&
		c.ClientSecret != "" && c.WebhookSecret != ""
}

func (c Config) apiURL() string {
	if c.APIURL == "" {
		return defaultAPIURL
	}
	return c.APIURL
}

func (c Config) webURL() string {
	if c.WebURL == "" {
		return defaultWebURL
	}
	return c.WebURL
}
