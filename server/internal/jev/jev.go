// Package jev asks TypeSafe's Jev typed questions. Code extracts the
// candidates; Jev only chooses among them.
package jev

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

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
	body, err := json.Marshal(map[string]any{
		"model": c.Model,
		"state": state,
		"questions": map[string]any{
			"q": map[string]any{"type": "choice", "instructions": instructions, "criteria": options},
		},
	})
	if err != nil {
		return Answer{}, err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(c.BaseURL, "/")+"/v1/systemone", bytes.NewReader(body))
	if err != nil {
		return Answer{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Key)
	req.Header.Set("Content-Type", "application/json")
	h := c.HTTP
	if h == nil {
		h = &http.Client{Timeout: 60 * time.Second}
	}
	resp, err := h.Do(req)
	if err != nil {
		return Answer{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return Answer{}, fmt.Errorf("jev: %s: %s", resp.Status, b)
	}
	var out struct {
		Model   string            `json:"model"`
		Answers map[string]Answer `json:"answers"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return Answer{}, err
	}
	if out.Model != c.Model {
		return Answer{}, fmt.Errorf("jev answered with %s, pinned %s", out.Model, c.Model)
	}
	a, ok := out.Answers["q"]
	if !ok {
		return Answer{}, fmt.Errorf("jev returned no answer")
	}
	return a, nil
}
