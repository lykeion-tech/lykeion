package addon

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"lykeion/server/pkg/agent"
)

func TestParseRefusesWhatCouldShadowSourcesOrCarryASecret(t *testing.T) {
	one := func(name, url string) string { return fmt.Sprintf(`[{"name":%q,"url":%q}]`, name, url) }
	var nine []string
	for i := 0; i < 9; i++ {
		nine = append(nine, fmt.Sprintf(`{"name":"s%d","url":"https://t.org/mcp"}`, i))
	}
	for _, c := range []struct{ raw, want string }{
		{one("sources", "https://t.org/mcp"), "reserved"},
		{one("lyk_sources", "https://t.org/mcp"), "reserved"},
		{one("lyk-tools", "https://t.org/mcp"), "reserved"},
		{one("lykeion", "https://t.org/mcp"), "reserved"},
		{one("Sources", "https://t.org/mcp"), "lowercase"},
		{one("LYK", "https://t.org/mcp"), "lowercase"},
		{one("a.b", "https://t.org/mcp"), "lowercase"},
		{one("", "https://t.org/mcp"), "lowercase"},
		{one(strings.Repeat("a", 29), "https://t.org/mcp"), "lowercase"},
		{`[{"name":"a","url":"https://t.org/mcp"},{"name":"a","url":"https://u.org/mcp"}]`, "twice"},
		{"[" + strings.Join(nine, ",") + "]", "at most 8"},
		{`[{"name":"tools","command":"npx","args":["-y","some-server"]}]`, "stdio"},
		{`[{"name":"tools","url":"https://t.org/mcp","headers":{"Authorization":"Bearer s"}}]`, "credentials"},
		{`[{"name":"tools","url":"https://t.org/mcp","env":{"KEY":"s"}}]`, "credentials"},
		{one("tools", "https://user:pass@t.org/mcp"), "credentials"},
		{one("tools", "https://t.org/mcp?api_key=s"), "query"},
		{one("tools", "https://t.org/mcp?"), "query"},
		{one("tools", "https://t.org/mcp#token"), "query"},
		{one("tools", "ftp://t.org/mcp"), "http"},
		{one("tools", "https:///mcp"), "host"},
		{one("tools", "https://t.org/m cp"), "ASCII"},
		{one("tools", "https://tö.org/mcp"), "ASCII"},
		{one("tools", "https://t.org/"+strings.Repeat("a", 2048)), "longer"},
		{`[{"name":1,"url":"https://t.org/mcp"}]`, "string"},
		{`{"name":"tools"}`, "array"},
		{`null`, "array"},
		{`true`, "array"},
		{`"tools"`, "array"},
	} {
		_, err := Parse(json.RawMessage(c.raw))
		if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v (want %q)", c.raw, err, c.want)
		}
	}
}

func TestParseKeepsOrder(t *testing.T) {
	ss, err := Parse(json.RawMessage(`[{"url":"https://b.org/mcp","name":"beta"},{"name":"alpha","url":"http://127.0.0.1:8766/mcp"}]`))
	if err != nil || len(ss) != 2 || ss[0] != (agent.MCPServer{Name: "beta", URL: "https://b.org/mcp"}) || ss[1].Name != "alpha" {
		t.Fatalf("%+v %v", ss, err)
	}
	if ss, err := Parse(json.RawMessage(`[]`)); err != nil || len(ss) != 0 {
		t.Fatalf("empty: %+v %v", ss, err)
	}
}

func TestCheckTextBounds(t *testing.T) {
	if err := CheckText(strings.Repeat("a", MaxSkillBytes)); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{strings.Repeat("a", MaxSkillBytes+1), "a\x00b", "a\xffb"} {
		if err := CheckText(s); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q accepted", s[:3])
		}
	}
}

func TestCanonicalAndHash(t *testing.T) {
	ss := []agent.MCPServer{{Name: "lab-index", URL: "https://tools.lab.org/a&b/mcp"}, {Name: "atlas", URL: "http://127.0.0.1:8766/mcp"}}
	want := `[{"name":"lab-index","url":"https://tools.lab.org/a&b/mcp"},{"name":"atlas","url":"http://127.0.0.1:8766/mcp"}]`
	if got := string(Canonical(ss)); got != want {
		t.Fatalf("canonical:\n%s\nwant\n%s", got, want)
	}
	if string(Canonical(nil)) != "[]" {
		t.Fatal("no servers must be []")
	}
	a := Addon{SkillMD: "Also search the lab index.", MCP: ss}
	sum := sha256.Sum256([]byte("Also search the lab index." + "\x00" + want))
	if a.Hash() != hex.EncodeToString(sum[:]) {
		t.Fatalf("hash %s", a.Hash())
	}
	if (Addon{}).Hash() != "" {
		t.Fatal("an empty add-on must hash to nothing")
	}
	if (Addon{SkillMD: "x"}).Hash() == (Addon{SkillMD: "x", MCP: ss[:1]}).Hash() ||
		(Addon{MCP: ss}).Hash() == (Addon{MCP: []agent.MCPServer{ss[1], ss[0]}}).Hash() {
		t.Fatal("hashes collide")
	}
}

func TestServersChecksSnapshotsAndReturnsNilWhenEmpty(t *testing.T) {
	for _, raw := range []json.RawMessage{nil, json.RawMessage(`[]`)} {
		ss, err := Servers(raw)
		if err != nil || ss != nil {
			t.Fatalf("empty snapshot: %#v %v", ss, err)
		}
	}
	for _, raw := range []string{`null`, `[{"name":"sources","url":"https://t.org/mcp"}]`, `[{"name":"tools","url":"https://t.org/mcp","env":{}}]`} {
		if _, err := Servers(json.RawMessage(raw)); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid snapshot %s: %v", raw, err)
		}
	}
	ss, err := Servers(json.RawMessage(`[{"name":"tools","url":"https://t.org/mcp"}]`))
	if err != nil || len(ss) != 1 || ss[0].Name != "tools" {
		t.Fatalf("valid snapshot: %+v %v", ss, err)
	}
}
