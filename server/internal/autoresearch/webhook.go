package autoresearch

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/seal"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

// WebhookPrefix starts every webhook URL token, so a leaked one is easy to
// recognise.
const WebhookPrefix = "lwh_"

const maxDeliveryAttempts = 5

var (
	ErrNoWebhook    = errors.New("webhook not found")
	ErrBadSignature = errors.New("the X-Hub-Signature-256 signature is missing or wrong")
	ErrBadPayload   = errors.New("the body must be a JSON object or array")
)

// Filter limits a webhook trigger to one event, and to some of its actions
// when Actions is not empty.
type Filter struct {
	Event   string   `json:"event"`
	Actions []string `json:"actions,omitempty"`
}

// ParseFilters checks a trigger's filters. Empty input means none.
func ParseFilters(raw json.RawMessage) ([]Filter, error) {
	fs := []Filter{}
	if len(raw) == 0 || string(raw) == "null" {
		return fs, nil
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&fs); err != nil {
		return nil, fmt.Errorf("filters must be a list of {event, actions}: %v", err)
	}
	if len(fs) > 20 {
		return nil, errors.New("at most 20 filters")
	}
	for i, f := range fs {
		if strings.TrimSpace(f.Event) == "" || len(f.Event) > 200 {
			return nil, fmt.Errorf("filters[%d].event must be 1 to 200 bytes", i)
		}
		for j, a := range f.Actions {
			if strings.TrimSpace(a) == "" || len(a) > 200 {
				return nil, fmt.Errorf("filters[%d].actions[%d] must be 1 to 200 bytes", i, j)
			}
		}
	}
	return fs, nil
}

// Envelope is a webhook request in one shape, whatever sent it. Payload is
// the body as received; it is data, never instructions.
type Envelope struct {
	Provider   string          `json:"provider"`
	Event      string          `json:"event"`
	Action     string          `json:"action,omitempty"`
	ReceivedAt time.Time       `json:"received_at"`
	Payload    json.RawMessage `json:"payload"`
}

// Receipt is the webhook's answer: accepted, skipped or ignored.
type Receipt struct {
	Status     string     `json:"status"`
	Reason     string     `json:"reason,omitempty"`
	DeliveryID *uuid.UUID `json:"delivery_id,omitempty"`
}

func clip(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 200 {
		s = strings.ToValidUTF8(s[:200], "")
	}
	return s
}

// normalise reads the body as a JSON object or array and names its event:
// GitHub's event header for a github trigger, else X-Event-Type, else the
// body's event or type field. The action is the body's action field.
func normalise(provider string, h http.Header, body []byte, now time.Time) (Envelope, error) {
	body = bytes.TrimPrefix(body, []byte("\xef\xbb\xbf"))
	// Postgres cannot store a NUL character in jsonb.
	if bytes.Contains(body, []byte(`\u0000`)) {
		return Envelope{}, fmt.Errorf(`%w: it contains \u0000`, ErrBadPayload)
	}
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		return Envelope{}, fmt.Errorf("%w: %v", ErrBadPayload, err)
	}
	obj, isObject := v.(map[string]any)
	if _, isList := v.([]any); !isObject && !isList {
		return Envelope{}, ErrBadPayload
	}
	field := func(k string) string {
		s, _ := obj[k].(string)
		return clip(s)
	}
	e := Envelope{Provider: provider, Action: field("action"), ReceivedAt: now.UTC(), Payload: body}
	switch {
	case provider == "github" && h.Get("X-GitHub-Event") != "":
		e.Event = clip(h.Get("X-GitHub-Event"))
	case h.Get("X-Event-Type") != "":
		e.Event = clip(h.Get("X-Event-Type"))
	case field("event") != "":
		e.Event = field("event")
	case field("type") != "":
		e.Event = field("type")
	default:
		e.Event = "webhook.received"
	}
	return e, nil
}

