// Package desktopupdate implements a check-and-cache operation. It never installs updates.
package desktopupdate

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"filippo.io/edwards25519"
)

const (
	signatureDomain    = "Lykeion desktop update v1\x00"
	maxEnvelope        = 64 << 10
	maxPayload         = 32 << 10
	maxArtifact        = 1 << 30
	maxSafeInteger     = 1<<53 - 1
	maxCacheBytes      = int64(2 << 30)
	reserveBytes       = int64(256 << 20)
	cacheEntryOverhead = int64(64 << 10)
	maxCacheEntries    = 32
)

var stableVersion = regexp.MustCompile(`^(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})$`)

type metadata struct {
	sequence                 int64
	version                  string
	issued, expires          int64
	platform, arch, filename string
	size                     int64
	digest                   string
	payloadDigest            [32]byte
}

func validMetadataTime(issued, expires int64, now time.Time) bool {
	n := now.Unix()
	return n > 0 && n <= maxSafeInteger-3600 && issued > 0 && expires <= maxSafeInteger &&
		expires > issued && expires-issued <= int64((7*24*time.Hour)/time.Second) &&
		issued <= n+int64(time.Hour/time.Second) && expires > n
}

func canonicalArray(raw []byte, count int) ([]json.RawMessage, error) {
	var fields []json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || len(fields) != count {
		return nil, errors.New("invalid metadata format")
	}
	encoded, err := json.Marshal(fields)
	if err != nil || !bytes.Equal(encoded, raw) {
		return nil, errors.New("noncanonical metadata")
	}
	return fields, nil
}

func scalar[T any](raw json.RawMessage, value *T) error {
	if err := json.Unmarshal(raw, value); err != nil {
		return errors.New("invalid metadata field")
	}
	encoded, err := json.Marshal(value)
	if err != nil || !bytes.Equal(encoded, raw) {
		return errors.New("noncanonical metadata field")
	}
	return nil
}

func decode64(value string, limit int) ([]byte, error) {
	if len(value) > base64.StdEncoding.EncodedLen(limit) {
		return nil, errors.New("encoded value too large")
	}
	decoded, err := base64.StdEncoding.Strict().DecodeString(value)
	if err != nil || len(decoded) > limit || base64.StdEncoding.EncodeToString(decoded) != value {
		return nil, errors.New("invalid base64")
	}
	return decoded, nil
}

// Trust anchors must be canonical, non-identity points in the prime-order subgroup.
func validPublicKey(key ed25519.PublicKey) bool {
	point, err := new(edwards25519.Point).SetBytes(key)
	if err != nil || !bytes.Equal(point.Bytes(), key) || point.Equal(edwards25519.NewIdentityPoint()) == 1 {
		return false
	}
	// The library accepts scalars below the subgroup order; use (order - 1)P + P.
	orderMinusOne := [32]byte{0xec, 0xd3, 0xf5, 0x5c, 0x1a, 0x63, 0x12, 0x58, 0xd6, 0x9c, 0xf7, 0xa2, 0xde, 0xf9, 0xde, 0x14, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0x10}
	scalar, err := new(edwards25519.Scalar).SetCanonicalBytes(orderMinusOne[:])
	if err != nil {
		return false
	}
	multiple := new(edwards25519.Point).ScalarMult(scalar, point)
	return new(edwards25519.Point).Add(multiple, point).Equal(edwards25519.NewIdentityPoint()) == 1
}

func trustedKeyMap(keys []ed25519.PublicKey) (map[string]ed25519.PublicKey, error) {
	if len(keys) == 0 || len(keys) > 2 {
		return nil, errors.New("update trust unavailable")
	}
	trusted := make(map[string]ed25519.PublicKey, len(keys))
	for _, key := range keys {
		if len(key) != ed25519.PublicKeySize || !validPublicKey(key) {
			return nil, errors.New("update trust unavailable")
		}
		der, err := x509.MarshalPKIXPublicKey(key)
		if err != nil {
			return nil, errors.New("update trust unavailable")
		}
		id := sha256.Sum256(der)
		name := hex.EncodeToString(id[:])
		if _, duplicate := trusted[name]; duplicate {
			return nil, errors.New("update trust unavailable")
		}
		trusted[name] = key
	}
	return trusted, nil
}

