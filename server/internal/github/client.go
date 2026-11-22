package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"lykeion/server/internal/store"
)

type Service struct {
	Store       *store.Store
	Config      Config
	HTTP        *http.Client
	PublicURL   string
	StateSecret []byte
	now         func() time.Time
}

func (s *Service) currentTime() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

func (s *Service) appJWT() (string, error) {
	if s.Config.AppID <= 0 || s.Config.PrivateKey == nil {
		return "", errors.New("GitHub App signing credentials are missing")
	}
	now := s.currentTime()
	claims := jwt.RegisteredClaims{
		Issuer:    strconv.FormatInt(s.Config.AppID, 10),
		IssuedAt:  jwt.NewNumericDate(now.Add(-time.Minute)),
		ExpiresAt: jwt.NewNumericDate(now.Add(9 * time.Minute)),
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodRS256, claims).SignedString(s.Config.PrivateKey)
	if err != nil {
		return "", errors.New("could not sign GitHub App JWT")
	}
	return token, nil
}

type APIError struct {
	Status         int
	Message        string
	RetryAfter     time.Duration
	RetryAfterSet  bool
	RateLimitReset time.Time
}

func (e *APIError) Error() string {
	return fmt.Sprintf("GitHub API returned HTTP %d: %s", e.Status, e.Message)
}

func (s *Service) apiError(resp *http.Response) *APIError {
	err := &APIError{Status: resp.StatusCode, Message: http.StatusText(resp.StatusCode)}
	retry := resp.Header.Get("Retry-After")
	if seconds, parseErr := strconv.ParseInt(retry, 10, 64); parseErr == nil && seconds >= 0 {
		err.RetryAfter = time.Duration(min(seconds, math.MaxInt64/int64(time.Second))) * time.Second
		err.RetryAfterSet = true
	} else if retryAt, parseErr := http.ParseTime(retry); parseErr == nil {
		err.RetryAfter = max(0, retryAt.Sub(s.currentTime()))
		err.RetryAfterSet = true
	}
	if reset, parseErr := strconv.ParseInt(resp.Header.Get("X-RateLimit-Reset"), 10, 63); parseErr == nil && reset >= 0 {
		err.RateLimitReset = time.Unix(reset, 0)
	}
	return err
}

func (s *Service) installationToken(ctx context.Context, installationID int64) (string, error) {
	appToken, err := s.appJWT()
	if err != nil {
		return "", err
	}
	path := fmt.Sprintf("/app/installations/%d/access_tokens", installationID)
	var out struct {
		Token string `json:"token"`
	}
	if _, err := s.request(ctx, http.MethodPost, appToken, path, &out); err != nil {
		return "", err
	}
	if out.Token == "" {
		return "", errors.New("GitHub returned an empty installation token")
	}
	return out.Token, nil
}

func (s *Service) get(ctx context.Context, token, path string, out any) error {
	_, err := s.request(ctx, http.MethodGet, token, path, out)
	return err
}

func (s *Service) request(ctx context.Context, method, token, path string, out any) (*http.Response, error) {
	target, err := endpoint(s.Config.apiURL(), path)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, target.String(), nil)
	if err != nil {
		return nil, errors.New("invalid GitHub API request")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := s.do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, s.apiError(resp)
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return nil, errors.New("could not decode GitHub API response")
		}
	}
	return resp, nil
}

func (s *Service) do(req *http.Request) (*http.Response, error) {
	if req.URL.User != nil || req.URL.Opaque != "" ||
		(req.Host != "" && !strings.EqualFold(req.Host, req.URL.Host)) || !s.allowedOrigin(req.URL) {
		return nil, errors.New("invalid GitHub request origin")
	}
	var client http.Client
	if s.HTTP != nil {
		client = *s.HTTP
	}
	if client.Timeout <= 0 {
		client.Timeout = 30 * time.Second
	}
	// Redirects can resend bearer headers or OAuth form credentials.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		if ctxErr := req.Context().Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, errors.New("GitHub request failed")
	}
	return resp, nil
}

