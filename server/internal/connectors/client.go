package connectors

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"

	"lykeion/server/internal/seal"
)

const (
	Timeout = 30 * time.Second
	MaxBody = 2 << 20
	maxWait = 5 * time.Second // a request whose rate slot is further away is refused, not queued
)

var (
	// ErrRefused marks a request Lykeion would not send, or a response it would not keep.
	ErrRefused  = errors.New("refused")
	ErrNoSecret = seal.ErrNoKey
)

// Request is what the agent asked for. It never holds a key: the server adds
// that when it sends the request.
type Request struct {
	Connector string            `json:"connector"`
	Method    string            `json:"method"`
	Path      string            `json:"path"`
	Query     map[string]string `json:"query,omitempty"`
	Variables map[string]any    `json:"variables,omitempty"`
}

// NewRequest is a GET, or a POST of the entry's fixed body when the agent
// passes variables for it.
func NewRequest(connector, path string, query map[string]string, vars map[string]any) Request {
	m := "GET"
	if len(vars) > 0 {
		m = "POST"
	}
	return Request{Connector: connector, Method: m, Path: path, Query: query, Variables: vars}
}

// Canonical is the request as JSON with sorted keys: equal requests encode
// to equal bytes.
func (r Request) Canonical() []byte {
	b, _ := json.Marshal(r)
	return b
}

func (r Request) SHA256() string {
	s := sha256.Sum256(r.Canonical())
	return hex.EncodeToString(s[:])
}

// SourceID is the id a fetched record is cited by.
func (r Request) SourceID() string { return r.Connector + ":" + r.SHA256() }

type Response struct {
	Status                  int
	ContentType, RetryAfter string
	Body                    []byte
	Truncated               bool
}

// Client fetches from the catalogue. HTTP must refuse private addresses
// (Guarded does); Contact is the server's contact email; Secret is the server's LYK_SECRET_KEY, which seals keys.
type Client struct {
	HTTP    *http.Client
	Contact string
	Secret  []byte

	mu   sync.Mutex
	next map[string]time.Time
}

func refuse(format string, a ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrRefused}, a...)...)
}

// Check refuses a request Do would refuse, without sending anything.
func (c *Client) Check(e Entry, r Request) error {
	_, _, err := build(e, r)
	return err
}

// build turns a request into the URL under the entry's base (without key or
// contact) and the POST body, or refuses it.
func build(e Entry, r Request) (*url.URL, []byte, error) {
	if r.Connector != e.Name {
		return nil, nil, refuse("the request names %s, not %s", r.Connector, e.Name)
	}
	if !slices.Contains(e.Methods, r.Method) {
		how := "pass no variables"
		if e.Body != "" {
			how = "pass the variables " + strings.Join(e.Variables(), ", ")
		}
		return nil, nil, refuse("%s takes only %s requests: %s", e.Name, strings.Join(e.Methods, " and "), how)
	}
	if len(r.Path) > 2048 || strings.ContainsAny(r.Path, "?#\\") || strings.HasPrefix(r.Path, "//") ||
		(r.Path != "" && !strings.HasPrefix(r.Path, "/")) {
		return nil, nil, refuse("path %q: give only the path under the connector's base, starting with /, and query parameters in query", r.Path)
	}
	p, err := url.PathUnescape(r.Path)
	if err != nil || strings.ContainsFunc(p, unicode.IsControl) {
		return nil, nil, refuse("path %q is not a valid path", r.Path)
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "." || seg == ".." {
			return nil, nil, refuse("path %q leaves the connector's base", r.Path)
		}
	}
	if len(r.Query) > 50 {
		return nil, nil, refuse("at most 50 query parameters")
	}
	q := url.Values{}
	for k, v := range r.Query {
		if k == "" || len(k) > 128 || len(v) > 8<<10 || strings.ContainsFunc(k+v, unicode.IsControl) {
			return nil, nil, refuse("query parameter %q is empty, too long or holds control characters", k)
		}
		if (e.KeyQuery != "" && strings.EqualFold(k, e.KeyQuery)) || (e.Contact != "" && strings.EqualFold(k, e.Contact)) {
			return nil, nil, refuse("%s is set by the server", k)
		}
		q.Set(k, v)
	}
	if len(r.Canonical()) > 64<<10 {
		return nil, nil, refuse("the request is over 64 KiB")
	}
	u, err := url.Parse(e.Base)
	if err != nil {
		return nil, nil, err
	}
	if p != "" {
		u.Path = strings.TrimSuffix(u.Path, "/") + p
	}
	u.RawQuery = q.Encode()
	var body []byte
	if r.Method == "POST" {
		if body, err = fill(e.Body, r.Variables); err != nil {
			return nil, nil, err
		}
	}
	return u, body, nil
}

