package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"lykeion/server/pkg/agent"
)

type outputsServer struct {
	*httptest.Server
	mu    sync.Mutex
	asked []map[string]any
	auth  []string
}

func newOutputsServer(t *testing.T) *outputsServer {
	o := &outputsServer{}
	o.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		o.mu.Lock()
		defer o.mu.Unlock()
		o.auth = append(o.auth, r.Header.Get("Authorization"))
		switch {
		case r.URL.Path == "/api/role/tools":
			var in map[string]any
			json.NewDecoder(r.Body).Decode(&in)
			o.asked = append(o.asked, in)
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"skills":[],"tools":[{"server":"sources","tool":"search_literature"}]}`))
		case r.URL.Path == "/api/role/outputs":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"outputs":[{"key":"r3","name":"notes.txt","stage":"experimentation","round":1}]}`))
		case r.URL.Path == "/api/role/outputs/r3":
			w.Header().Set("Content-Type", "text/plain")
			w.Write([]byte("original"))
		case r.URL.Path == "/api/role/outputs/by-name":
			switch r.URL.Query().Get("name") {
			case "notes.txt":
				w.Header().Set("Content-Type", "text/plain; charset=utf-8")
				w.Write([]byte("hello"))
			case "big.csv":
				w.Header().Set("Content-Type", "text/csv")
				w.Write([]byte(strings.Repeat("a", maxReadOutput+1)))
			case "fig.png":
				w.Header().Set("Content-Type", "image/png")
				w.Write([]byte{0x89, 'P', 'N', 'G'})
			case "latin.txt":
				w.Header().Set("Content-Type", "text/plain")
				w.Write([]byte{0xff, 0xfe, 'x'})
			case "../x":
				w.WriteHeader(400)
			default:
				w.WriteHeader(404)
			}
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(o.Close)
	return o
}

func handInDoor(t *testing.T, server string) *Door {
	t.Helper()
	d := &Door{Server: server, RunToken: "ltk_run", CallToken: "ltk_call", HandIn: true,
		Child: func() agent.ExecOptions { return agent.ExecOptions{Dir: t.TempDir()} }}
	must(t, d.Start())
	t.Cleanup(d.Close)
	return d
}

func TestOnlyAHandInDoorOffersReadOutputAndFindTools(t *testing.T) {
	o := newOutputsServer(t)
	if got := doorTools(t, memberDoor(t, o.URL, false)); slices.Contains(got, "read_output") || slices.Contains(got, "find_tools") {
		t.Fatalf("a Validation door offers %v", got)
	}
	if got := doorTools(t, handInDoor(t, o.URL)); !slices.Contains(got, "read_output") || !slices.Contains(got, "find_tools") {
		t.Fatalf("a hand-in door offers %v", got)
	}
}

func TestReadOutputAnswersTextAndNamesTheRest(t *testing.T) {
	o := newOutputsServer(t)
	d := handInDoor(t, o.URL)
	if got, err := doorTool(t, d, "list_outputs", map[string]any{"name": "notes.txt"}); err != nil || !strings.Contains(got, `"key":"r3"`) {
		t.Fatalf("catalog: %q %v", got, err)
	}
	if got, err := doorTool(t, d, "read_output", map[string]any{"key": "r3"}); err != nil || got != "original" {
		t.Fatalf("exact output: %q %v", got, err)
	}
	if _, err := doorTool(t, d, "read_output", map[string]any{"key": "r3", "name": "notes.txt"}); err == nil {
		t.Fatal("a name and key together should be rejected")
	}
	if got, err := doorTool(t, d, "read_output", map[string]any{"name": "notes.txt"}); err != nil || got != "hello" {
		t.Fatalf("text: %q %v", got, err)
	}
	for _, name := range []string{"big.csv", "fig.png", "latin.txt"} {
		got, err := doorTool(t, d, "read_output", map[string]any{"name": name})
		if err != nil || !strings.Contains(got, `"name":"`+name+`"`) || !strings.Contains(got, "binary or too large") {
			t.Fatalf("%s: %q %v", name, got, err)
		}
	}
	if _, err := doorTool(t, d, "read_output", map[string]any{"name": "gone.txt"}); err == nil || !strings.Contains(err.Error(), "no file named") {
		t.Fatalf("missing: %v", err)
	}
	if _, err := doorTool(t, d, "read_output", map[string]any{"name": "../x"}); err == nil || !strings.Contains(err.Error(), "relative") {
		t.Fatalf("bad name: %v", err)
	}
	for _, a := range o.auth {
		if a != "Bearer ltk_run" {
			t.Fatalf("outputs read with %q, want the run's token", a)
		}
	}
}

