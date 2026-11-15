package sources

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCatalogueRateLimitRetryRecordsOneCall(t *testing.T) {
	var count atomic.Int32
	f := newConnFix(t, func(w http.ResponseWriter, r *http.Request) {
		if count.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(`{"id":"ENSG00000141510"}`))
	})
	_, err := f.fetch(f.card, tp53)
	if err != nil {
		t.Fatal(err)
	}
	if got := count.Load(); got != 2 {
		t.Fatalf("attempts=%d", got)
	}
	if got := f.count(`SELECT count(*) FROM record WHERE kind='call'`); got != 1 {
		t.Fatalf("calls=%d", got)
	}
}

func TestFailedCatalogueCallIsRecorded(t *testing.T) {
	f := newConnFix(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	_, err := f.fetch(f.card, tp53)
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := f.count(`SELECT count(*) FROM record WHERE kind='call' AND body->>'error' <> ''`); got != 1 {
		t.Fatalf("failed call records=%d", got)
	}
}

func TestCancelledCatalogueRetryRecordsFailure(t *testing.T) {
	f := newConnFix(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "10")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	role := f.role(f.card, "agent")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := f.h.Fetch(ctx, role, runTok(role), tp53)
	if err == nil || !strings.Contains(err.Error(), "Retry-After") {
		t.Fatalf("error = %v", err)
	}
	if n := f.count(`SELECT count(*) FROM record WHERE kind='call' AND body->>'error' <> ''`); n != 1 {
		t.Fatalf("failure records=%d", n)
	}
}

func TestRejectedSourceCallDoesNotRecordSecretInPath(t *testing.T) {
	f := newConnFix(t, ok(`{}`))
	const secret = "secret-forbidden-path"
	_, err := f.fetch(f.card, ConnectorIn{Connector: "ensembl", Path: "/bad?api_key=" + secret + "\n"})
	if err == nil {
		t.Fatal("expected rejection")
	}
	if n := f.count(`SELECT count(*) FROM record WHERE kind='call' AND body::text LIKE '%` + secret + `%'`); n != 0 {
		t.Fatalf("secret appeared in %d records", n)
	}
	if n := f.count(`SELECT count(*) FROM record WHERE kind='call' AND body->>'error' <> ''`); n != 1 {
		t.Fatalf("failure records=%d", n)
	}
}

func TestUnconfiguredCatalogueCallIsRecorded(t *testing.T) {
	f := newConnFix(t, ok(`{}`))
	_, err := f.fetch(f.card, ConnectorIn{Connector: "materials-project", Path: "/materials/summary/mp-149"})
	if err == nil {
		t.Fatal("expected missing key error")
	}
	if n := f.count(`SELECT count(*) FROM record WHERE kind='call' AND body->>'error' <> ''`); n != 1 {
		t.Fatalf("failure records=%d", n)
	}
}
