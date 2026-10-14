package auth

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"lykeion/server/internal/testutil"
)

func TestTokenShape(t *testing.T) {
	plain, hash, shown := NewToken(PersonalPrefix)
	if !strings.HasPrefix(plain, "lyk_") || len(plain) < 40 || Hash(plain) != hash || !strings.HasPrefix(plain, shown) || len(shown) != 10 {
		t.Fatalf("%q %q %q", plain, hash, shown)
	}
}

func TestJWTRoundTripAndTamper(t *testing.T) {
	iss := Issuer{Secret: []byte("s"), TTL: time.Hour}
	u := uuid.New()
	tok, _ := iss.Issue(u, "a@x")
	got, email, err := iss.Parse(tok)
	if err != nil || got != u || email != "a@x" {
		t.Fatalf("%v %v %v", got, email, err)
	}
	if _, _, err := (Issuer{Secret: []byte("other")}).Parse(tok); err == nil {
		t.Fatal("token verified with the wrong secret")
	}
	old, _ := Issuer{Secret: []byte("s"), TTL: -time.Minute}.Issue(u, "a@x")
	if _, _, err := iss.Parse(old); err == nil {
		t.Fatal("expired token accepted")
	}
}

func TestCodeIsSixDigits(t *testing.T) {
	c := NewCode()
	if len(c) != 6 || strings.Trim(c, "0123456789") != "" {
		t.Fatal(c)
	}
	if CodeHash("a@x", c) == CodeHash("b@x", c) {
		t.Fatal("code hash ignores the email")
	}
}

func TestLimiter(t *testing.T) {
	env := testutil.Setup(t)
	l := Limiter{R: env.Redis}
	for i := 0; i < 3; i++ {
		if ok, err := l.Allow(context.Background(), "k", 3, time.Minute); !ok || err != nil {
			t.Fatalf("call %d refused: %v", i, err)
		}
	}
	if ok, _ := l.Allow(context.Background(), "k", 3, time.Minute); ok {
		t.Fatal("fourth call allowed")
	}
}

func TestLimiterConcurrentWindowHasExpiry(t *testing.T) {
	env := testutil.Setup(t)
	l := Limiter{R: env.Redis}
	var allowed atomic.Int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			ok, err := l.Allow(t.Context(), "concurrent", 5, time.Minute)
			if err != nil {
				t.Error(err)
			}
			if ok {
				allowed.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	if got := allowed.Load(); got != 5 {
		t.Fatalf("allowed=%d", got)
	}
	ttl, err := env.Redis.PTTL(t.Context(), "lyk:rl:concurrent").Result()
	if err != nil || ttl <= 0 || ttl > time.Minute {
		t.Fatalf("ttl=%s err=%v", ttl, err)
	}
	if ok, err := (Limiter{}).Allow(t.Context(), "missing", 5, time.Minute); ok || err == nil {
		t.Fatal("unavailable limiter did not fail closed")
	}
}
