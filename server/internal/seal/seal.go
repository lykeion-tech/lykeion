// Package seal encrypts small secrets at rest, such as webhook signing
// secrets, with AES-256-GCM under the server's key.
package seal

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
)

var ErrNoKey = errors.New("the server has no LYK_SECRET_KEY: secrets cannot be sealed or opened")

// ParseKey reads LYK_SECRET_KEY, standard base64 of exactly 32 bytes. An empty
// value means no key.
func ParseKey(s string) ([]byte, error) {
	if s == "" {
		return nil, nil
	}
	k, err := base64.StdEncoding.DecodeString(s)
	if err != nil || len(k) != 32 {
		return nil, errors.New("LYK_SECRET_KEY must be base64 of exactly 32 bytes (openssl rand -base64 32)")
	}
	return k, nil
}

func aead(key []byte) (cipher.AEAD, error) {
	if len(key) == 0 {
		return nil, ErrNoKey
	}
	b, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(b)
}

// Seal returns nonce ‖ ciphertext. ad binds the result to its row, so a sealed
// value copied onto another row does not open there.
func Seal(key, plain, ad []byte) ([]byte, error) {
	g, err := aead(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, g.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return g.Seal(nonce, nonce, plain, ad), nil
}

func Open(key, sealed, ad []byte) ([]byte, error) {
	g, err := aead(key)
	if err != nil {
		return nil, err
	}
	if len(sealed) < g.NonceSize() {
		return nil, errors.New("sealed value too short")
	}
	return g.Open(nil, sealed[:g.NonceSize()], sealed[g.NonceSize():], ad)
}