func TestPlanningDoorDefersCodeTools(t *testing.T) {
	d := &Door{HandIn: true, Planning: true}
	must(t, d.Start())
	t.Cleanup(d.Close)
	got := doorTools(t, d)
	for _, name := range []string{"run_code", "run_script", "set_up_environment"} {
		if slices.Contains(got, name) {
			t.Errorf("planning exposed %s", name)
		}
	}
	if !slices.Contains(got, "list_outputs") || !slices.Contains(got, "search_literature") {
		t.Fatalf("planning needs access to evidence: %v", got)
	}
}

func TestFindToolsAsksForNoNotice(t *testing.T) {
	o := newOutputsServer(t)
	d := handInDoor(t, o.URL)
	got, err := doorTool(t, d, "find_tools", map[string]any{"need": "search papers"})
	if err != nil || !strings.Contains(got, "search_literature") {
		t.Fatalf("answer %q %v", got, err)
	}
	if len(o.asked) != 1 || o.asked[0]["notify"] != false {
		t.Fatalf("asked %v, want notify false", o.asked)
	}
}

// A hand-in run cites nothing by hand, so no tool it is offered mentions lyk
// or a record key's cite prefix; a Validation run's tools still say how to
// cite.
func TestAHandInDoorsToolsNameNoProtocol(t *testing.T) {
	o := newOutputsServer(t)
	protocol := regexp.MustCompile(`\blyk\b|\b(?:call|run):`)
	text := func(d *Door) string {
		ctx := context.Background()
		c := mcp.NewClient(&mcp.Implementation{Name: "cli", Version: "0"}, nil)
		s, err := c.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: d.URL, HTTPClient: &http.Client{Transport: bearer(d.Token)}, DisableStandaloneSSE: true}, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		var all string
		for tool, err := range s.Tools(ctx, nil) {
			if err != nil {
				t.Fatal(err)
			}
			schema, _ := json.Marshal(tool.InputSchema)
			all += tool.Name + ": " + tool.Description + " " + string(schema) + "\n"
		}
		return all
	}
	if got := text(handInDoor(t, o.URL)); protocol.MatchString(got) {
		t.Fatalf("a hand-in door's tools name the protocol:\n%s", got)
	}
	if got := text(memberDoor(t, o.URL, false)); !protocol.MatchString(got) {
		t.Fatalf("a Validation door's tools no longer say how to cite:\n%s", got)
	}
}

func TestStudyDoorListsSourceSchemasWithoutJev(t *testing.T) {
	l := newLykeion(t)
	d := &Door{Server: l.URL, Sources: l.URL + "/mcp/sources", RunToken: "ltk_run", CallToken: "ltk_call", HandIn: true, Child: func() agent.ExecOptions { return agent.ExecOptions{Dir: t.TempDir()} }}
	must(t, d.Start())
	t.Cleanup(d.Close)
	got, err := doorTool(t, d, "list_source_tools", map[string]any{})
	if err != nil || !strings.Contains(got, "search_literature") || !strings.Contains(got, "input_schema") {
		t.Fatalf("source schemas: %q %v", got, err)
	}
	if strings.Contains(got, "list_connectors") || strings.Contains(got, `"name":"fetch"`) {
		t.Fatalf("unavailable source tools leaked: %s", got)
	}
}

func TestStudyDoorExposesTypedLiteratureTools(t *testing.T) {
	l := newLykeion(t)
	d := &Door{Server: l.URL, Sources: l.URL + "/mcp/sources", RunToken: "ltk_run", CallToken: "ltk_call", HandIn: true, Child: func() agent.ExecOptions { return agent.ExecOptions{Dir: t.TempDir()} }}
	must(t, d.Start())
	t.Cleanup(d.Close)
	for _, tool := range []string{"search_literature", "fetch_text"} {
		if !slices.Contains(doorTools(t, d), tool) {
			t.Errorf("missing direct tool %s", tool)
		}
	}
	got, err := doorTool(t, d, "search_literature", map[string]any{"database": "europepmc", "query": "walking"})
	if err != nil || !strings.Contains(got, "walking") {
		t.Fatalf("direct search %q: %v", got, err)
	}
	if len(l.posts) != 1 || l.posts[0]["server"] != "sources" || l.posts[0]["tool"] != "search_literature" {
		t.Fatalf("search not recorded: %v", l.posts)
	}
}
