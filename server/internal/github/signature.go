package github

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

func VerifySignature(secret, header string, body []byte) bool {
	digest, ok := strings.CutPrefix(header, "sha256=")
	if secret == "" || !ok || len(digest) != sha256.Size*2 {
		return false
	}
	want, err := hex.DecodeString(digest)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal(mac.Sum(nil), want)
}