func verifyMetadata(raw []byte, keys []ed25519.PublicKey, platform, arch string, now time.Time) (metadata, error) {
	var result metadata
	bad := errors.New("update metadata could not be verified")
	if len(raw) == 0 || len(raw) > maxEnvelope || len(keys) == 0 || len(keys) > 2 {
		return result, bad
	}
	envelope, err := canonicalArray(raw, 3)
	if err != nil {
		return result, bad
	}
	var format int64
	var payload64 string
	if scalar(envelope[0], &format) != nil || format != 1 || scalar(envelope[1], &payload64) != nil {
		return result, bad
	}
	body, err := decode64(payload64, maxPayload)
	if err != nil {
		return result, bad
	}
	var sigs []json.RawMessage
	if json.Unmarshal(envelope[2], &sigs) != nil || len(sigs) == 0 || len(sigs) > 2 {
		return result, bad
	}
	canonical, err := json.Marshal(sigs)
	if err != nil || !bytes.Equal(canonical, envelope[2]) {
		return result, bad
	}
	trusted, err := trustedKeyMap(keys)
	if err != nil {
		return result, bad
	}
	seen := make(map[string]bool)
	verified := false
	for _, sigRaw := range sigs {
		pair, err := canonicalArray(sigRaw, 2)
		if err != nil {
			return result, bad
		}
		var id, signature64 string
		if scalar(pair[0], &id) != nil || scalar(pair[1], &signature64) != nil || len(id) != 64 || strings.ToLower(id) != id || seen[id] {
			return result, bad
		}
		if _, err := hex.DecodeString(id); err != nil {
			return result, bad
		}
		seen[id] = true
		sig, err := decode64(signature64, ed25519.SignatureSize)
		if err != nil || len(sig) != ed25519.SignatureSize {
			return result, bad
		}
		if key, ok := trusted[id]; ok && ed25519.Verify(key, append([]byte(signatureDomain), body...), sig) {
			verified = true
		}
	}
	if !verified {
		return result, bad
	}
	payload, err := canonicalArray(body, 11)
	if err != nil {
		return result, bad
	}
	var channel string
	if scalar(payload[0], &format) != nil || format != 1 || scalar(payload[1], &channel) != nil || channel != "stable" ||
		scalar(payload[2], &result.sequence) != nil || scalar(payload[3], &result.version) != nil ||
		scalar(payload[4], &result.issued) != nil || scalar(payload[5], &result.expires) != nil ||
		scalar(payload[6], &result.platform) != nil || scalar(payload[7], &result.arch) != nil ||
		scalar(payload[8], &result.filename) != nil || scalar(payload[9], &result.size) != nil ||
		scalar(payload[10], &result.digest) != nil {
		return metadata{}, bad
	}
	if result.sequence < 1 || result.sequence > maxSafeInteger || !validMetadataTime(result.issued, result.expires, now) ||
		result.platform != platform || result.arch != arch || result.size < 1 || result.size > maxArtifact ||
		!stableVersion.MatchString(result.version) || len(result.digest) != 64 || strings.ToLower(result.digest) != result.digest {
		return metadata{}, bad
	}
	if _, err := hex.DecodeString(result.digest); err != nil {
		return metadata{}, bad
	}
	if arch != "x64" && arch != "arm64" {
		return metadata{}, bad
	}
	var filename string
	switch platform {
	case "darwin":
		if arch == "x64" {
			filename = fmt.Sprintf("Lykeion-%s-mac.zip", result.version)
		} else {
			filename = fmt.Sprintf("Lykeion-%s-%s-mac.zip", result.version, arch)
		}
	case "linux":
		if arch == "x64" {
			filename = fmt.Sprintf("Lykeion-%s.AppImage", result.version)
		} else {
			filename = fmt.Sprintf("Lykeion-%s-%s.AppImage", result.version, arch)
		}
	default:
		return metadata{}, bad
	}
	if result.filename != filename {
		return metadata{}, bad
	}
	result.payloadDigest = sha256.Sum256(body)
	return result, nil
}

func newerVersion(candidate, current string) bool {
	if !stableVersion.MatchString(candidate) || !stableVersion.MatchString(current) {
		return false
	}
	a, b := strings.Split(candidate, "."), strings.Split(current, ".")
	for i := range a {
		x, _ := strconv.ParseUint(a[i], 10, 32)
		y, _ := strconv.ParseUint(b[i], 10, 32)
		if x != y {
			return x > y
		}
	}
	return false
}
