// Package jev asks TypeSafe's Jev typed questions. Code extracts the
// candidates; Jev only chooses among them.
package jev

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"
)

// Model is the pinned model used for server-side scientific judgments.
const Model = "jev-1.13.0"

type Client struct {
	BaseURL string
	Key     string
	Model   string // pinned; an answer from any other model is refused
	HTTP    *http.Client
}

type Answer struct {
	Choice        string             `json:"choice"`
	Probabilities map[string]float64 `json:"probabilities"`
	Confidence    float64            `json:"confidence"`
}

func (c *Client) Choice(ctx context.Context, state, instructions string, options map[string]string) (Answer, error) {
	raw, err := c.post(ctx, state, map[string]any{
		"q": map[string]any{"type": "choice", "instructions": instructions, "criteria": options},
	})
	if err != nil {
		return Answer{}, err
	}
	var a Answer
	if err := json.Unmarshal(raw["q"], &a); err != nil {
		return Answer{}, err
	}
	if _, known := options[a.Choice]; !known {
		return Answer{}, fmt.Errorf("jev returned no choice among the options (got %q)", a.Choice)
	}
	return a, nil
}

// ScoreQuestion rates content against 2 to 10 levels, ordered low to high.
type ScoreQuestion struct {
	Instructions string
	Levels       []string
}

// ScoreAnswer's Score runs from 0 to len(Levels)-1; Probabilities are keyed by level index.
type ScoreAnswer struct {
	Score         float64            `json:"score"`
	Confidence    float64            `json:"confidence"`
	Probabilities map[string]float64 `json:"probabilities"`
}

// Score puts every question in one request.
func (c *Client) Score(ctx context.Context, state string, questions map[string]ScoreQuestion) (map[string]ScoreAnswer, error) {
	qs := map[string]any{}
	for id, q := range questions {
		if len(q.Levels) < 2 || len(q.Levels) > 10 {
			return nil, fmt.Errorf("jev: score %q needs 2 to 10 levels, got %d", id, len(q.Levels))
		}
		qs[id] = map[string]any{"type": "score", "instructions": q.Instructions, "criteria": q.Levels}
	}
	raw, err := c.post(ctx, state, qs)
	if err != nil {
		return nil, err
	}
	out := map[string]ScoreAnswer{}
	for id := range questions {
		var a struct {
			Type          string             `json:"type"`
			Score         *float64           `json:"score"`
			Confidence    float64            `json:"confidence"`
			Probabilities map[string]float64 `json:"probabilities"`
		}
		if err := json.Unmarshal(raw[id], &a); err != nil {
			return nil, err
		}
		top := float64(len(questions[id].Levels) - 1)
		if a.Type != "score" || a.Score == nil || math.IsNaN(*a.Score) || *a.Score < 0 || *a.Score > top {
			return nil, fmt.Errorf("jev: no valid score for %q", id)
		}
		out[id] = ScoreAnswer{Score: *a.Score, Confidence: a.Confidence, Probabilities: a.Probabilities}
	}
	return out, nil
}

// Noul puts every yes/no question in one request and returns each probability of yes.
func (c *Client) Noul(ctx context.Context, state string, questions map[string]string) (map[string]float64, error) {
	qs := map[string]any{}
	for id, instructions := range questions {
		qs[id] = map[string]any{"type": "noul", "instructions": instructions}
	}
	raw, err := c.post(ctx, state, qs)
	if err != nil {
		return nil, err
	}
	out := map[string]float64{}
	for id := range questions {
		var a struct {
			Noul *float64 `json:"noul"`
		}
		if err := json.Unmarshal(raw[id], &a); err != nil {
			return nil, err
		}
		if a.Noul == nil || math.IsNaN(*a.Noul) || *a.Noul < 0 || *a.Noul > 1 {
			return nil, fmt.Errorf("jev: no valid yes/no answer for %q", id)
		}
		out[id] = *a.Noul
	}
	return out, nil
}

// post sends the questions and returns each id's raw answer; it refuses an
// answer from another model and any id the reply leaves out.
func (c *Client) post(ctx context.Context, state string, questions map[string]any) (map[string]json.RawMessage, error) {
	if len(questions) == 0 {
		return nil, errors.New("jev: no questions to ask")
	}
	body, err := json.Marshal(map[string]any{"model": c.Model, "state": state, "questions": questions})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(c.BaseURL, "/")+"/v1/systemone", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Key)
	req.Header.Set("Content-Type", "application/json")
	h := c.HTTP
	if h == nil {
		h = &http.Client{Timeout: 60 * time.Second}
	}
	resp, err := h.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("jev: %s: %s", resp.Status, b)
	}
	var out struct {
		Model   string                     `json:"model"`
		Answers map[string]json.RawMessage `json:"answers"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if out.Model != c.Model {
		return nil, fmt.Errorf("jev answered with %s, pinned %s", out.Model, c.Model)
	}
	for id := range questions {
		if a := out.Answers[id]; len(a) == 0 || string(a) == "null" {
			return nil, fmt.Errorf("jev returned no answer for %q", id)
		}
	}
	return out.Answers, nil
}

// Retry is shared by every attempt at one scoring: Max bounds the waiting
// summed across them, so the scoring stays inside its lease.
type Retry struct {
	Waits []time.Duration
	Max   time.Duration
	Sleep func(context.Context, time.Duration) error
	spent time.Duration
}

// NewRetry retries twice, three minutes apart, and waits six minutes at most per scoring.
func NewRetry() *Retry {
	return &Retry{Waits: []time.Duration{3 * time.Minute, 3 * time.Minute}, Max: 6 * time.Minute, Sleep: sleep}
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Do runs fn until it succeeds or the waits and the shared budget run out,
// then returns its last error.
func (r *Retry) Do(ctx context.Context, fn func() error) error {
	for attempt := 0; ; attempt++ {
		err := fn()
		if err == nil {
			return nil
		}
		if attempt >= len(r.Waits) || r.spent+r.Waits[attempt] > r.Max {
			return fmt.Errorf("jev unavailable after %d attempts: %w", attempt+1, err)
		}
		r.spent += r.Waits[attempt]
		if serr := r.Sleep(ctx, r.Waits[attempt]); serr != nil {
			return fmt.Errorf("jev unavailable after %d attempts: %w", attempt+1, err)
		}
	}
}
