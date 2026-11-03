package lessons

import (
	"encoding/json"
	"fmt"
	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/google/uuid"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAnthropicBatchProtocol(t *testing.T) {
	id := uuid.New()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/messages/batches":
			var p struct {
				Requests []struct {
					CustomID string `json:"custom_id"`
					Params   struct {
						Model  string
						Output struct {
							Format struct {
								Type   string
								Schema map[string]any
							}
						} `json:"output_config"`
					}
				}
			}
			if err := json.NewDecoder(r.Body).Decode(&p); err != nil || len(p.Requests) != 1 {
				t.Errorf("requests %v: %v", p, err)
				return
			}
			req := p.Requests[0]
			if r.Method != "POST" || r.Header.Get("x-api-key") != "fake-server-key" || req.CustomID != id.String() || req.Params.Model != Model || req.Params.Output.Format.Type != "json_schema" || req.Params.Output.Format.Schema["additionalProperties"] != false {
				t.Errorf("incorrect batch contract %v", req)
			}
			fmt.Fprint(w, `{"id":"batch","type":"message_batch","processing_status":"in_progress"}`)
		case "/v1/messages/batches/batch":
			fmt.Fprint(w, `{"id":"batch","type":"message_batch","processing_status":"ended"}`)
		case "/v1/messages/batches/batch/results":
			w.Header().Set("Content-Type", "application/x-jsonl")
			fmt.Fprintf(w, `{"custom_id":"%s","result":{"type":"succeeded","message":{"model":"%s","stop_reason":"end_turn","content":[{"type":"thinking","thinking":"private"},{"type":"text","text":"{\"lessons\":[],\"counters\":[],\"replacement\":null}"}]}}}`, id, Model)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	b := AnthropicBatcher{Client: anthropic.NewClient(option.WithAPIKey("fake-server-key"), option.WithBaseURL(server.URL)), Model: Model}
	batch, err := b.Submit(t.Context(), []Request{{id, "synthetic"}})
	if err != nil {
		t.Fatal(err)
	}
	results, done, err := b.Collect(t.Context(), batch)
	if err != nil || !done || len(results) != 1 || results[0].Err != "" || results[0].JobID != id {
		t.Fatalf("results=%+v done=%v err=%v", results, done, err)
	}
}
