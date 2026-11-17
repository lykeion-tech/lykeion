package jev

import (
	"testing"

	"github.com/google/uuid"
)

func TestSealedKeyOpensOnlyForItsOwner(t *testing.T) {
	secret := make([]byte, 32)
	alice, bob := uuid.New(), uuid.New()
	sealed, err := SealKey(secret, alice, "ts-key")
	if err != nil {
		t.Fatal(err)
	}
	if k, err := OpenKey(secret, alice, sealed); err != nil || k != "ts-key" {
		t.Fatalf("owner: %q %v", k, err)
	}
	if _, err := OpenKey(secret, bob, sealed); err == nil {
		t.Fatal("another person opened the key")
	}
	if _, err := SealKey(nil, alice, "ts-key"); err == nil {
		t.Fatal("sealed without a server secret")
	}
}