// matches reports whether a delivery passes the trigger's filters. No
// filters pass everything.
func matches(fs []Filter, e Envelope) bool {
	if len(fs) == 0 {
		return true
	}
	for _, f := range fs {
		if f.Event == e.Event && (len(f.Actions) == 0 || slices.Contains(f.Actions, e.Action)) {
			return true
		}
	}
	return false
}

// deliveryKey is what makes a redelivery a duplicate: GitHub's delivery id
// for a github trigger, else the Idempotency-Key header. A request with
// neither gets a key of its own, so it is never taken for a duplicate.
func deliveryKey(provider string, h http.Header) (string, error) {
	k := strings.TrimSpace(h.Get("Idempotency-Key"))
	if g := strings.TrimSpace(h.Get("X-GitHub-Delivery")); provider == "github" && g != "" {
		k = g
	}
	if len(k) > 200 {
		return "", fmt.Errorf("%w: the delivery key is longer than 200 bytes", ErrBadPayload)
	}
	if k == "" {
		return "auto:" + uuid.NewString(), nil
	}
	return "key:" + k, nil
}

// validHubSignature checks GitHub's X-Hub-Signature-256 header,
// "sha256=<hex HMAC-SHA256 of the body>", in constant time.
func validHubSignature(secret []byte, header string, body []byte) bool {
	hexsig, ok := strings.CutPrefix(header, "sha256=")
	if !ok {
		return false
	}
	got, err := hex.DecodeString(hexsig)
	if err != nil {
		return false
	}
	m := hmac.New(sha256.New, secret)
	m.Write(body)
	return hmac.Equal(m.Sum(nil), got)
}

// Receive handles one webhook request. A signed trigger's signature is checked
// first, and with no key to open its secret the request fails closed. A
// paused Autoresearch or a disabled trigger ignores it; a filtered or repeated
// one is skipped; anything else is stored for the delivery worker.
func (s *Service) Receive(ctx context.Context, tokenHash string, h http.Header, body []byte) (Receipt, error) {
	ref, err := s.Store.Global().AutoresearchWebhookLookup(ctx, tokenHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return Receipt{}, ErrNoWebhook
	}
	if err != nil {
		return Receipt{}, err
	}
	var out Receipt
	err = s.Store.InWorkspace(ctx, ref.WorkspaceID, uuid.Nil, func(tx *store.Tx) error {
		if err := lockWorkspace(ctx, tx); err != nil {
			return err
		}
		t, err := tx.Q.LockTrigger(ctx, db.LockTriggerParams{WorkspaceID: ref.WorkspaceID, ID: ref.TriggerID})
		if err != nil {
			return err
		}
		if t.SigningSecretEnc != nil {
			secret, err := seal.Open(s.Key, t.SigningSecretEnc, t.ID[:])
			if err != nil {
				return fmt.Errorf("%w (%v)", seal.ErrNoKey, err)
			}
			if !validHubSignature(secret, h.Get("X-Hub-Signature-256"), body) {
				return ErrBadSignature
			}
		}
		ap, err := tx.Q.GetAutoresearch(ctx, db.GetAutoresearchParams{WorkspaceID: ref.WorkspaceID, ID: t.AutoresearchID})
		if err != nil {
			return err
		}
		switch {
		case ap.Status != "active":
			out = Receipt{Status: "ignored", Reason: "paused"}
			return nil
		case !t.Enabled:
			out = Receipt{Status: "ignored", Reason: "trigger_disabled"}
			return nil
		}
		env, err := normalise(t.Provider, h, body, s.now())
		if err != nil {
			return err
		}
		fs, err := ParseFilters(t.Filters)
		if err != nil {
			return err
		}
		if !matches(fs, env) {
			out = Receipt{Status: "skipped", Reason: "filtered"}
			return nil
		}
		key, err := deliveryKey(t.Provider, h)
		if err != nil {
			return err
		}
		raw, err := json.Marshal(env)
		if err != nil {
			return err
		}
		d, err := tx.Q.InsertDelivery(ctx, db.InsertDeliveryParams{WorkspaceID: tx.WS, TriggerID: t.ID, DeliveryKey: key, Envelope: raw})
		if errors.Is(err, pgx.ErrNoRows) {
			out = Receipt{Status: "skipped", Reason: "duplicate"}
			return nil
		}
		if err != nil {
			return err
		}
		out = Receipt{Status: "accepted", DeliveryID: &d.ID}
		return tx.Emit(ctx, "webhook_delivery.received", nil, store.System,
			map[string]any{"delivery_id": d.ID, "trigger_id": t.ID, "autoresearch_id": ap.ID, "event": env.Event})
	})
	return out, err
}

