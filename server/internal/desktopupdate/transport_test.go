package desktopupdate

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestMetadataNetworkErrorsHaveSafeCategory(t *testing.T) {
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("secret path and token")}
	})
	_, err := fetchMetadata(context.Background(), updateClient(transport, "desktop-stable-darwin-arm64.json"), "darwin", "arm64")
	if ErrorCategory(err) != Network || strings.Contains(err.Error(), "secret") {
		t.Fatalf("network failure was not safely categorized: %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestMetadataFetchBoundsAndForwardsNoCredentials(t *testing.T) {
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("Accept-Encoding") != "identity" {
			t.Fatalf("unexpected request headers: %v", r.Header)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("signed")), ContentLength: 6, Request: r}, nil
	})
	client := updateClient(transport, "desktop-stable-darwin-arm64.json")
	got, err := fetchMetadata(context.Background(), client, "darwin", "arm64")
	if err != nil || string(got) != "signed" {
		t.Fatalf("metadata %q error %v", got, err)
	}
	transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(strings.Repeat("x", maxEnvelope+1))), ContentLength: -1, Request: r}, nil
	})
	client = updateClient(transport, "desktop-stable-darwin-arm64.json")
	if _, err := fetchMetadata(context.Background(), client, "darwin", "arm64"); err == nil {
		t.Fatal("accepted oversized metadata stream")
	}
}

func TestArtifactHeaderWaitHonorsCancellation(t *testing.T) {
	started := make(chan struct{})
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		close(started)
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	m := metadata{version: "1.2.3", filename: "Lykeion-1.2.3-arm64-mac.zip", size: 12}
	client := updateClient(transport, m.filename)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := openArtifact(ctx, client, m); done <- err }()
	<-started
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled headers succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("header cancellation blocked")
	}
}

func TestMetadataRedirectDoesNotForwardReferrer(t *testing.T) {
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Referer") != "" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Fatalf("credentials or referrer forwarded")
		}
		if r.URL.Hostname() == "github.com" {
			return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{"https://release-assets.githubusercontent.com/github-production-release-asset/123?signature=secret"}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("signed")), ContentLength: 6, Request: r}, nil
	})
	client := updateClient(transport, "desktop-stable-darwin-arm64.json")
	if _, err := fetchMetadata(context.Background(), client, "darwin", "arm64"); err != nil {
		t.Fatal(err)
	}
}

func TestSignedAssetRedirectCannotCrossOrigin(t *testing.T) {
	for _, crossOrigin := range []bool{false, true} {
		t.Run(map[bool]string{false: "same-origin", true: "cross-origin"}[crossOrigin], func(t *testing.T) {
			requests := 0
			transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
				requests++
				switch requests {
				case 1:
					return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{"https://release-assets.githubusercontent.com/github-production-release-asset/123?signature=secret"}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
				case 2:
					target := "https://release-assets.githubusercontent.com/github-production-release-asset/456?signature=secret"
					if crossOrigin {
						target = "https://objects.githubusercontent.com/github-production-release-asset-2e65be/456?signature=secret"
					}
					return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{target}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
				default:
					return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("signed")), ContentLength: 6, Request: r}, nil
				}
			})
			_, err := fetchMetadata(context.Background(), updateClient(transport, "desktop-stable-darwin-arm64.json"), "darwin", "arm64")
			if crossOrigin && (err == nil || requests != 2) {
				t.Fatalf("cross-origin redirect reached transport: requests=%d err=%v", requests, err)
			}
			if !crossOrigin && (err != nil || requests != 3) {
				t.Fatalf("same-origin redirect failed: requests=%d err=%v", requests, err)
			}
		})
	}
}

func TestUpdateURLsOnlyAllowFixedReleasePathsAndApprovedCDN(t *testing.T) {
	name := "desktop-stable-darwin-arm64.json"
	for _, raw := range []string{
		"https://github.com/lykeion-tech/lykeion/releases/latest/download/" + name,
		"https://github.com/lykeion-tech/lykeion/releases/download/v1.2.3/" + name,
		"https://release-assets.githubusercontent.com/github-production-release-asset/123?signature=abc",
	} {
		u, err := url.Parse(raw)
		if err != nil || !allowedURL(u, name) {
			t.Fatalf("rejected allowed update URL: %q %v", raw, err)
		}
	}
	for _, raw := range []string{
		"http://github.com/lykeion-tech/lykeion/releases/latest/download/" + name,
		"https://github.com/other/project/releases/latest/download/" + name,
		"https://github.com/lykeion-tech/lykeion/releases/latest/download/other.json",
		"https://github.com/lykeion-tech/lykeion/releases/latest/download/" + name + "?secret=abc",
		"https://release-assets.githubusercontent.com.evil.test/github-production-release-asset/123",
		"https://release-assets.githubusercontent.com/private/other",
		"https://release-assets.githubusercontent.com/github-production-release-asset-evil/123",
		"https://release-assets.githubusercontent.com/github-production-release-asset/../private",
		"https://release-assets.githubusercontent.com/github-production-release-asset/%2e%2e/private",
		"https://user@github.com/lykeion-tech/lykeion/releases/latest/download/" + name,
		"https://github.com:444/lykeion-tech/lykeion/releases/latest/download/" + name,
	} {
		u, err := url.Parse(raw)
		if err == nil && allowedURL(u, name) {
			t.Fatalf("accepted unsafe update URL: %q", raw)
		}
	}
}
