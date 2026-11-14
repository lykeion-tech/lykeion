package desktopupdate

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	"filippo.io/edwards25519"
)

func signedEnvelope(t *testing.T, payload []any) ([]byte, ed25519.PublicKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	keyID := sha256.Sum256(der)
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	sig := ed25519.Sign(priv, append([]byte(signatureDomain), body...))
	envelope, err := json.Marshal([]any{1, base64.StdEncoding.EncodeToString(body), []any{[]string{hex.EncodeToString(keyID[:]), base64.StdEncoding.EncodeToString(sig)}}})
	if err != nil {
		t.Fatal(err)
	}
	return envelope, pub
}

func TestSignedMetadataRejectsTamperingAndWrongTarget(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	payload := []any{1, "stable", 7, "1.2.3", now.Unix(), now.Add(time.Hour).Unix(), "darwin", "arm64", "Lykeion-1.2.3-arm64-mac.zip", 123, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	envelope, pub := signedEnvelope(t, payload)
	metadata, err := verifyMetadata(envelope, []ed25519.PublicKey{pub}, "darwin", "arm64", now)
	if err != nil || metadata.version != "1.2.3" || metadata.sequence != 7 {
		t.Fatalf("metadata=%+v error=%v", metadata, err)
	}
	if _, err = verifyMetadata(envelope, []ed25519.PublicKey{pub}, "linux", "arm64", now); err == nil {
		t.Fatal("accepted wrong platform")
	}
	if _, err = verifyMetadata(envelope, []ed25519.PublicKey{pub}, "darwin", "arm64", now.Add(2*time.Hour)); err == nil {
		t.Fatal("accepted expired metadata")
	}
	other, _, _ := ed25519.GenerateKey(rand.Reader)
	if _, err = verifyMetadata(envelope, []ed25519.PublicKey{other}, "darwin", "arm64", now); err == nil {
		t.Fatal("accepted wrong key")
	}
	envelope[len(envelope)/2] ^= 1
	if _, err = verifyMetadata(envelope, []ed25519.PublicKey{pub}, "darwin", "arm64", now); err == nil {
		t.Fatal("accepted altered metadata")
	}
}

func TestSignedMetadataRejectsUnsafeFields(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	base := []any{1, "stable", 7, "1.2.3", now.Unix(), now.Add(time.Hour).Unix(), "darwin", "arm64", "Lykeion-1.2.3-arm64-mac.zip", 123, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	for _, tc := range []struct {
		name  string
		index int
		value any
	}{
		{"channel", 1, "beta"},
		{"unsafe sequence", 2, int64(1 << 53)},
		{"invalid version", 3, "01.2.3"},
		{"too far in future", 4, now.Add(2 * time.Hour).Unix()},
		{"too long validity", 5, now.Add(8 * 24 * time.Hour).Unix()},
		{"path traversal", 8, "../file.zip"},
		{"oversized artifact", 9, int64(maxArtifact) + 1},
		{"uppercase digest", 10, "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := append([]any(nil), base...)
			payload[tc.index] = tc.value
			envelope, key := signedEnvelope(t, payload)
			if _, err := verifyMetadata(envelope, []ed25519.PublicKey{key}, "darwin", "arm64", now); err == nil {
				t.Fatal("accepted unsafe metadata")
			}
		})
	}
}

func TestVersionOrdering(t *testing.T) {
	for _, tc := range []struct {
		candidate, current string
		newer              bool
	}{
		{"1.2.4", "1.2.3", true},
		{"1.10.0", "1.9.9", true},
		{"1.2.3", "1.2.3", false},
		{"1.2.2", "1.2.3", false},
		{"1.2.4-rc.1", "1.2.3", false},
	} {
		if got := newerVersion(tc.candidate, tc.current); got != tc.newer {
			t.Fatalf("%q newer than %q = %v", tc.candidate, tc.current, got)
		}
	}
}

func TestBuilderDefaultArtifactNames(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	for _, tc := range []struct{ platform, arch, name string }{
		{"darwin", "x64", "Lykeion-1.2.3-mac.zip"},
		{"darwin", "arm64", "Lykeion-1.2.3-arm64-mac.zip"},
		{"linux", "x64", "Lykeion-1.2.3.AppImage"},
		{"linux", "arm64", "Lykeion-1.2.3-arm64.AppImage"},
	} {
		payload := []any{1, "stable", 7, "1.2.3", now.Unix(), now.Add(time.Hour).Unix(), tc.platform, tc.arch, tc.name, 123, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
		envelope, key := signedEnvelope(t, payload)
		if _, err := verifyMetadata(envelope, []ed25519.PublicKey{key}, tc.platform, tc.arch, now); err != nil {
			t.Fatalf("%s/%s: %v", tc.platform, tc.arch, err)
		}
	}
}

func TestKeyRotationAcceptsOneTrustedSignatureAndRejectsDuplicates(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	payload, err := json.Marshal([]any{1, "stable", 7, "1.2.3", now.Unix(), now.Add(time.Hour).Unix(), "darwin", "arm64", "Lykeion-1.2.3-arm64-mac.zip", 123, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"})
	if err != nil {
		t.Fatal(err)
	}
	oldPub, oldPriv, _ := ed25519.GenerateKey(rand.Reader)
	newPub, newPriv, _ := ed25519.GenerateKey(rand.Reader)
	pair := func(pub ed25519.PublicKey, priv ed25519.PrivateKey) []string {
		der, err := x509.MarshalPKIXPublicKey(pub)
		if err != nil {
			t.Fatal(err)
		}
		id := sha256.Sum256(der)
		sig := ed25519.Sign(priv, append([]byte(signatureDomain), payload...))
		return []string{hex.EncodeToString(id[:]), base64.StdEncoding.EncodeToString(sig)}
	}
	oldPair, newPair := pair(oldPub, oldPriv), pair(newPub, newPriv)
	encode := func(pairs ...[]string) []byte {
		b, err := json.Marshal([]any{1, base64.StdEncoding.EncodeToString(payload), pairs})
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	rotated := encode(oldPair, newPair)
	if _, err := verifyMetadata(rotated, []ed25519.PublicKey{oldPub}, "darwin", "arm64", now); err != nil {
		t.Fatalf("old key did not accept rotated envelope: %v", err)
	}
	if _, err := verifyMetadata(rotated, []ed25519.PublicKey{newPub}, "darwin", "arm64", now); err != nil {
		t.Fatalf("new key did not accept rotated envelope: %v", err)
	}
	if _, err := verifyMetadata(encode(oldPair, oldPair), []ed25519.PublicKey{oldPub}, "darwin", "arm64", now); err == nil {
		t.Fatal("accepted duplicate signer IDs")
	}
}

func TestRejectsIdentityAndLowOrderTrustAnchors(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	payload, err := json.Marshal([]any{1, "stable", 7, "1.2.3", now.Unix(), now.Add(time.Hour).Unix(), "darwin", "arm64", "Lykeion-1.2.3-arm64-mac.zip", 123, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"})
	if err != nil {
		t.Fatal(err)
	}
	identity := make(ed25519.PublicKey, ed25519.PublicKeySize)
	identity[0] = 1
	der, err := x509.MarshalPKIXPublicKey(identity)
	if err != nil {
		t.Fatal(err)
	}
	id := sha256.Sum256(der)
	forged := make([]byte, ed25519.SignatureSize)
	forged[0] = 1
	if !ed25519.Verify(identity, append([]byte(signatureDomain), payload...), forged) {
		t.Fatal("identity forgery control no longer reproduces on this Go version")
	}
	envelope, err := json.Marshal([]any{1, base64.StdEncoding.EncodeToString(payload), [][]string{{hex.EncodeToString(id[:]), base64.StdEncoding.EncodeToString(forged)}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifyMetadata(envelope, []ed25519.PublicKey{identity}, "darwin", "arm64", now); err == nil {
		t.Fatal("accepted identity anchor and forgery")
	}
	zero := make(ed25519.PublicKey, ed25519.PublicKeySize)
	if validPublicKey(zero) {
		t.Fatal("accepted low-order anchor")
	}
	good, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if !validPublicKey(good) {
		t.Fatal("rejected a generated prime-order anchor")
	}
	goodPoint, err := new(edwards25519.Point).SetBytes(good)
	if err != nil {
		t.Fatal(err)
	}
	lowPoint, err := new(edwards25519.Point).SetBytes(zero)
	if err != nil {
		t.Fatal(err)
	}
	mixed := new(edwards25519.Point).Add(goodPoint, lowPoint).Bytes()
	if validPublicKey(mixed) {
		t.Fatal("accepted anchor with mixed torsion")
	}
}
