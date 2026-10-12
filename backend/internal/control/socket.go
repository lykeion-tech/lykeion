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
	lease, e := s.openLease(r.Context(), workspace, user, token, epoch)
	if e != nil {
		if errors.Is(e, errConnectionLimit) {
			w.Header().Set("Retry-After", "30")
			problem(w, 429, "CONNECTION_LIMIT")
		} else {
			problem(w, 503, "STORE_UNAVAILABLE")
		}
		return
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		s.DB.Pool.Exec(ctx, "DELETE FROM realtime_leases WHERE id=$1", lease)
	}()
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
		windowStart := time.Now()
		count := 0
		for {
			kind, raw, e := c.Read(ctx)
			if e != nil {
				select {
				case errs <- e:
				default:
				}
				return
			}
			if time.Since(windowStart) >= 10*time.Second {
				windowStart = time.Now()
				count = 0
			}
			count++
			if count > 60 {
				select {
				case errs <- errors.New("frame rate exceeded"):
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
	hints, unlisten := s.hints.subscribe()
	defer unlisten()
	presenceEnabled := false
	lastRenew := time.Now()
	lastPresence := time.Time{}
	var previousPresence json.RawMessage
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
			case "presence.update":
				var p struct {
					Subject ref    `json:"subject_ref"`
					State   string `json:"state"`
				}
				if subscription == "" || strictFrame(f.Payload, &p) != nil || p.Subject.Kind != "workspace" || p.Subject.ID != workspace || p.Subject.Revision != "1" || p.State != "viewing" {
					c.Close(websocket.StatusPolicyViolation, "unsupported presence")
					return
				}
				presenceEnabled = true
				if _, e = s.DB.Pool.Exec(ctx, "UPDATE realtime_leases SET presence_until=clock_timestamp()+interval '30 seconds' WHERE id=$1", lease); e != nil {
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
				s.DB.Pool.Exec(ctx, "UPDATE realtime_leases SET presence_until=NULL WHERE id=$1", lease)
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
		case <-hints:
			// Wake the same bounded catch-up path; an empty notification has no authority.
			ticker.Reset(time.Millisecond)
		case <-ticker.C:
			ticker.Reset(250 * time.Millisecond)
			if _, e = s.DB.Authenticate(ctx, token); e != nil {
				c.Close(websocket.StatusPolicyViolation, "session expired")
				return
			}
			_, current, seq, _, e := s.DB.Access(ctx, workspace, user)
			if e != nil || current != epoch {
				c.Close(websocket.StatusPolicyViolation, "access changed")
				return
			}
			if time.Since(lastRenew) >= 10*time.Second {
				if _, e = s.DB.Pool.Exec(ctx, "UPDATE realtime_leases SET expires_at=clock_timestamp()+interval '30 seconds' WHERE id=$1", lease); e != nil {
					return
				}
				lastRenew = time.Now()
			}
			if presenceEnabled && subscription != "" && time.Since(lastPresence) >= time.Second {
				raw, err := s.presenceSnapshot(ctx, workspace, epoch)
				if err != nil {
					return
				}
				if !bytes.Equal(raw, previousPresence) {
					if send("presence.snapshot", raw, "") != nil {
						return
					}
					previousPresence = raw
				}
				lastPresence = time.Now()
			}
			if subscription == "" || seq <= after {
				continue
			}
			if seq-after > 500 {
				send("resync_required", map[string]any{"reason": "slow_consumer", "snapshot_path": "/v1/workspaces/" + workspace + "/members"}, "")
				c.Close(websocket.StatusTryAgainLater, "snapshot required")
				return
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

var errConnectionLimit = errors.New("connection limit")

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
