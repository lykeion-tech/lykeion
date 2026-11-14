package daemon

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"lykeion/server/internal/coresearch"
)

func TestCoresearchRecoveryRetiresUnpublishedJournals(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(map[bool]string{false: "empty", true: "partial-temp"}[partial], func(t *testing.T) {
			f := newCoreFixture(t)
			pending := coreReceipt{Work: f.work, Kind: "cells", ID: uuid.New()}
			finished := coreReceipt{Work: f.work, Kind: "cells", ID: uuid.New(), Body: mustMarshal(map[string]any{"status": "ok", "outputs": []any{}})}
			for _, r := range []coreReceipt{pending, finished} {
				must(t, f.d.journalCoresearch(r.Work, r.Kind, r.ID))
			}
			must(t, atomicJSON(f.d.coreReceiptDir(finished), "finish.json", finished))
			saved, e := os.ReadFile(filepath.Join(f.d.coreReceiptDir(finished), "finish.json"))
			must(t, e)
			orphan := f.d.coreReceiptDir(coreReceipt{Work: f.work, Kind: "cells", ID: uuid.New()})
			must(t, os.MkdirAll(orphan, 0700))
			if partial {
				must(t, os.WriteFile(filepath.Join(orphan, ".claim.json-interrupted.tmp"), []byte(`{"work":`), 0600))
			}
			must(t, f.d.recoverCoresearch())
			if _, e = os.Stat(orphan); !errors.Is(e, os.ErrNotExist) {
				t.Fatal("unpublished directory still active", e)
			}
			quarantined, e := os.ReadDir(filepath.Join(f.d.cfg.StateDir, "coresearch-unpublished"))
			must(t, e)
			if len(quarantined) != 1 {
				t.Fatal("unpublished evidence was not preserved", len(quarantined))
			}
			if partial {
				b, e := os.ReadFile(filepath.Join(f.d.cfg.StateDir, "coresearch-unpublished", quarantined[0].Name(), ".claim.json-interrupted.tmp"))
				must(t, e)
				if string(b) != `{"work":` {
					t.Fatal("partial publication changed", string(b))
				}
			}
			recovered, e := readCoreReceipt(filepath.Join(f.d.coreReceiptDir(pending), "finish.json"))
			must(t, e)
			if !bytes.Contains(recovered.Body, []byte("interrupted")) {
				t.Fatal("valid unfinished work lost", string(recovered.Body))
			}
			got, e := os.ReadFile(filepath.Join(f.d.coreReceiptDir(finished), "finish.json"))
			must(t, e)
			if !bytes.Equal(got, saved) {
				t.Fatal("valid completed receipt changed")
			}
			must(t, f.d.flushCoresearch(t.Context()))
			if f.finishes.Load() != 2 {
				t.Fatal("unrelated valid work did not recover", f.finishes.Load())
			}
		})
	}
}

func TestCoresearchRecoveryPreservesPublishedReceiptWithoutClaim(t *testing.T) {
	f := newCoreFixture(t)
	r := coreReceipt{Work: f.work, Kind: "cells", ID: uuid.New(), Body: mustMarshal(coreFailure("cells", nil))}
	must(t, atomicJSON(f.d.coreReceiptDir(r), "finish.json", r))
	must(t, f.d.recoverCoresearch())
	must(t, f.d.flushCoresearch(t.Context()))
	if f.finishes.Load() != 1 {
		t.Fatal("published receipt discarded")
	}
}

