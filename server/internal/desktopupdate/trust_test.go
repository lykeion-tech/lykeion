package desktopupdate

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"testing"
)

func TestBuildTrustRequiresValidatedReleaseKeysAndVersion(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	encoded := base64.StdEncoding.EncodeToString(der)
	keys, err := parseBuildTrust(encoded, "1.2.3")
	if err != nil || len(keys) != 1 {
		t.Fatalf("valid release trust rejected: %v", err)
	}
	for _, input := range []struct{ keys, version string }{
		{"", "1.2.3"}, {encoded, ""}, {encoded, "01.2.3"}, {encoded + "," + encoded, "1.2.3"}, {"not-base64", "1.2.3"},
	} {
		if _, err := parseBuildTrust(input.keys, input.version); err == nil {
			t.Fatalf("accepted invalid build trust %q / %q", input.keys, input.version)
		}
	}
	identity := make(ed25519.PublicKey, ed25519.PublicKeySize)
	identity[0] = 1
	der, err = x509.MarshalPKIXPublicKey(identity)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseBuildTrust(base64.StdEncoding.EncodeToString(der), "1.2.3"); err == nil {
		t.Fatal("accepted identity trust anchor")
	}
}

func TestEmbeddedBuildTrustReportsOnlyValidatedPublicIdentity(t *testing.T) {
	oldKeys, oldVersion := embeddedKeys, embeddedVersion
	t.Cleanup(func() { embeddedKeys, embeddedVersion = oldKeys, oldVersion })
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	embeddedKeys, embeddedVersion = base64.StdEncoding.EncodeToString(der), "1.2.3"
	info, err := EmbeddedBuildTrust()
	if err != nil || info.Version != "1.2.3" || len(info.KeyIDs) != 1 {
		t.Fatalf("info=%+v error=%v", info, err)
	}
	embeddedKeys = "not-a-valid-key"
	if _, err := EmbeddedBuildTrust(); err == nil {
		t.Fatal("accepted invalid embedded trust")
	}
}
