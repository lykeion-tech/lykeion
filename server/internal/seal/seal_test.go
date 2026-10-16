package seal

import (
	"bytes"
	"encoding/base64"
	"errors"
	"testing"
)

func TestSealOpensOnlyWithItsKeyAndRow(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	plain := []byte("whsec-very-secret")
	a, err := Seal(key, plain, []byte("row-1"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := Seal(key, plain, []byte("row-1"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(a, b) || bytes.Contains(a, plain) {
		t.Fatal("sealing is deterministic or leaks the secret")
	}
	if got, err := Open(key, a, []byte("row-1")); err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("open: %q %v", got, err)
	}
	if _, err := Open(key, a, []byte("row-2")); err == nil {
		t.Fatal("a sealed value opened on another row")
	}
	if _, err := Open(bytes.Repeat([]byte{8}, 32), a, []byte("row-1")); err == nil {
		t.Fatal("a sealed value opened under another key")
	}
	if _, err := Open(key, a[:5], []byte("row-1")); err == nil {
		t.Fatal("a truncated value opened")
	}
	if _, err := Seal(nil, plain, nil); !errors.Is(err, ErrNoKey) {
		t.Fatalf("seal without a key: %v", err)
	}
	if _, err := Open(nil, a, nil); !errors.Is(err, ErrNoKey) {
		t.Fatalf("open without a key: %v", err)
	}
	for _, size := range []int{16, 24} {
		shortKey := bytes.Repeat([]byte{9}, size)
		if _, err := Seal(shortKey, plain, []byte("row-1")); err == nil {
			t.Errorf("Seal accepted a %d-byte key", size)
		}
		if _, err := Open(shortKey, a, []byte("row-1")); err == nil {
			t.Errorf("Open accepted a %d-byte key", size)
		}
	}
}

func TestParseKey(t *testing.T) {
	if k, err := ParseKey(""); k != nil || err != nil {
		t.Fatalf("empty: %v %v", k, err)
	}
	good := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))
	if k, err := ParseKey(good); err != nil || len(k) != 32 {
		t.Fatalf("good: %v", err)
	}
	for _, bad := range []string{"not base64!", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 31))} {
		if _, err := ParseKey(bad); err == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
}