func TestCoresearchBlockedDeliveryDoesNotStarveCompletedCell(t *testing.T) {
	f := coreKernelFixture(t)
	pending := coreReceipt{Work: f.work, Kind: "cells", ID: uuid.New(), Body: mustMarshal(coreFailure("cells", nil))}
	must(t, f.d.journalCoresearch(pending.Work, pending.Kind, pending.ID))
	must(t, atomicJSON(f.d.coreReceiptDir(pending), "finish.json", pending))
	cell := coresearch.ClaimedCell{WorkContext: f.work, ID: uuid.New(), Kernel: "custom", Source: "host"}
	blocked, finishEntered, renewed := make(chan struct{}), make(chan bool, 1), make(chan struct{}, 1)
	releaseBacklog, releaseCell := make(chan struct{}), make(chan struct{})
	var backlogOnce, cellOnce sync.Once
	unblock := func() { backlogOnce.Do(func() { close(releaseBacklog) }); cellOnce.Do(func() { close(releaseCell) }) }
	var finishing atomic.Bool
	original := http.DefaultTransport
	f.d.api.HTTP = &http.Client{Transport: coreRoundTrip(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, pending.ID.String()) && strings.HasSuffix(r.URL.Path, "/finish") {
			close(blocked)
			select {
			case <-releaseBacklog:
			case <-r.Context().Done():
				return nil, r.Context().Err()
			}
		}
		if strings.Contains(r.URL.Path, cell.ID.String()) {
			if strings.HasSuffix(r.URL.Path, "/heartbeat") && finishing.Load() {
				select {
				case renewed <- struct{}{}:
				default:
				}
			}
			if strings.HasSuffix(r.URL.Path, "/finish") {
				_, err := readCoreReceipt(filepath.Join(f.d.coreReceiptDir(coreReceipt{Work: f.work, ID: cell.ID}), "finish.json"))
				finishing.Store(true)
				finishEntered <- err == nil
				select {
				case <-releaseCell:
				case <-r.Context().Done():
					return nil, r.Context().Err()
				}
			}
		}
		return original.RoundTrip(r)
	})}
	ctx, cancel := context.WithCancel(t.Context())
	flushDone, cellDone := make(chan error, 1), make(chan error, 1)
	go func() { flushDone <- f.d.flushCoresearch(ctx) }()
	select {
	case <-blocked:
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("backlog never entered delivery")
	}
	go func() { cellDone <- f.d.runCell(ctx, cell) }()
	defer func() { unblock(); cancel(); <-flushDone; <-cellDone }()
	select {
	case durable := <-finishEntered:
		if !durable {
			t.Fatal("cell reached network before durable persistence")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("unrelated blocked delivery prevented cell persistence/settlement")
	}
	select {
	case <-renewed:
	case <-time.After(3 * time.Second):
		t.Fatal("completed cell stopped lease renewal before settlement")
	}
	// A second sweep must skip both in-flight receipts, without duplicate sends
	// or waiting for either network request.
	again := make(chan error, 1)
	go func() { again <- f.d.flushCoresearch(ctx) }()
	select {
	case err := <-again:
		must(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("concurrent sweep waited on in-flight delivery")
	}
	cellOnce.Do(func() { close(releaseCell) })
	// A remains blocked while B must receive its acknowledgement and cleanup.
	select {
	case err := <-cellDone:
		must(t, err)
		cellDone <- nil
	case <-time.After(3 * time.Second):
		t.Fatal("cell waits for unrelated backlog")
	}
	if _, e := os.Stat(f.d.coreReceiptDir(coreReceipt{Work: f.work, ID: cell.ID})); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("acknowledged receipt retained", e)
	}
}