func (s *Service) allowedOrigin(target *url.URL) bool {
	for _, raw := range []string{s.Config.apiURL(), s.Config.webURL()} {
		if base, err := parseBaseURL(raw); err == nil && sameOrigin(base, target) {
			return true
		}
	}
	return false
}

func parseBaseURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" ||
		u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return nil, errors.New("invalid GitHub base URL")
	}
	return u, nil
}

func endpoint(baseURL, path string) (*url.URL, error) {
	base, err := parseBaseURL(baseURL)
	if err != nil {
		return nil, err
	}
	ref, err := url.Parse(path)
	if err != nil || ref.IsAbs() || ref.Host != "" || ref.User != nil || ref.Fragment != "" ||
		!strings.HasPrefix(ref.Path, "/") || strings.HasPrefix(ref.Path, "//") || strings.Contains(ref.Path, "\\") {
		return nil, errors.New("invalid GitHub endpoint")
	}
	for part := range strings.SplitSeq(ref.Path, "/") {
		if part == "." || part == ".." {
			return nil, errors.New("invalid GitHub endpoint")
		}
	}
	// Treat a configured base path, such as /api/v3, as the API prefix.
	ref.RawPath = strings.TrimSuffix(base.EscapedPath(), "/") + ref.EscapedPath()
	ref.Path = strings.TrimSuffix(base.Path, "/") + ref.Path
	ref.Scheme, ref.Host = base.Scheme, base.Host
	return ref, nil
}

func sameOrigin(a, b *url.URL) bool {
	return a.Scheme == b.Scheme && strings.EqualFold(a.Host, b.Host)
}

var (
	nextLinkPattern   = regexp.MustCompile(`(?:^|,)\s*<([^>]*)>\s*;\s*rel\s*=\s*(?:"next"|next)(?:\s*[;,]|\s*$)`)
	canonicalPullPath = regexp.MustCompile(`^/repositories/[1-9][0-9]*/pulls$`)
)

// getPage resolves next links against the current request and keeps paging the same endpoint.
func (s *Service) getPage(ctx context.Context, token, path string, out any) (string, error) {
	resp, err := s.request(ctx, http.MethodGet, token, path, out)
	if err != nil {
		return "", err
	}
	next := nextLinkPattern.FindStringSubmatch(strings.Join(resp.Header.Values("Link"), ","))
	if next == nil {
		return "", nil
	}
	current := resp.Request.URL
	target, err := current.Parse(next[1])
	if err != nil || !sameOrigin(current, target) || target.User != nil || target.Fragment != "" ||
		(target.EscapedPath() != current.EscapedPath() &&
			!(strings.HasPrefix(current.Path, "/repos/") && strings.HasSuffix(current.Path, "/pulls") &&
				canonicalPullPath.MatchString(target.EscapedPath()))) {
		return "", errors.New("invalid GitHub pagination link")
	}
	// GitHub may identify the repository by its numeric ID in a pagination URL.
	// Use its next query on the original path to keep the request scoped to the same repository.
	original, err := url.Parse(path)
	if err != nil {
		return "", errors.New("invalid GitHub endpoint")
	}
	query, err := url.ParseQuery(target.RawQuery)
	if err != nil {
		return "", errors.New("invalid GitHub pagination query")
	}
	pagingQuery := original.Query()
	for _, name := range []string{"page", "per_page", "before", "after", "since"} {
		if values, ok := query[name]; ok {
			pagingQuery[name] = values
		}
	}
	original.RawQuery = pagingQuery.Encode()
	return original.RequestURI(), nil
}

func scrub(err error, secrets ...string) error {
	if err == nil {
		return nil
	}
	// Typed API errors carry only local status text and parsed rate-limit data.
	if apiErr, ok := err.(*APIError); ok {
		return apiErr
	}
	var pairs []string
	for _, secret := range secrets {
		if secret != "" {
			pairs = append(pairs, secret, "[redacted]")
		}
	}
	return errors.New(strings.NewReplacer(pairs...).Replace(err.Error()))
}
