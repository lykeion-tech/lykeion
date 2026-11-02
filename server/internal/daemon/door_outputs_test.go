package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

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
