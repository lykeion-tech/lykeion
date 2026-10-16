package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestHypCommandsNeedAnAgentRunToken(t *testing.T) {
	t.Setenv("LYK_TOKEN", "")
	for _, args := range [][]string{{"hyp", "candidate"}, {"hyp", "rank"}, {"hyp", "missing-premise", "--gap", "g"},
		{"hyp", "novelty", "--candidate", "H1", "--status", "unknown", "--query", "q"}} {
		cmd := Root(strings.NewReader(`{}`), &strings.Builder{})
		cmd.SetArgs(args)
		cmd.SetErr(&strings.Builder{})
		if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "LYK_TOKEN") {
			t.Fatalf("%v: %v", args, err)
		}
	}
}

func TestHypStdinMustBeOneJSONObject(t *testing.T) {
	t.Setenv("LYK_TOKEN", "ltk_x")
	t.Setenv("LYK_SERVER", "http://127.0.0.1:1")
	for _, in := range []string{"", "not json", "[1, 2]", `{"a": 1} {"b": 2}`} {
		cmd := Root(strings.NewReader(in), &strings.Builder{})
		cmd.SetArgs([]string{"hyp", "matrix"})
		cmd.SetErr(&strings.Builder{})
		if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "one JSON object") {
			t.Fatalf("%q: %v", in, err)
		}
	}
}

func TestMissingPremiseRequest(t *testing.T) {
	for _, typed := range []bool{false, true} {
		want := map[string]string{"gap": "sensor collection context", "step": ""}
		args := []string{"hyp", "missing-premise", "--gap", want["gap"]}
		if typed {
			want["kind"] = "dataset_metadata"
			want["dataset"] = "D2"
			want["why_required"] = "align observation windows"
			args = append(args, "--kind", want["kind"], "--dataset", want["dataset"], "--why-required", want["why_required"])
		}
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer ltk_run" || r.Header.Get("X-Workspace-ID") != "" {
				t.Error("wrong credentials")
			}
			var req struct {
				Kind string
				Body map[string]string
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatal(err)
			}
			if req.Kind != "missing_premise" || !reflect.DeepEqual(req.Body, want) {
				t.Errorf("%+v want %v", req, want)
			}
			if typed {
				http.Error(w, "dataset is not a live Grounding dataset", 400)
				return
			}
			w.Write([]byte(`{"key":"ok"}`))
		}))
		t.Setenv("LYK_TOKEN", "ltk_run")
		t.Setenv("LYK_SERVER", srv.URL)
		cmd := Root(strings.NewReader(""), &strings.Builder{})
		cmd.SetArgs(args)
		cmd.SetErr(&strings.Builder{})
		err := cmd.Execute()
		srv.Close()
		if typed {
			if err == nil || !strings.Contains(err.Error(), "live Grounding dataset") {
				t.Fatal(err)
			}
		} else if err != nil {
			t.Fatal(err)
		}
	}
}