// fill puts the agent's variables into the entry's fixed body. Only string
// values that are exactly "$name" are slots, so a variable is always a JSON
// value of its own and never part of the query text.
func fill(tmpl string, vars map[string]any) ([]byte, error) {
	var v any
	if err := json.Unmarshal([]byte(tmpl), &v); err != nil {
		return nil, err
	}
	used := map[string]bool{}
	var missing []string
	var walk func(any) any
	walk = func(x any) any {
		switch t := x.(type) {
		case string:
			if name, ok := strings.CutPrefix(t, "$"); ok && name != "" {
				val, ok := vars[name]
				if !ok {
					missing = append(missing, name)
					return nil
				}
				used[name] = true
				return val
			}
		case map[string]any:
			for k, c := range t {
				t[k] = walk(c)
			}
		case []any:
			for i, c := range t {
				t[i] = walk(c)
			}
		}
		return x
	}
	v = walk(v)
	if len(missing) > 0 {
		slices.Sort(missing)
		return nil, refuse("the variables %s are required", strings.Join(missing, ", "))
	}
	for k := range vars {
		if !used[k] {
			return nil, refuse("this connector's body has no variable %q", k)
		}
	}
	return json.Marshal(v)
}

// Do sends one request with the workspace's key, if any, and returns the
// upstream's answer. It waits for the connector's rate in this workspace,
// follows redirects only within the entry's hosts, caps the body and removes
// the key from whatever comes back.
func (c *Client) Do(ctx context.Context, ws uuid.UUID, e Entry, r Request, key string) (Response, error) {
	u, body, err := build(e, r)
	if err != nil {
		return Response{}, err
	}
	q := u.Query()
	if e.KeyQuery != "" && key != "" {
		q.Set(e.KeyQuery, key)
	}
	if e.Contact != "" && c.Contact != "" {
		q.Set(e.Contact, c.Contact)
	}
	u.RawQuery = q.Encode()
	if err := c.wait(ctx, ws.String()+"/"+e.Name, e.Rate); err != nil {
		return Response{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, r.Method, u.String(), rd)
	if err != nil {
		return Response{}, errors.New(scrub(err.Error(), key))
	}
	req.Header.Set("Accept", "application/json, text/plain;q=0.9, */*;q=0.5")
	req.Header.Set("User-Agent", "Lykeion")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if e.KeyHeader != "" && key != "" {
		req.Header.Set(e.KeyHeader, key)
	}
	hc := http.Client{Transport: c.httpClient().Transport, Timeout: Timeout}
	hc.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return refuse("more than 5 redirects")
		}
		if next.URL.Scheme != "https" || next.URL.Port() != "" || !slices.Contains(e.Hosts, next.URL.Hostname()) {
			return refuse("%s redirected to %s, outside its hosts", e.Name, next.URL.Hostname())
		}
		return nil
	}
	res, err := hc.Do(req)
	if err != nil {
		msg := scrub(err.Error(), key)
		if errors.Is(err, ErrRefused) {
			return Response{}, refuse("%s", msg)
		}
		return Response{}, errors.New(msg)
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, MaxBody+1))
	if err != nil {
		return Response{}, errors.New(scrub(err.Error(), key))
	}
	out := Response{Status: res.StatusCode, ContentType: res.Header.Get("Content-Type"), RetryAfter: res.Header.Get("Retry-After")}
	b = gunzipAgain(b)
	if len(b) > MaxBody {
		b, out.Truncated = cut(b, MaxBody), true
	}
	if !utf8.Valid(b) {
		return Response{}, refuse("%s answered with something that is not text; fetch is for records and API responses", e.Name)
	}
	out.Body = []byte(scrub(string(b), key))
	return out, nil
}

