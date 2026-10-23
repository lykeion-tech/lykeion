package sources

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestUpstreamCallsAreBounded pins the default client's bounds, then shows
// each bound cuts a stalled upstream.
func TestUpstreamCallsAreBounded(t *testing.T) {
	if client.Transport == nil {
		t.Fatal("default client has no bounded transport")
	}
	if client.Timeout != 30*time.Second || client.Transport.(*http.Transport).TLSHandshakeTimeout != 10*time.Second {
		t.Fatalf("default client: call %v, connect %v", client.Timeout, client.Transport.(*http.Transport).TLSHandshakeTimeout)
	}
	ctx := context.Background()

	// An upstream that accepts the request and never answers.
	hang := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer hang.Close()
	start := time.Now()
	if _, err := get(ctx, newClient(200*time.Millisecond, 5*time.Second), hang.URL); err == nil || time.Since(start) > 2*time.Second {
		t.Fatalf("a silent upstream: %v after %v", err, time.Since(start))
	}

	// An upstream that accepts the connection and never completes TLS.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	held := make(chan net.Conn, 8)
	done := make(chan struct{})
	defer func() {
		ln.Close()
		<-done
		close(held)
		for c := range held {
			c.Close()
		}
	}()
	go func() {
		defer close(done)
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			held <- c
		}
	}()
	start = time.Now()
	if _, err := get(ctx, newClient(5*time.Second, 200*time.Millisecond), "https://"+ln.Addr().String()); err == nil || time.Since(start) > 2*time.Second {
		t.Fatalf("a stalled handshake: %v after %v", err, time.Since(start))
	}
}

func TestOpenAlexNamesItsContact(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.URL.Query().Get("mailto"))
		if strings.HasSuffix(r.URL.Path, "/works") {
			w.Write([]byte(`{"results":[]}`))
			return
		}
		w.Write([]byte(`{"id":"https://openalex.org/W1","title":"t"}`))
	}))
	defer srv.Close()
	oa := OpenAlex{Base: srv.URL, Mailto: "lab@example.org"}
	if _, err := oa.Search(context.Background(), "q"); err != nil {
		t.Fatal(err)
	}
	if _, err := oa.Get(context.Background(), "W1"); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "lab@example.org" || got[1] != "lab@example.org" {
		t.Fatalf("mailto sent: %q", got)
	}
	got = nil
	if _, err := (OpenAlex{Base: srv.URL}).Search(context.Background(), "q"); err != nil || got[0] != "" {
		t.Fatalf("no contact, yet mailto %q (%v)", got, err)
	}
}
