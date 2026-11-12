package desktopupdate

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"strings"
	"time"
)

var releaseFilename = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// Only repository release paths or GitHub's release asset CDN may be fetched.
func allowedURL(u *url.URL, filename string) bool {
	if u == nil || !releaseFilename.MatchString(filename) || u.Scheme != "https" || u.User != nil || u.Fragment != "" || (u.Port() != "" && u.Port() != "443") || strings.Contains(u.EscapedPath(), "%") || path.Clean(u.Path) != u.Path {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "release-assets.githubusercontent.com" {
		return strings.HasPrefix(u.Path, "/github-production-release-asset/")
	}
	if host == "objects.githubusercontent.com" {
		return strings.HasPrefix(u.Path, "/github-production-release-asset-2e65be/")
	}
	if host != "github.com" || u.RawQuery != "" {
		return false
	}
	const base = "/lykeion-ia/lykeion/releases/"
	if u.Path == base+"latest/download/"+filename {
		return true
	}
	const prefix = base + "download/v"
	if !strings.HasPrefix(u.Path, prefix) || !strings.HasSuffix(u.Path, "/"+filename) {
		return false
	}
	version := strings.TrimSuffix(strings.TrimPrefix(u.Path, prefix), "/"+filename)
	return stableVersion.MatchString(version)
}

func updateClient(transport http.RoundTripper, filename string) *http.Client {
	return &http.Client{
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 || !allowedURL(req.URL, filename) {
				return errors.New("unsupported update redirect")
			}
			for _, previous := range via {
				if previous.URL.RawQuery != "" && !strings.EqualFold(previous.URL.Hostname(), req.URL.Hostname()) {
					return errors.New("unsupported update redirect")
				}
			}
			req.Header.Del("Authorization")
			req.Header.Del("Cookie")
			req.Header.Del("Proxy-Authorization")
			req.Header.Del("Referer")
			return nil
		},
	}
}

// The helper must run with a minimal environment and a private cache lock held.
func secureTransport() (*http.Transport, error) {
	for _, name := range []string{"SSL_CERT_FILE", "SSL_CERT_DIR", "GODEBUG"} {
		if os.Getenv(name) != "" {
			return nil, errors.New("update network environment unavailable")
		}
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	return &http.Transport{
		Proxy:                 nil,
		DialContext:           dialer.DialContext,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		DisableCompression:    true,
	}, nil
}

func fetchMetadata(ctx context.Context, client *http.Client, platform, arch string) ([]byte, error) {
	errUnavailable := errors.New("update metadata unavailable")
	if (platform != "darwin" && platform != "linux") || (arch != "arm64" && arch != "x64") || client == nil {
		return nil, errUnavailable
	}
	filename := "desktop-stable-" + platform + "-" + arch + ".json"
	address := "https://github.com/lykeion-ia/lykeion/releases/latest/download/" + filename
	u, err := url.Parse(address)
	if err != nil || !allowedURL(u, filename) {
		return nil, errUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, errUnavailable
	}
	req.Header.Set("Accept-Encoding", "identity")
	response, err := client.Do(req)
	if err != nil {
		var networkError *net.OpError
		var dnsError *net.DNSError
		if ctx.Err() == nil && (errors.As(err, &networkError) || errors.As(err, &dnsError)) {
			return nil, checkError{Network}
		}
		return nil, errUnavailable
	}
	defer response.Body.Close()
	encoding := response.Header.Get("Content-Encoding")
	if response.StatusCode != http.StatusOK || (encoding != "" && encoding != "identity") || response.ContentLength > maxEnvelope {
		return nil, errUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxEnvelope+1))
	if err != nil || len(data) == 0 || len(data) > maxEnvelope {
		return nil, errUnavailable
	}
	return data, nil
}

func openArtifact(ctx context.Context, client *http.Client, m metadata) (io.ReadCloser, error) {
	errUnavailable := errors.New("update download unavailable")
	if client == nil || !stableVersion.MatchString(m.version) || !releaseFilename.MatchString(m.filename) || m.size < 1 || m.size > maxArtifact {
		return nil, errUnavailable
	}
	address := "https://github.com/lykeion-ia/lykeion/releases/download/v" + m.version + "/" + m.filename
	u, err := url.Parse(address)
	if err != nil || !allowedURL(u, m.filename) {
		return nil, errUnavailable
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, errUnavailable
	}
	req.Header.Set("Accept-Encoding", "identity")
	response, err := client.Do(req)
	if err != nil {
		return nil, errUnavailable
	}
	encoding := response.Header.Get("Content-Encoding")
	if response.StatusCode != http.StatusOK || (encoding != "" && encoding != "identity") || (response.ContentLength >= 0 && response.ContentLength != m.size) {
		response.Body.Close()
		return nil, errUnavailable
	}
	return response.Body, nil
}