func TestCoresearchEveryReceiptKindRenewsThroughDelivery(t *testing.T) {
	for _, kind := range []string{"cells", "runs", "setups"} {
		t.Run(kind, func(t *testing.T) {
			f := newCoreFixture(t)
			id := uuid.New()
			must(t, f.d.journalCoresearch(f.work, kind, id))
			entered, renewed, release := make(chan struct{}), make(chan struct{}, 1), make(chan struct{})
			var once sync.Once
			original := http.DefaultTransport
			f.d.api.HTTP = &http.Client{Transport: coreRoundTrip(func(r *http.Request) (*http.Response, error) {
				if strings.HasSuffix(r.URL.Path, "/heartbeat") {
					// Hold the first renewal until delivery enters. This tests overlap,
					// independently of the production renewal interval.
					select {
					case <-entered:
					case <-r.Context().Done():
						return nil, r.Context().Err()
					}
					select {
					case renewed <- struct{}{}:
					default:
					}
				}
				if strings.HasSuffix(r.URL.Path, "/finish") {
					close(entered)
					select {
					case <-release:
					case <-r.Context().Done():
						return nil, r.Context().Err()
					}
				}
				return original.RoundTrip(r)
			})}
			done := make(chan error, 1)
			go func() { _, e := f.d.queueCoresearch(f.work, kind, id, coreFailure(kind, nil), nil); done <- e }()
			defer func() { once.Do(func() { close(release) }); must(t, <-done) }()
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("no terminal delivery")
			}
			select {
			case <-renewed:
			case <-time.After(3 * time.Second):
				t.Fatal("terminal delivery is not renewing its lease")
			}
		})
	}
}

func TestCoresearchCancelledReceiptSweepPreservesEvidence(t *testing.T) {
	f := newCoreFixture(t)
	r := coreReceipt{Work: f.work, Kind: "cells", ID: uuid.New(), Body: mustMarshal(coreFailure("cells", nil))}
	must(t, f.d.journalCoresearch(r.Work, r.Kind, r.ID))
	must(t, atomicJSON(f.d.coreReceiptDir(r), "finish.json", r))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	must(t, f.d.flushCoresearch(ctx))
	if f.finishes.Load() != 0 {
		t.Fatal("cancelled sweep delivered work")
	}
	_, err := readCoreReceipt(filepath.Join(f.d.coreReceiptDir(r), "finish.json"))
	must(t, err)
	// Next startup can still acknowledge the same immutable terminal result.
	must(t, f.d.recoverCoresearch())
	must(t, f.d.flushCoresearch(t.Context()))
	if f.finishes.Load() != 1 {
		t.Fatal("cancelled sweep lost its receipt")
	}
	f.d.coreReceiptMu.Lock()
	defer f.d.coreReceiptMu.Unlock()
	if len(f.d.coreReceiptFlights) != 0 {
		t.Fatal("completed deliveries retained coordination state")
	}
}

func TestCoresearchReceiptDurabilityRetry(t *testing.T) {
	w := coresearch.WorkContext{WorkspaceID: uuid.New(), ProjectID: uuid.New(), ChatID: uuid.New(), ClaimToken: uuid.New()}
	r := coreReceipt{Work: w, Kind: "cells", ID: uuid.New(), Body: mustMarshal(coreFailure("cells", nil))}
	dir := filepath.Join(t.TempDir(), "root", "receipt")
	must(t, atomicJSON(dir, "finish.json", r))
	path := filepath.Join(dir, "finish.json")
	for _, failAt := range []int{1, 2, 3, 4} {
		calls := 0
		_, err := readDurableCoreReceipt(path, func(file *os.File) error {
			calls++
			if calls == failAt {
				return errors.New("injected sync failure")
			}
			return file.Sync()
		})
		if err == nil || calls != failAt {
			t.Fatalf("sync %d: calls=%d err=%v", failAt, calls, err)
		}
	}
	got, err := readDurableCoreReceipt(path, (*os.File).Sync)
	must(t, err)
	if got.ID != r.ID || !bytes.Equal(got.Body, r.Body) {
		t.Fatal("receipt changed during retry")
	}
}

