package daemon

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"lykeion/server/internal/apiclient"
	"lykeion/server/internal/handin"
	"lykeion/server/pkg/agent"
)

func TestLocalPathHandInGetsCorrectedBeforeAcceptance(t *testing.T) {
	var posts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/role/records":
			_, _ = io.WriteString(w, `[]`)
		case strings.HasSuffix(r.URL.Path, "/hand-in"):
			var body struct {
				HandIn json.RawMessage `json:"hand_in"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
				return
			}
			posts++
			_, problems := handin.Parse(handin.Grounding, body.HandIn)
			_ = json.NewEncoder(w).Encode(map[string]any{"accepted": len(problems) == 0, "problems": problems})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	d := &daemon{cfg: Config{Server: srv.URL, Home: t.TempDir()}, api: &apiclient.Client{Server: srv.URL}}
	var c claimed
	c.AgentRun.ID, c.AgentRun.Attempt = uuid.New(), 1
	if err := os.Mkdir(filepath.Join(d.cfg.Home, scratchName(c.AgentRun.ID, 1)), 0700); err != nil {
		t.Fatal(err)
	}
	var correction string
	first := agent.Result{SessionID: "s1", Output: `{"status":"done","answer":"Saved [report](/scratch/run/report)."}`}
	result, problems, err := d.handIn(t.Context(), c, first, func(prompt, resume string) agent.Result {
		correction = prompt
		if resume != "s1" {
			t.Errorf("resume = %q", resume)
		}
		return agent.Result{SessionID: "s1", Output: `{"status":"done","answer":"Saved the report as an output."}`}
	}, func(agent.Message) error { return nil }, nil)
	if err != nil || len(problems) != 0 || posts != 2 {
		t.Fatalf("err=%v problems=%q posts=%d", err, problems, posts)
	}
	if !strings.Contains(correction, "put files to keep in outputs/") {
		t.Fatalf("correction=%q", correction)
	}
	if strings.Contains(result.Output, "/scratch/") {
		t.Fatalf("uncorrected result %q", result.Output)
	}
}
