package jev

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
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

func TestScoreSendsOrderedLevels(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Questions map[string]struct {
				Type     string
				Criteria []string
			}
		}
		json.NewDecoder(r.Body).Decode(&body)
		q := body.Questions["a"]
		if q.Type != "score" || len(q.Criteria) != 3 || q.Criteria[0] != "low" || q.Criteria[2] != "high" || len(body.Questions) != 2 {
			t.Errorf("bad body %+v", body)
		}
		w.Write([]byte(`{"model":"jev-1.13.0","answers":{"a":{"type":"score","score":1.4,"confidence":0.5,"probabilities":{"1":0.6,"2":0.4}},"b":{"type":"score","score":0.1,"confidence":0.9,"probabilities":{"0":1}}}}`))
	}))
	defer s.Close()
	c := &Client{BaseURL: s.URL, Key: "k", Model: Model}
	out, err := c.Score(context.Background(), "s", map[string]ScoreQuestion{
		"a": {Instructions: "rate", Levels: []string{"low", "mid", "high"}},
		"b": {Instructions: "rate", Levels: []string{"no", "yes"}},
	})
	if err != nil || out["a"].Score != 1.4 || out["a"].Probabilities["1"] != 0.6 || out["b"].Confidence != 0.9 {
		t.Fatalf("%+v %v", out, err)
	}
}

func TestNoulReturnsTheYesProbability(t *testing.T) {
	s := replying(`{"model":"jev-1.13.0","answers":{"q":{"type":"noul","noul":0.71}}}`)
	defer s.Close()
	c := &Client{BaseURL: s.URL, Key: "k", Model: Model}
	out, err := c.Noul(context.Background(), "s", map[string]string{"q": "is it so?"})
	if err != nil || out["q"] != 0.71 {
		t.Fatalf("%v %v", out, err)
	}
}

func TestAnAnswerFromAnotherModelIsRefused(t *testing.T) {
	s := replying(`{"model":"jev-1.14.0","answers":{"q":{"type":"noul","noul":0.71}}}`)
	defer s.Close()
	c := &Client{BaseURL: s.URL, Key: "k", Model: Model}
	if _, err := c.Noul(context.Background(), "s", map[string]string{"q": "?"}); err == nil {
		t.Error("noul: want error")
	}
	if _, err := c.Score(context.Background(), "s", map[string]ScoreQuestion{"q": {Levels: []string{"a", "b"}}}); err == nil {
		t.Error("score: want error")
	}
}

func TestAMissingQuestionIsRefused(t *testing.T) {
	s := replying(`{"model":"jev-1.13.0","answers":{"x":{"type":"noul","noul":0.7}}}`)
	defer s.Close()
	c := &Client{BaseURL: s.URL, Key: "k", Model: Model}
	if _, err := c.Noul(context.Background(), "s", map[string]string{"q": "?"}); err == nil {
		t.Error("noul: want error")
	}
	if _, err := c.Score(context.Background(), "s", map[string]ScoreQuestion{"q": {Levels: []string{"a", "b"}}}); err == nil {
		t.Error("score: want error")
	}
}

func TestRetryDoStopsAtTheSharedBudget(t *testing.T) {
	var slept []time.Duration
	r := NewRetry()
	r.Sleep = func(_ context.Context, d time.Duration) error { slept = append(slept, d); return nil }
	n := 0
	err := r.Do(context.Background(), func() error { n++; return errors.New("down") })
	if err == nil || n != 3 || len(slept) != 2 {
		t.Fatalf("%v %d %v", err, n, slept)
	}
	if err := r.Do(context.Background(), func() error { n++; return errors.New("down") }); err == nil || n != 4 {
		t.Fatalf("budget spent: want one attempt, got n=%d %v", n, err)
	}
}

func TestMalformedScoreAndNoulAnswersAreRefused(t *testing.T) {
	for name, body := range map[string]string{
		"no score":    `{"model":"jev-1.13.0","answers":{"q":{"type":"score","confidence":0.5}}}`,
		"other type":  `{"model":"jev-1.13.0","answers":{"q":{"type":"noul","noul":0.5}}}`,
		"too high":    `{"model":"jev-1.13.0","answers":{"q":{"type":"score","score":2.1}}}`,
		"negative":    `{"model":"jev-1.13.0","answers":{"q":{"type":"score","score":-0.1}}}`,
		"null answer": `{"model":"jev-1.13.0","answers":{"q":null}}`,
	} {
		s := replying(body)
		c := &Client{BaseURL: s.URL, Key: "k", Model: Model}
		if out, err := c.Score(context.Background(), "s", map[string]ScoreQuestion{"q": {Levels: []string{"a", "b"}}}); err == nil {
			t.Errorf("%s: want error, got %+v", name, out)
		}
		s.Close()
	}
	for _, body := range []string{
		`{"model":"jev-1.13.0","answers":{"q":{"type":"noul","noul":1.2}}}`,
		`{"model":"jev-1.13.0","answers":{"q":{"type":"noul","noul":-0.2}}}`,
	} {
		s := replying(body)
		c := &Client{BaseURL: s.URL, Key: "k", Model: Model}
		if out, err := c.Noul(context.Background(), "s", map[string]string{"q": "?"}); err == nil {
			t.Errorf("want error, got %v", out)
		}
		s.Close()
	}
}

func TestNoQuestionsSendsNothing(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("request sent") }))
	defer s.Close()
	c := &Client{BaseURL: s.URL, Key: "k", Model: Model}
	if _, err := c.Noul(context.Background(), "s", nil); err == nil {
		t.Error("want error")
	}
	if _, err := c.Score(context.Background(), "s", nil); err == nil {
		t.Error("want error")
	}
}
