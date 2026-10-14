package jev

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func server(t *testing.T, status int, model string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/systemone" || r.Header.Get("Authorization") != "Bearer k" {
			t.Errorf("bad request %s %s", r.URL.Path, r.Header.Get("Authorization"))
		}
		var body struct {
			State     string
			Questions map[string]struct {
				Type     string
				Criteria map[string]string
			}
		}
		json.NewDecoder(r.Body).Decode(&body)
		if body.Questions["q"].Type != "choice" || len(body.Questions["q"].Criteria) != 2 || body.State != "s" {
			t.Errorf("bad body %+v", body)
		}
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(map[string]any{"model": model, "answers": map[string]any{
			"q": map[string]any{"type": "choice", "choice": "a", "confidence": 0.9, "probabilities": map[string]float64{"a": 0.9, "b": 0.1}}}})
	}))
}

func TestChoice(t *testing.T) {
	s := server(t, 200, "jev-1.13.0")
	defer s.Close()
	c := &Client{BaseURL: s.URL, Key: "k", Model: "jev-1.13.0"}
	a, err := c.Choice(context.Background(), "s", "pick", map[string]string{"a": "A", "b": "B"})
	if err != nil || a.Choice != "a" || a.Probabilities["a"] != 0.9 {
		t.Fatalf("%+v %v", a, err)
	}
}

func TestErrorsAreNeverAnswers(t *testing.T) {
	for name, s := range map[string]*httptest.Server{"status": server(t, 503, "jev-1.13.0"), "model": server(t, 200, "jev-1.14.0")} {
		c := &Client{BaseURL: s.URL, Key: "k", Model: "jev-1.13.0"}
		if _, err := c.Choice(context.Background(), "s", "pick", map[string]string{"a": "A", "b": "B"}); err == nil {
			t.Errorf("%s: want error", name)
		}
		s.Close()
	}
}

// replying answers every request with body, verbatim.
func replying(body string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(body))
	}))
}

func TestAMissingVerdictIsAnError(t *testing.T) {
	for name, body := range map[string]string{
		"no q":           `{"model":"jev-1.13.0","answers":{}}`,
		"null q":         `{"model":"jev-1.13.0","answers":{"q":null}}`,
		"no choice":      `{"model":"jev-1.13.0","answers":{"q":{"probabilities":{"a":0.9},"confidence":0.9}}}`,
		"empty choice":   `{"model":"jev-1.13.0","answers":{"q":{"choice":"","probabilities":{"a":0.9}}}}`,
		"unknown choice": `{"model":"jev-1.13.0","answers":{"q":{"choice":"c","probabilities":{"c":0.9}}}}`,
		"not json":       `{"model":`,
	} {
		s := replying(body)
		c := &Client{BaseURL: s.URL, Key: "k", Model: "jev-1.13.0"}
		a, err := c.Choice(context.Background(), "s", "pick", map[string]string{"a": "A", "b": "B"})
		if err == nil || a.Choice != "" || a.Probabilities != nil {
			t.Errorf("%s: want an error and no answer, got %+v %v", name, a, err)
		}
		s.Close()
	}
}
