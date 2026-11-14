package auth

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

type Issuer struct {
	Secret []byte
	TTL    time.Duration
}

type claims struct {
	jwt.RegisteredClaims
	Email string `json:"email"`
}

func (i Issuer) Issue(user uuid.UUID, email string) (string, error) {
	now := time.Now()
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims{
		RegisteredClaims: jwt.RegisteredClaims{Subject: user.String(), IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(i.TTL))},
		Email:            email,
	}).SignedString(i.Secret)
}

func (i Issuer) Parse(tok string) (uuid.UUID, string, error) {
	var c claims
	_, err := jwt.ParseWithClaims(tok, &c, func(*jwt.Token) (any, error) { return i.Secret, nil },
		jwt.WithValidMethods([]string{"HS256"}), jwt.WithExpirationRequired())
	if err != nil {
		return uuid.Nil, "", err
	}
	u, err := uuid.Parse(c.Subject)
	if err != nil {
		return uuid.Nil, "", fmt.Errorf("bad subject: %w", err)
	}
	return u, c.Email, nil
}
