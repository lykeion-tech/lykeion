// Package auth holds the pieces of sign-in: tokens, sessions, emailed codes,
// mail and rate limits.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
)

const PersonalPrefix = "lyk_"

// NewToken returns the token to hand out once, the hash to store, and a short
// prefix that identifies it in lists.
func NewToken(prefix string) (plain, hash, shown string) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	plain = prefix + base64.RawURLEncoding.EncodeToString(b)
	return plain, Hash(plain), plain[:len(prefix)+6]
}

func Hash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}
