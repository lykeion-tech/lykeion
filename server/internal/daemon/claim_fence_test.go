package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"lykeion/server/internal/apiclient"
)

// An empty active map is not a drain fence: a claim can already be assigned
// by the server while its HTTP response is still in flight.
func TestActiveCountMissesInflightClaim(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	assigned, release := make(chan struct{}), make(chan struct{})
	runID, workspaceID, runtimeID := uuid.New(), uuid.New(), uuid.New()
	var response claimed
	response.AgentRun.ID, response.AgentRun.WorkspaceID = runID, workspaceID
	response.AgentRun.RuntimeID, response.AgentRun.Attempt = &runtimeID, 1
	response.Token = "ltk_test"
	transport := coreRoundTrip(func(r *http.Request) (*http.Response, error) {
		close(assigned)
		select {
		case <-release:
			body, err := json.Marshal([]claimed{response})
			if err != nil {
				return nil, err
			}
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(body)), Header: make(http.Header), Request: r}, nil
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
	})
	state := t.TempDir()
	d := &daemon{cfg: Config{StateDir: state, Home: t.TempDir()}, api: &apiclient.Client{Server: "https://example.invalid", HTTP: &http.Client{Transport: transport}}, active: map[string]bool{}, failures: make(chan error, 1), wake: make(chan struct{}, 1)}
	d.out = &Outbox{Dir: filepath.Join(state, "outbox")}
	claimDone := make(chan struct{})
	go func() {
		defer close(claimDone)
		d.claimTurns(ctx, []runtimeRef{{WorkspaceID: workspaceID, RuntimeID: runtimeID}}, 1, false)
	}()
	select {
	case <-assigned:
	case <-ctx.Done():
		t.Fatal("claim did not arrive")
	}
	d.mu.Lock()
	count := len(d.active)
	d.mu.Unlock()
	if count != 0 {
		t.Fatalf("unexpected active count while assigned response blocked: %d", count)
	}
	close(release)
	select {
	case <-claimDone:
	case <-ctx.Done():
		t.Fatal("claim did not finish")
	}
	d.wg.Wait()
	select {
	case err := <-d.failures:
		t.Fatalf("run settlement failed: %v", err)
	default:
	}
	report, err := readReport(d.out.file(Report{AgentRunID: runID, Attempt: 1}))
	if err != nil || report.Kind != "fail" || report.AgentRunID != runID {
		t.Fatalf("missing durable local failure report: %+v, %v", report, err)
	}
}

func TestQuotaPauseStopsLaterClaimsInSamePoll(t *testing.T) {
	runtime := uuid.New()
	d := &daemon{cfg: Config{Slots: 1}, refs: []runtimeRef{{WorkspaceID: uuid.New(), RuntimeID: runtime}}, harnessOf: map[uuid.UUID]string{runtime: "claude"}}
	calls := 0
	d.api = &apiclient.Client{Server: "https://example.invalid", HTTP: &http.Client{Transport: coreRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		body := "[]"
		if strings.HasSuffix(r.URL.Path, "/coresearch/claim") {
			body = "{}"
			d.pauseQuota("claude")
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: r}, nil
	})}}
	d.claim(t.Context())
	if calls != 1 {
		t.Fatalf("%d claims: poll reused references after quota pause", calls)
	}
}
