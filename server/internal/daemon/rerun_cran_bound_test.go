package daemon

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type cranCountBody struct {
	reader io.Reader
	reads  int
	bytes  int
	closed bool
}

func (b *cranCountBody) Read(p []byte) (int, error) {
	b.reads++
	n, err := b.reader.Read(p)
	b.bytes += n
	return n, err
}

func (b *cranCountBody) Close() error { b.closed = true; return nil }

type cranRoundTrip func(*http.Request) (*http.Response, error)

func (f cranRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestFetchCRANRejectsDeclaredOversizeBeforeCreatingFile(t *testing.T) {
	oldClient, oldLimit := cranClient, maxCRANBytes
	t.Cleanup(func() { cranClient, maxCRANBytes = oldClient, oldLimit })
	maxCRANBytes = 4
	for _, tc := range []struct {
		name          string
		contentLength int64
		body          string
		wantError     bool
		wantNoRead    bool
		preexisting   bool
	}{
		{"declared too large", 5, "abcde", true, true, false},
		{"declared too large with existing file", 5, "abcde", true, true, true},
		{"exact limit", 4, "abcd", false, false, false},
		{"unknown length", -1, "abcdefghij", true, false, false},
		{"misleading length", 1, "abcdefghij", true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := &cranCountBody{reader: strings.NewReader(tc.body)}
			cranClient = &http.Client{Transport: cranRoundTrip(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", ContentLength: tc.contentLength, Body: body, Header: make(http.Header)}, nil
			})}
			dir := t.TempDir()
			if tc.preexisting {
				if err := os.WriteFile(filepath.Join(dir, "example_1.0.tar.gz"), []byte("keep"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			path, err := fetchCRAN(context.Background(), dir, "example", "1.0")
			var permanent unrebuildable
			if tc.wantError {
				if !errors.As(err, &permanent) {
					t.Fatalf("expected unrebuildable size error, got %v", err)
				}
			} else if err != nil || path != filepath.Join(dir, "example_1.0.tar.gz") {
				t.Fatalf("fetch: %q %v", path, err)
			}
			if !body.closed {
				t.Fatal("response body was not closed")
			}
			if body.bytes > int(maxCRANBytes+1) {
				t.Fatalf("read %d bytes beyond stream cap", body.bytes)
			}
			if !tc.wantError {
				data, err := os.ReadFile(path)
				if err != nil || string(data) != tc.body {
					t.Fatalf("valid archive changed: %q %v", data, err)
				}
			}
			if tc.wantNoRead {
				if body.reads != 0 {
					t.Fatalf("oversize response body read %d times", body.reads)
				}
				if tc.preexisting {
					data, err := os.ReadFile(filepath.Join(dir, "example_1.0.tar.gz"))
					if err != nil || string(data) != "keep" {
						t.Fatalf("existing file changed: %q %v", data, err)
					}
				} else {
					files, err := os.ReadDir(dir)
					if err != nil || len(files) != 0 {
						t.Fatalf("created output for oversized response: %v %v", files, err)
					}
				}
			}
		})
	}
}
