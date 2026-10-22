package apiclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Client struct {
	Server, Token, Workspace string
	HTTP                     *http.Client
}

// Zip is a request body sent as a raw zip archive instead of JSON.
type Zip []byte

func (c *Client) Do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	contentType := "application/json"
	if z, ok := in.(Zip); ok {
		body, contentType = bytes.NewReader(z), "application/zip"
	} else if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	resp, err := c.send(ctx, method, path, body, contentType)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if out != nil && resp.StatusCode != http.StatusNoContent {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// Download streams a GET response's body into w, never holding it whole.
func (c *Client) Download(ctx context.Context, path string, w io.Writer) error {
	resp, err := c.send(ctx, "GET", path, nil, "")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, err = io.Copy(w, resp.Body)
	return err
}

// send makes the request and returns a 2xx response whose body the caller
// closes; any other status is an *HTTPError.
func (c *Client) send(ctx context.Context, method, path string, body io.Reader, contentType string) (*http.Response, error) {
	base, err := url.Parse(c.Server)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return nil, fmt.Errorf("invalid server URL %q", c.Server)
	}
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") {
		return nil, fmt.Errorf("invalid API path %q", path)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.Server, "/")+path, body)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	if c.Workspace != "" {
		req.Header.Set("X-Workspace-ID", c.Workspace)
	}
	h := http.Client{Timeout: 30 * time.Second}
	if c.HTTP != nil {
		h = *c.HTTP
	}
	// Redirect policy applies to injected clients without modifying their settings.
	h.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := h.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return resp, nil
	}
	defer resp.Body.Close()
	var e struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return nil, fmt.Errorf("%s %s: read error response: %w", method, path, err)
	}
	_ = json.Unmarshal(b, &e)
	if e.Error == "" {
		e.Error = resp.Status
		if text := strings.TrimSpace(string(b)); text != "" {
			e.Error += ": " + text
		}
	}
	return nil, &HTTPError{Status: resp.StatusCode, Code: e.Code, Msg: fmt.Sprintf("%s %s: %s", method, path, e.Error)}
}

type HTTPError struct {
	Status int
	Code   string
	Msg    string
}

func (e *HTTPError) Error() string { return e.Msg }
