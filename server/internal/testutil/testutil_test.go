package testutil

import "testing"

func TestMalformedDatabaseURLDoesNotPanic(t *testing.T) {
	for _, url := range []string{"%", "://broken", "postgres://%zz"} {
		if got := WithDB(url, "test"); got != url {
			t.Fatalf("malformed URL changed: %q", got)
		}
		if got := withUser(url, "app", "app"); got != url {
			t.Fatalf("malformed URL changed: %q", got)
		}
	}
}
