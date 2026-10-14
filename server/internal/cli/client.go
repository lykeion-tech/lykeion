package cli

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

func (c *Client) Do(ctx context.Context, method, path string, in, out any) error {
	base, err := url.Parse(c.Server)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return fmt.Errorf("invalid server URL %q", c.Server)
	}
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") {
		return fmt.Errorf("invalid API path %q", path)
	}
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.Server, "/")+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	if c.Workspace != "" {
		req.Header.Set("X-Workspace-ID", c.Workspace)
	}
	h := c.HTTP
	if h == nil {
		h = &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	resp, err := h.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		b, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if err != nil {
			return fmt.Errorf("%s %s: read error response: %w", method, path, err)
		}
		_ = json.Unmarshal(b, &e)
		if e.Error == "" {
			e.Error = resp.Status
			if text := strings.TrimSpace(string(b)); text != "" {
				e.Error += ": " + text
			}
		}
		return fmt.Errorf("%s %s: %s", method, path, e.Error)
	}
	if out != nil && resp.StatusCode != http.StatusNoContent {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}