// gunzipAgain undoes a second gzip layer: some upstreams (rest.uniprot.org)
// compress a body that Go's transport has already decompressed once. The
// decompressed bytes are capped at MaxBody+1, so a bomb costs no more than a
// plain oversized answer; a body that does not decode is returned unchanged.
func gunzipAgain(b []byte) []byte {
	if len(b) < 2 || b[0] != 0x1f || b[1] != 0x8b {
		return b
	}
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return b
	}
	out, _ := io.ReadAll(io.LimitReader(zr, MaxBody+1))
	if len(out) == 0 {
		return b
	}
	return out
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return Guarded()
}

// cut shortens b to at most n bytes without splitting a character.
func cut(b []byte, n int) []byte {
	b = b[:n]
	for i := len(b) - 1; i >= 0 && i >= len(b)-utf8.UTFMax; i-- {
		if utf8.RuneStart(b[i]) {
			if !utf8.FullRune(b[i:]) {
				b = b[:i]
			}
			break
		}
	}
	return b
}

// scrub removes every spelling of the key a URL or body may carry.
func scrub(s, key string) string {
	if key == "" {
		return s
	}
	for _, k := range []string{key, url.QueryEscape(key), url.PathEscape(key)} {
		s = strings.ReplaceAll(s, k, "[key removed]")
	}
	return s
}

// wait holds the request until its rate slot in the workspace comes up.
func (c *Client) wait(ctx context.Context, k string, rate float64) error {
	d, ok := c.reserve(k, rate, time.Now())
	if !ok {
		return fmt.Errorf("rate limit: this connector allows %.2g requests a second per workspace; retry in %s", rate, d.Round(time.Second))
	}
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// reserve takes the next slot of k at rate per second and says how long
// until it. It takes nothing, and says false, when that is over maxWait.
// ponytail: one server process keeps the slots in memory; move them to
// Postgres when hosting runs several server instances.
func (c *Client) reserve(k string, rate float64, now time.Time) (time.Duration, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.next == nil {
		c.next = map[string]time.Time{}
	}
	at := c.next[k]
	if at.Before(now) {
		at = now
	}
	if at.Sub(now) > maxWait {
		return at.Sub(now), false
	}
	c.next[k] = at.Add(time.Duration(float64(time.Second) / rate))
	return at.Sub(now), true
}

var reserved = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("240.0.0.0/4"), netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("2001:db8::/32"), netip.MustParsePrefix("2002::/16"),
}

// Public says whether an address is a public unicast address the server may
// reach. Loopback, private, link-local, shared, documentation and
// translation ranges are not.
func Public(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() {
		return false
	}
	for _, p := range reserved {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}

// Guarded is the client for real upstreams: it checks every address it
// dials, after DNS and on every redirect, and uses no proxy.
func Guarded() *http.Client {
	d := &net.Dialer{Timeout: 10 * time.Second, Control: func(_, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return err
		}
		ip, err := netip.ParseAddr(host)
		if err != nil {
			return err
		}
		if !Public(ip) {
			return refuse("%s is not a public address", ip)
		}
		return nil
	}}
	return &http.Client{Transport: &http.Transport{Proxy: nil, DialContext: d.DialContext, ForceAttemptHTTP2: true,
		TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: Timeout, MaxIdleConnsPerHost: 4}}
}

func aad(ws uuid.UUID, name string) []byte { return []byte(ws.String() + "/" + name) }

// Seal encrypts a workspace's key for one connector under the server's
// LYK_SECRET_KEY; the sealed bytes open only for that workspace and connector.
func (c *Client) Seal(ws uuid.UUID, name, key string) ([]byte, error) {
	return seal.Seal(c.Secret, []byte(key), aad(ws, name))
}

func (c *Client) Open(ws uuid.UUID, name string, sealed []byte) (string, error) {
	k, err := seal.Open(c.Secret, sealed, aad(ws, name))
	if errors.Is(err, ErrNoSecret) {
		return "", err
	}
	if err != nil {
		return "", errors.New("the sealed key does not open with this server's LYK_SECRET_KEY")
	}
	return string(k), nil
}
