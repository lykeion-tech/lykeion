package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"lykeion/server/internal/apiclient"
	"lykeion/server/pkg/agent"
)

func TestHandInEnvironmentEvidenceSurvivesOutboxAndFallback(t *testing.T) {
	var posts []map[string]json.RawMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		posts = append(posts, body)
		if len(posts) == 1 {
			http.Error(w, "older server", http.StatusBadRequest)
			return
		}
		_, _ = io.WriteString(w, `{"accepted":true}`)
	}))
	defer srv.Close()
	original := Report{HandInMessageSeq: 17, EnvironmentFailures: []string{"python"}}
	saved, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var replay Report
	if err := json.Unmarshal(saved, &replay); err != nil {
		t.Fatal(err)
	}
	d := &daemon{api: &apiclient.Client{Server: srv.URL}}
	problems, err := d.postHandIn(context.Background(), replay, json.RawMessage(`{"status":"could_not_do","blocked_by_environment":"python"}`))
	if err != nil || len(problems) != 0 {
		t.Fatalf("post: %v, problems %v", err, problems)
	}
	if len(posts) != 2 {
		t.Fatalf("posts = %d", len(posts))
	}
	for _, p := range posts {
		var got []string
		if err := json.Unmarshal(p["environment_failures"], &got); err != nil || !reflect.DeepEqual(got, []string{"python"}) {
			t.Fatalf("trusted evidence lost in post: %s (%v)", p["environment_failures"], err)
		}
	}
}

func TestHandInSnapshotsEnvironmentOnEverySubmission(t *testing.T) {
	var posts []map[string]json.RawMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		posts = append(posts, body)
		if len(posts) == 1 {
			_, _ = io.WriteString(w, `{"accepted":false,"problems":["try again"]}`)
			return
		}
		http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	d := &daemon{api: &apiclient.Client{Server: srv.URL}}
	var c claimed
	c.AgentRun.Kind = "autoresearch"
	door := &Door{}
	door.setEnvironmentFailure("python", true)
	_, _, err := d.handIn(context.Background(), c, agent.Result{Output: `{}`}, func(string, string) agent.Result {
		door.setEnvironmentFailure("python", false)
		return agent.Result{Output: `{}`}
	}, func(agent.Message) error { return nil }, door)
	var unsent *unsentHandIn
	if !errors.As(err, &unsent) {
		t.Fatalf("expected retryable unsent hand-in, got %v", err)
	}
	if len(posts) != 2 {
		t.Fatalf("posts = %d", len(posts))
	}
	var first []string
	if err := json.Unmarshal(posts[0]["environment_failures"], &first); err != nil || !reflect.DeepEqual(first, []string{"python"}) {
		t.Fatalf("first submission evidence %s (%v)", posts[0]["environment_failures"], err)
	}
	if _, ok := posts[1]["environment_failures"]; ok {
		t.Fatal("recovered failure leaked into second submission")
	}
	if len(unsent.environmentFailures) != 0 {
		t.Fatalf("outbox kept stale failure: %v", unsent.environmentFailures)
	}
}
