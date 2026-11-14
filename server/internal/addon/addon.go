// Package addon holds a workspace's additions to a column's stage roles: text added
// to its roles' skills, and tool servers they may call. The same rules run
// wherever an add-on crosses a trust boundary: when the owner saves it, when
// an agent run is queued, and when a daemon claims it.
package addon

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/pkg/agent"
	db "lykeion/server/pkg/db/generated"
)

const (
	MaxSkillBytes = 16 << 10 // text added to a role's skill
	MaxServers    = 8
	maxURL        = 2048
)

// Heading separates a role's own skill from the lab's additions.
const Heading = "## Lab additions"

var ErrInvalid = errors.New("invalid add-on")

// A name is a plain lowercase key: Codex takes it inside a -c config key, and
// Claude builds tool names mcp__lyk_<name>__<tool>, which must stay short.
var namePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,27}$`)

type Addon struct {
	SkillMD string
	MCP     []agent.MCPServer
}

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
}

// CheckText refuses text that is too long or that Postgres cannot store.
func CheckText(s string) error {
	switch {
	case len(s) > MaxSkillBytes:
		return invalid("skill_md is %d bytes, more than %d", len(s), MaxSkillBytes)
	case !utf8.ValidString(s):
		return invalid("skill_md is not valid UTF-8")
	case strings.ContainsRune(s, 0):
		return invalid("skill_md contains a NUL byte")
	}
	return nil
}

// CheckServers enforces the server rules: a bounded list of unique plain names
// that cannot stand in for Lykeion's own servers, at http(s) URLs that carry
// no credentials.
func CheckServers(ss []agent.MCPServer) error {
	if len(ss) > MaxServers {
		return invalid("%d MCP servers, at most %d", len(ss), MaxServers)
	}
	seen := map[string]bool{}
	for _, s := range ss {
		switch {
		case s.Name == "sources" || strings.HasPrefix(s.Name, "lyk"):
			return invalid("MCP server name %q is reserved: sources and names starting with lyk belong to Lykeion", s.Name)
		case !namePattern.MatchString(s.Name):
			return invalid("MCP server name %q must be 1 to 28 lowercase letters, digits, _ or -, starting with a letter", s.Name)
		case seen[s.Name]:
			return invalid("MCP server name %q appears twice", s.Name)
		}
		seen[s.Name] = true
		if err := checkURL(s.URL); err != nil {
			return invalid("MCP server %q: %v", s.Name, err)
		}
	}
	return nil
}

func checkURL(raw string) error {
	if len(raw) > maxURL {
		return fmt.Errorf("url longer than %d bytes", maxURL)
	}
	for _, r := range raw {
		if r <= ' ' || r > '~' {
			return errors.New("url must be printable ASCII with no spaces")
		}
	}
	if strings.ContainsAny(raw, "?#") {
		return errors.New("url may not have a query or fragment, where credentials hide; add-on servers take none")
	}
	u, err := url.Parse(raw)
	switch {
	case err != nil:
		return fmt.Errorf("url: %v", err)
	case u.Scheme != "http" && u.Scheme != "https":
		return errors.New("only http and https MCP servers are allowed")
	case u.Host == "" || u.Opaque != "":
		return errors.New("url needs a host")
	case u.User != nil:
		return errors.New("url carries credentials; add-on servers take none")
	}
	return nil
}

// Parse reads add-on servers: a JSON array of {"name", "url"} objects. A
// stdio server, or any field that could carry a credential, is refused.
func Parse(raw json.RawMessage) ([]agent.MCPServer, error) {
	if trimmed := bytes.TrimSpace(raw); len(trimmed) == 0 || trimmed[0] != '[' {
		return nil, invalid(`mcp must be an array of {"name", "url"} objects`)
	}
	var entries []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, invalid(`mcp must be an array of {"name", "url"} objects`)
	}
	out := make([]agent.MCPServer, 0, len(entries))
	for _, e := range entries {
		var s agent.MCPServer
		for k, v := range e {
			var err error
			switch k {
			case "name":
				err = json.Unmarshal(v, &s.Name)
			case "url":
				err = json.Unmarshal(v, &s.URL)
			case "command", "args":
				return nil, invalid("stdio MCP servers are refused: their command would run on every researcher's machine")
			default:
				return nil, invalid("MCP server field %q is not allowed: add-on servers have only a name and a url, and carry no credentials", k)
			}
			if err != nil {
				return nil, invalid("MCP server %s must be a string", k)
			}
		}
		out = append(out, s)
	}
	if err := CheckServers(out); err != nil {
		return nil, err
	}
	return out, nil
}

// Canonical is the servers as JSON in their stored order: no whitespace, each
// object's keys sorted (name, then url), HTML characters left unescaped.
// Names and URLs are printable ASCII, so this is byte for byte what Python's
// json.dumps(servers, sort_keys=True, separators=(",", ":")) writes.
func Canonical(ss []agent.MCPServer) []byte {
	if ss == nil {
		ss = []agent.MCPServer{}
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.Encode(ss)
	return bytes.TrimSuffix(b.Bytes(), []byte("\n"))
}

// Hash identifies what an add-on gave an agent run: sha256 over the text, one NUL
// byte (stored text cannot contain one) and Canonical(MCP), in hex. An empty
// add-on hashes to "", meaning the agent run ran with none.
func (a Addon) Hash() string {
	if a.SkillMD == "" && len(a.MCP) == 0 {
		return ""
	}
	h := sha256.New()
	h.Write([]byte(a.SkillMD))
	h.Write([]byte{0})
	h.Write(Canonical(a.MCP))
	return hex.EncodeToString(h.Sum(nil))
}

// Compose adds the lab's text after a role's skill, under Heading. The role's
// skill comes first and whole; with no text it comes back unchanged.
func Compose(skill, text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return skill
	}
	if !strings.HasSuffix(skill, "\n") {
		skill += "\n"
	}
	return skill + "\n" + Heading + "\n\n" + text + "\n"
}

// Load reads a column's add-on. No row is an empty add-on. A stored row that
// breaks a rule returns an error wrapping ErrInvalid.
func Load(ctx context.Context, q *db.Queries, ws uuid.UUID, column string) (Addon, error) {
	row, err := q.GetStageAddon(ctx, db.GetStageAddonParams{WorkspaceID: ws, BoardColumn: column})
	if errors.Is(err, pgx.ErrNoRows) {
		return Addon{}, nil
	}
	if err != nil {
		return Addon{}, err
	}
	if err := CheckText(row.SkillMd); err != nil {
		return Addon{}, err
	}
	ss, err := Parse(row.Mcp)
	if err != nil {
		return Addon{}, err
	}
	return Addon{SkillMD: row.SkillMd, MCP: ss}, nil
}

// Servers reads the servers an agent run was queued with and checks them again
// before they reach a daemon.
func Servers(raw json.RawMessage) ([]agent.MCPServer, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	ss, err := Parse(raw)
	if err != nil || len(ss) == 0 {
		return nil, err
	}
	return ss, nil
}
