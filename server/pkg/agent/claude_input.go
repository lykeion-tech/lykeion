package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

const claudeInputQueue = 16

var errClaudeInputClosed = errors.New("Claude input is closed")

// claudeInput is the one writer of Claude's stream-json stdin. Frames queue
// boundedly so the stdout reader never blocks on a slow or stuck child.
type claudeInput struct {
	w    io.WriteCloser
	q    chan []byte
	stop chan struct{}
	once sync.Once
}

func newClaudeInput(w io.WriteCloser) *claudeInput {
	in := &claudeInput{w: w, q: make(chan []byte, claudeInputQueue), stop: make(chan struct{})}
	go in.loop()
	return in
}
func (in *claudeInput) loop() {
	for {
		select {
		case <-in.stop:
			return
		case b := <-in.q:
			if _, err := in.w.Write(b); err != nil {
				in.close()
				return
			}
		}
	}
}

// send queues one NDJSON frame; a full queue fails instead of blocking.
func (in *claudeInput) send(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	select {
	case <-in.stop:
		return errClaudeInputClosed
	default:
	}
	select {
	case in.q <- b:
		return nil
	default:
		return fmt.Errorf("Claude input queue is full (%d frames)", claudeInputQueue)
	}
}

// close unblocks a pending write and ends input; safe to call repeatedly.
func (in *claudeInput) close() {
	in.once.Do(func() {
		close(in.stop)
		in.w.Close()
	})
}

func claudeUserFrame(prompt string) any {
	return map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": prompt}}
}

// claudeControl decodes one control_request. This is the version-tested CLI
// framing (2.1.x), not a documented public API. Lykeion answers no host
// callbacks: every request is refused explicitly and recorded, never approved.
// The record names only the request kind and tool, never inputs.
func claudeControl(line []byte) (reply any, record Message, err error) {
	var ev struct {
		RequestID string `json:"request_id"`
		Request   struct {
			Subtype  string `json:"subtype"`
			ToolName string `json:"tool_name"`
		} `json:"request"`
	}
	if err := json.Unmarshal(line, &ev); err != nil {
		return nil, Message{}, fmt.Errorf("invalid Claude control request: %w", err)
	}
	if ev.RequestID == "" || len(ev.RequestID) > 200 || ev.Request.Subtype == "" || len(ev.Request.Subtype) > 100 || len(ev.Request.ToolName) > 200 {
		return nil, Message{}, errors.New("malformed Claude control request")
	}
	const why = "Lykeion does not answer host callbacks; the account's own permission rules decide"
	if ev.Request.Subtype == "can_use_tool" {
		reply = map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": ev.RequestID, "response": map[string]any{"behavior": "deny", "message": why}}}
	} else {
		reply = map[string]any{"type": "control_response", "response": map[string]any{"subtype": "error", "request_id": ev.RequestID, "error": why}}
	}
	record = Message{Type: "status", Tool: ev.Request.ToolName, Content: raw(map[string]string{"type": "blocked_action", "request_id": ev.RequestID, "subtype": ev.Request.Subtype, "decision": "deny"})}
	return reply, record, nil
}
