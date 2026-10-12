package control

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/coder/websocket"
	"github.com/lykeion-ia/lykeion-redesign/backend/internal/store"
	"io"
	"net/http"
	"regexp"
	"time"
)

type frame struct {
	Version      string          `json:"schema_version"`
	Type         string          `json:"type"`
	ID           string          `json:"message_id"`
	Subscription string          `json:"subscription_id,omitempty"`
	Cursor       string          `json:"cursor,omitempty"`
	Payload      json.RawMessage `json:"payload"`
}

func (s *Server) socket(w http.ResponseWriter, r *http.Request, token, user, workspace string, epoch int64) {
	if !s.Origins[r.Header.Get("Origin")] {
		problem(w, 403, "ORIGIN_REQUIRED")
		return
	}
	if r.Header.Get("Sec-WebSocket-Protocol") != "lykeion.workspace.v1" {
		problem(w, 400, "PROTOCOL_REQUIRED")
		return
	}
	// The exact Origin check above is stricter than hostname glob matching in the library.
	c, e := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{"lykeion.workspace.v1"}, OriginPatterns: []string{"*"}})
	if e != nil {
		return
	}
	defer c.CloseNow()
	c.SetReadLimit(65536)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	incoming := make(chan frame, 8)
	errs := make(chan error, 1)
	go func() {
		for {
			kind, raw, e := c.Read(ctx)
			if e != nil {
				select {
				case errs <- e:
				default:
				}
				return
			}
			var f frame
			if e = strictFrame(raw, &f); e != nil || kind != websocket.MessageText || f.Version != "1" || !frameID.MatchString(f.ID) || (f.Subscription != "" && !frameID.MatchString(f.Subscription)) || len(f.Cursor) > 2048 {
				select {
				case errs <- errors.New("invalid frame"):
				default:
				}
				return
			}
			select {
			case incoming <- f:
			case <-ctx.Done():
				return
			default:
				select {
				case errs <- errors.New("frame rate exceeded"):
				default:
				}
				return
			}
		}
	}()
	send := func(kind string, payload any, cursor string) error {
		v := map[string]any{"schema_version": "1", "type": kind, "message_id": store.ID("msg"), "payload": payload}
		if cursor != "" {
			v["cursor"] = cursor
		}
		raw, _ := json.Marshal(v)
		deadline, stop := context.WithTimeout(ctx, 2*time.Second)
		defer stop()
		return c.Write(deadline, websocket.MessageText, raw)
	}
	subscription := ""
	after := int64(0)
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-errs:
			c.Close(websocket.StatusPolicyViolation, "invalid or closed stream")
			return
		case f := <-incoming:
			if _, e = s.DB.Authenticate(ctx, token); e != nil {
				c.Close(websocket.StatusPolicyViolation, "session expired")
				return
			}
			_, current, seq, _, e := s.DB.Access(ctx, workspace, user)
			if e != nil || current != epoch {
				c.Close(websocket.StatusPolicyViolation, "access changed")
				return
			}
			switch f.Type {
			case "subscribe":
				var p struct {
					Channels []string `json:"channels"`
					Subjects []ref    `json:"subject_refs"`
					After    string   `json:"after_cursor"`
				}
				if strictFrame(f.Payload, &p) != nil || len(p.Channels) != 1 || p.Channels[0] != "workspace" || len(p.Subjects) != 0 {
					c.Close(websocket.StatusPolicyViolation, "unsupported subscription")
					return
				}
				after = seq
				if p.After != "" {
					after, e = s.parseCursor(p.After, workspace, user, epoch)
					if e != nil || after > seq {
						send("resync_required", map[string]any{"reason": "cursor_expired", "snapshot_path": "/v1/workspaces/" + workspace + "/members"}, "")
						continue
					}
				}
				subscription = store.ID("sub")
				if send("subscribed", map[string]any{"subscription_id": subscription, "high_water_cursor": s.signCursor(workspace, user, epoch, seq), "authorization_epoch": formatRevision(epoch)}, "") != nil {
					return
				}
			case "unsubscribe":
				var p struct {
					Subscription string `json:"subscription_id"`
				}
				if strictFrame(f.Payload, &p) != nil || p.Subscription == "" || p.Subscription != subscription {
					c.Close(websocket.StatusPolicyViolation, "invalid subscription")
					return
				}
				subscription = ""
			case "ack": // Delivery acknowledgement has no domain side effect.
				var p struct {
					Cursor string `json:"cursor"`
				}
				if strictFrame(f.Payload, &p) != nil || len(p.Cursor) > 2048 {
					c.Close(websocket.StatusPolicyViolation, "invalid acknowledgement")
					return
				}
				position, err := s.parseCursor(p.Cursor, workspace, user, epoch)
				if err != nil || position > after || subscription == "" {
					c.Close(websocket.StatusPolicyViolation, "invalid acknowledgement")
					return
				}
			default:
				c.Close(websocket.StatusPolicyViolation, "unsupported frame")
				return
			}
		case <-ticker.C:
			if _, e = s.DB.Authenticate(ctx, token); e != nil {
				c.Close(websocket.StatusPolicyViolation, "session expired")
				return
			}
			_, current, seq, _, e := s.DB.Access(ctx, workspace, user)
			if e != nil || current != epoch {
				c.Close(websocket.StatusPolicyViolation, "access changed")
				return
			}
			if subscription == "" || seq <= after {
				continue
			}
			through := min(after+100, seq)
			events, e := s.events(ctx, user, workspace, after, through)
			if e != nil {
				return
			}
			for i, raw := range events {
				_, fresh, _, _, e := s.DB.Access(ctx, workspace, user)
				if e != nil || fresh != epoch {
					c.Close(websocket.StatusPolicyViolation, "access changed")
					return
				}
				var payload any
				json.Unmarshal(raw, &payload)
				position := after
				if i == len(events)-1 {
					position = through
				}
				if send("event", payload, s.signCursor(workspace, user, epoch, position)) != nil {
					return
				}
			}
			after = through
		case <-ping.C:
			pctx, stop := context.WithTimeout(ctx, 5*time.Second)
			e = c.Ping(pctx)
			stop()
			if e != nil {
				return
			}
		}
	}
}

var frameID = regexp.MustCompile(`^[a-z][a-z0-9_-]{2,127}$`)

func strictFrame(raw []byte, v any) error {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return errors.New("object required")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}