func TestCoresearchReceiptSyncFailurePreservesScratchAndDefersDelivery(t *testing.T) {
	for _, operation := range []string{"queue", "queue-new", "flush", "recover", "flush-output", "recover-output"} {
		t.Run(operation, func(t *testing.T) {
			f := newCoreFixture(t)
			body := map[string]any{"status": "failed", "pages": 0}
			r := coreReceipt{Work: f.work, Kind: "builds", ID: uuid.New(), Body: mustMarshal(body)}
			must(t, f.d.journalCoresearch(r.Work, r.Kind, r.ID))
			scratch := buildScratchFixture(t, f.d, r)
			var output string
			if strings.HasSuffix(operation, "-output") {
				data := []byte("durable build output")
				output = filepath.Join(f.d.coreReceiptDir(r), "output-00000000")
				must(t, os.WriteFile(output, data, 0600))
				r.Outputs = []coreOutputReceipt{{Path: "pdf", File: "output-00000000", Size: int64(len(data)), SHA: fmt.Sprintf("%x", sha256.Sum256(data))}}
			}
			if operation != "queue-new" {
				must(t, atomicJSON(f.d.coreReceiptDir(r), "finish.json", r))
			}
			var calls int
			f.d.coreReceiptSync = func(file *os.File) error {
				calls++
				if calls == 2 {
					return errors.New("injected directory sync failure")
				}
				return file.Sync()
			}
			invoke := func() error {
				switch operation {
				case "queue", "queue-new":
					_, err := f.d.queueCoresearch(r.Work, r.Kind, r.ID, body, nil)
					return err
				case "flush", "flush-output":
					return f.d.flushCoresearch(t.Context())
				default:
					return f.d.recoverCoresearch()
				}
			}
			if err := invoke(); err == nil {
				t.Fatal("sync failure accepted")
			}
			if _, err := os.Stat(scratch); err != nil {
				t.Fatalf("scratch lost: %v", err)
			}
			if f.finishes.Load() != 0 {
				t.Fatal("finish sent before durable receipt")
			}
			if output != "" {
				if _, err := os.Stat(output); err != nil {
					t.Fatalf("output lost: %v", err)
				}
			}
			f.d.coreReceiptSync = nil
			must(t, invoke())
			if strings.HasPrefix(operation, "recover") {
				must(t, f.d.flushCoresearch(t.Context()))
			}
			if _, err := os.Stat(scratch); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("scratch retained after retry: %v", err)
			}
			if f.finishes.Load() != 1 {
				t.Fatalf("finish count after retry: %d", f.finishes.Load())
			}
		})
	}
}

func TestCoresearchReceiptReplacementDuringSync(t *testing.T) {
	w := coresearch.WorkContext{WorkspaceID: uuid.New(), ProjectID: uuid.New(), ChatID: uuid.New(), ClaimToken: uuid.New()}
	r := coreReceipt{Work: w, Kind: "cells", ID: uuid.New(), Body: mustMarshal(coreFailure("cells", nil))}
	dir := filepath.Join(t.TempDir(), "root", "receipt")
	must(t, atomicJSON(dir, "finish.json", r))
	replacement := filepath.Join(dir, "replacement")
	must(t, os.WriteFile(replacement, mustMarshal(r), 0600))
	_, err := readDurableCoreReceipt(filepath.Join(dir, "finish.json"), func(f *os.File) error {
		must(t, f.Sync())
		return os.Rename(replacement, filepath.Join(dir, "finish.json"))
	})
	if err == nil {
		t.Fatal("replaced receipt accepted")
	}
}

func TestRejectedCoresearchReceiptRequiresSync(t *testing.T) {
	f := newCoreFixture(t)
	r := coreReceipt{Work: f.work, Kind: "cells", ID: uuid.New(), Body: mustMarshal(coreFailure("cells", nil)), Rejection: "rejected"}
	must(t, atomicJSON(f.d.coreReceiptDir(r), "finish.json", r))
	f.d.coreReceiptSync = func(*os.File) error { return errors.New("injected failure") }
	if err := f.d.flushCoresearch(t.Context()); err == nil {
		t.Fatal("rejected receipt skipped durability")
	}
	f.d.coreReceiptSync = nil
	must(t, f.d.flushCoresearch(t.Context()))
	if f.finishes.Load() != 0 {
		t.Fatal("rejected receipt delivered")
	}
	if _, err := os.Stat(filepath.Join(f.d.coreReceiptDir(r), "finish.json")); err != nil {
		t.Fatal(err)
	}
}