// Deliver dispatches stored webhook deliveries until none is ready. Workers on
// several servers may run at once: each delivery is leased with SKIP LOCKED,
// and the lease of a worker that dies lapses after a minute.
func (s *Service) Deliver(ctx context.Context) error {
	for {
		batch, err := s.Store.Global().ClaimWebhookDeliveries(ctx, 10)
		if err != nil || len(batch) == 0 {
			return err
		}
		for _, d := range batch {
			if err := s.deliver(ctx, d.WorkspaceID, d.DeliveryID); err != nil {
				slog.Error("webhook delivery", "delivery", d.DeliveryID, "err", err)
				if err := s.Store.InWorkspace(ctx, d.WorkspaceID, uuid.Nil, func(tx *store.Tx) error {
					if err := lockWorkspace(ctx, tx); err != nil {
						return err
					}
					return tx.Q.ReleaseDelivery(ctx, db.ReleaseDeliveryParams{WorkspaceID: d.WorkspaceID, ID: d.DeliveryID})
				}); err != nil {
					slog.Error("webhook delivery release", "delivery", d.DeliveryID, "err", err)
				}
			}
		}
	}
}

// deliver turns one leased delivery into its run, once, and dispatches it. A
// delivery past its attempts is failed instead.
func (s *Service) deliver(ctx context.Context, ws, id uuid.UUID) error {
	var run *db.AutoresearchRun
	err := s.Store.InWorkspace(ctx, ws, uuid.Nil, func(tx *store.Tx) error {
		if err := lockWorkspace(ctx, tx); err != nil {
			return err
		}
		d, err := tx.Q.LockDelivery(ctx, db.LockDeliveryParams{WorkspaceID: ws, ID: id})
		if err != nil {
			return err
		}
		if d.State != "dispatching" {
			return nil
		}
		if d.Attempts > maxDeliveryAttempts {
			if err := tx.Q.FinishDelivery(ctx, db.FinishDeliveryParams{State: "failed", WorkspaceID: ws, ID: d.ID}); err != nil {
				return err
			}
			return tx.Emit(ctx, "webhook_delivery.failed", nil, store.System, map[string]any{"delivery_id": d.ID, "attempts": d.Attempts})
		}
		t, err := tx.Q.GetTrigger(ctx, db.GetTriggerParams{WorkspaceID: ws, ID: d.TriggerID})
		if err != nil {
			return err
		}
		r, _, err := s.Admit(ctx, tx, Admission{Autoresearch: t.AutoresearchID, Trigger: &t.ID, Source: "webhook", Actor: t.CreatedBy,
			PlannedAt: d.ReceivedAt, Key: "webhook:" + t.ID.String() + ":" + d.DeliveryKey, Payload: d.Envelope})
		if err != nil {
			return err
		}
		if err := tx.Q.FinishDelivery(ctx, db.FinishDeliveryParams{State: "dispatched", RunID: &r.ID, WorkspaceID: ws, ID: d.ID}); err != nil {
			return err
		}
		run = &r
		return tx.Emit(ctx, "webhook_delivery.dispatched", nil, store.System, map[string]any{"delivery_id": d.ID, "run_id": r.ID})
	})
	if err != nil || run == nil {
		return err
	}
	// A dispatch that fails leaves the run queued, and the tick repairs it.
	if err := s.Dispatch(ctx, ws, run.ID); err != nil {
		slog.Error("autoresearch dispatch", "run", run.ID, "err", err)
	}
	return nil
}
