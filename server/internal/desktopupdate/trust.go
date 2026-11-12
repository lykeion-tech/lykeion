package desktopupdate

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
)

// Release inputs are embedded before signing and never read at runtime.
var embeddedKeys string
var embeddedVersion string

type BuildTrust struct {
	Version string   `json:"version"`
	KeyIDs  []string `json:"keyIDs"`
}

type Result struct {
	State   string `json:"state"`
	Version string `json:"version"`
}

// EmbeddedBuildTrust is a read-only local build check and does not make requests.
func EmbeddedBuildTrust() (BuildTrust, error) {
	keys, err := parseBuildTrust(embeddedKeys, embeddedVersion)
	if err != nil {
		return BuildTrust{}, err
	}
	result := BuildTrust{Version: embeddedVersion, KeyIDs: make([]string, 0, len(keys))}
	for _, key := range keys {
		der, err := x509.MarshalPKIXPublicKey(key)
		if err != nil {
			return BuildTrust{}, errors.New("update trust unavailable")
		}
		digest := sha256.Sum256(der)
		result.KeyIDs = append(result.KeyIDs, hex.EncodeToString(digest[:]))
	}
	return result, nil
}

func parseBuildTrust(encoded, version string) ([]ed25519.PublicKey, error) {
	errUnsafe := errors.New("update trust unavailable")
	if !stableVersion.MatchString(version) || len(encoded) == 0 || len(encoded) > 512 {
		return nil, errUnsafe
	}
	parts := strings.Split(encoded, ",")
	if len(parts) == 0 || len(parts) > 2 {
		return nil, errUnsafe
	}
	keys := make([]ed25519.PublicKey, 0, len(parts))
	for _, part := range parts {
		der, err := base64.StdEncoding.Strict().DecodeString(part)
		if err != nil || base64.StdEncoding.EncodeToString(der) != part {
			return nil, errUnsafe
		}
		parsed, err := x509.ParsePKIXPublicKey(der)
		if err != nil {
			return nil, errUnsafe
		}
		key, ok := parsed.(ed25519.PublicKey)
		if !ok {
			return nil, errUnsafe
		}
		canonical, err := x509.MarshalPKIXPublicKey(key)
		if err != nil || !bytes.Equal(canonical, der) {
			return nil, errUnsafe
		}
		keys = append(keys, key)
	}
	if _, err := trustedKeyMap(keys); err != nil {
		return nil, errUnsafe
	}
	return keys, nil
}
