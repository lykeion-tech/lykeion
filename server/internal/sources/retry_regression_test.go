package sources

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestOpenAlexRetriesRateLimit(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if r.URL.Query().Get("mailto") != "contact@lykeion.tech" {
			t.Error("server contact missing")
		}
		if attempts == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	defer srv.Close()
	if _, err := (OpenAlex{Base: srv.URL, Mailto: "contact@lykeion.tech"}).Search(context.Background(), "q"); err != nil {
		t.Fatal(err)
	}
	if attempts != 2 {
		t.Fatalf("got %d attempts, want 2", attempts)
	}
}

func TestOpenAlexReportsRetryAfterWhenRateLimitPersists(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	_, err := (OpenAlex{Base: srv.URL}).Search(context.Background(), "q")
	if err == nil || !strings.Contains(err.Error(), "Retry-After") || attempts != 3 {
		t.Fatalf("err=%v attempts=%d", err, attempts)
	}
}

func TestRateLimitWaitCancellationKeepsRetryAfter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "10")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := (OpenAlex{Base: srv.URL}).Search(ctx, "q")
	if err == nil || !strings.Contains(err.Error(), "Retry-After: 10") {
		t.Fatalf("error = %v", err)
	}
}

func TestNon5xxStatusIsNotRetried(t *testing.T) {
	count := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { count++; w.WriteHeader(600) }))
	defer srv.Close()
	_, _ = (OpenAlex{Base: srv.URL}).Search(context.Background(), "q")
	if count != 1 {
		t.Fatalf("attempts=%d for HTTP 600", count)
	}
}

func TestRateLimitBeyondDeadlineReturnsPromptly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "10")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := time.Now()
	_, err := (OpenAlex{Base: srv.URL}).Search(ctx, "q")
	if err == nil || !strings.Contains(err.Error(), "Retry-After: 10") || time.Since(start) > time.Second {
		t.Fatalf("error=%v elapsed=%v", err, time.Since(start))
	}
}
