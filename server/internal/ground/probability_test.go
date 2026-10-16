package ground

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"lykeion/server/internal/board"
	"lykeion/server/internal/jev"
)

func TestAnsweredZeroProbabilitySurvivesGateJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"model":"jev-1.13.0","answers":{"q":{"choice":"supports","probabilities":{"supports":0}}}}`))
	}))
	defer srv.Close()
	j := &jev.Client{BaseURL: srv.URL, Model: jev.Model, HTTP: srv.Client()}
	ch := Ask(context.Background(), j, board.Check{ID: "evidence.zero", Question: "Does the evidence support the claim?"}, "", map[string]string{"supports": "supports"}, func(a jev.Answer) (board.Result, float64) {
		p := a.Probabilities[a.Choice]
		return board.FromP(p), p
	})
	b, err := json.Marshal(board.Gate{Checks: []board.Check{ch}})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Checks []map[string]any `json:"checks"`
	}
	if err = json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if p, ok := got.Checks[0]["p"]; !ok || p != float64(0) {
		t.Fatalf("answered zero probability missing or changed: %s", b)
	}
	if ch.Result != board.Fail || ch.Choice != "supports" {
		t.Fatalf("answer changed: %+v", ch)
	}
}

func TestUnknownProbabilityStaysAbsent(t *testing.T) {
	for _, raw := range []string{`{"id":"old","judge":"jev","result":"attention","question":"Historical?"}`, `{"id":"code","judge":"code","result":"pass"}`} {
		var ch board.Check
		if err := json.Unmarshal([]byte(raw), &ch); err != nil {
			t.Fatal(err)
		}
		b, err := json.Marshal(ch)
		if err != nil {
			t.Fatal(err)
		}
		var got map[string]any
		if err = json.Unmarshal(b, &got); err != nil {
			t.Fatal(err)
		}
		if _, ok := got["p"]; ok {
			t.Fatalf("invented probability: %s", b)
		}
	}
}

func TestUnavailableJevDoesNotInventProbability(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	for _, j := range []*jev.Client{nil, {BaseURL: srv.URL, Model: jev.Model, HTTP: srv.Client()}} {
		ch := Ask(context.Background(), j, board.Check{ID: "unknown", Question: "Does it?"}, "", Relation, func(jev.Answer) (board.Result, float64) {
			t.Fatal("judge called without an answer")
			return board.Fail, 0
		})
		b, err := json.Marshal(ch)
		if err != nil {
			t.Fatal(err)
		}
		var got map[string]any
		if err = json.Unmarshal(b, &got); err != nil {
			t.Fatal(err)
		}
		if _, ok := got["p"]; ok {
			t.Fatalf("invented probability: %s", b)
		}
		if ch.Result != board.Attention {
			t.Fatalf("unavailable answer approved: %+v", ch)
		}
	}
}
