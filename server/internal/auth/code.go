package auth

import (
	"crypto/rand"
	"fmt"
	"math/big"
)

func NewCode() string {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		panic(err)
	}
	return fmt.Sprintf("%06d", n.Int64())
}

// CodeHash binds a code to the email it was sent to.
func CodeHash(email, code string) string { return Hash(email + ":" + code) }
