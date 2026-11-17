package jev

import (
	"github.com/google/uuid"

	"lykeion/server/internal/seal"
)

// keyAD binds a sealed TypeSafe key to the person who saved it.
func keyAD(user uuid.UUID) []byte { return []byte("user/" + user.String() + "/typesafe") }

// SealKey seals a person's TypeSafe key under the server's LYK_SECRET_KEY.
func SealKey(secret []byte, user uuid.UUID, key string) ([]byte, error) {
	return seal.Seal(secret, []byte(key), keyAD(user))
}

// OpenKey opens what SealKey sealed for the same person.
func OpenKey(secret []byte, user uuid.UUID, sealed []byte) (string, error) {
	k, err := seal.Open(secret, sealed, keyAD(user))
	return string(k), err
}
