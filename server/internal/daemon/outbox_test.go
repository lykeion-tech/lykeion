package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"lykeion/server/internal/apiclient"
)

func reportFixture() Report {
	return Report{AgentRunID: uuid.New(), WorkspaceID: uuid.New(), RuntimeID: uuid.New(), Attempt: 1, Kind: "complete"}
}
func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func TestOutboxRecoveryAndAttemptSeparation(t *testing.T) {
	dir := t.TempDir()
	r := reportFixture()
	ob := &Outbox{Dir: dir, Send: func(context.Context, Report) error { return errors.New("offline") }}
	must(t, ob.Put(r))
	info, err := os.Stat(ob.file(r))
	must(t, err)
	if info.Mode().Perm() != 0600 {
		t.Fatal(info.Mode())
	}
	second := r
	second.Attempt = 2
	must(t, ob.Put(second))
	must(t, ob.Flush(context.Background()))
	entries, err := os.ReadDir(dir)
	must(t, err)
	if len(entries) != 2 {
		t.Fatalf("lost pending attempt: %v", entries)
	}
	raw, err := os.ReadFile(ob.file(r))
	must(t, err)
	must(t, os.Remove(ob.file(r)))
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(ob.file(r))+"-*.tmp")
	must(t, err)
	_, err = tmp.Write(raw)
	must(t, err)
	must(t, tmp.Close())
	var got []int32
	restart := &Outbox{Dir: dir, Send: func(_ context.Context, r Report) error { got = append(got, r.Attempt); return nil }}
	must(t, restart.Flush(context.Background()))
	if len(got) != 2 {
		t.Fatalf("recovered %v", got)
	}
	entries, err = os.ReadDir(dir)
	must(t, err)
	if len(entries) != 0 {
		t.Fatalf("retained ACKs %v", entries)
	}
}
func TestOutboxImmutableSerializedAndCorruptEvidence(t *testing.T) {
	ob := &Outbox{Dir: t.TempDir(), Send: func(context.Context, Report) error { return nil }}
	r := reportFixture()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := ob.Put(r); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	changed := r
	changed.Error = "different"
	if ob.Put(changed) == nil {
		t.Fatal("overwrote durable result")
	}
	must(t, os.WriteFile(filepath.Join(ob.Dir, ".corrupt.tmp"), []byte("{"), 0600))
	if ob.Flush(context.Background()) == nil {
		t.Fatal("hid corrupt recovery data")
	}
	if _, err := os.Stat(filepath.Join(ob.Dir, ".corrupt.tmp")); err != nil {
		t.Fatal("lost evidence", err)
	}
}
func TestOutboxRejectionAge(t *testing.T) {
	r := reportFixture()
	ob := &Outbox{Dir: t.TempDir(), Send: func(context.Context, Report) error { return &apiclient.HTTPError{Status: 404, Msg: "not found"} }}
	must(t, ob.Put(r))
	for i := 0; i < 3; i++ {
		must(t, ob.Flush(context.Background()))
	}
	b, err := os.ReadFile(ob.file(r))
	must(t, err)
	var saved Report
	must(t, json.Unmarshal(b, &saved))
	if len(saved.Rejections) != 3 {
		t.Fatal(saved)
	}
	saved.Rejections[0] = time.Now().Add(-11 * time.Minute)
	must(t, atomicJSON(ob.Dir, filepath.Base(ob.file(r)), saved))
	must(t, ob.Flush(context.Background()))
	if _, err := os.Stat(filepath.Join(ob.Dir, "quarantine", filepath.Base(ob.file(r)))); err != nil {
		t.Fatal(err)
	}
}

func TestPermanentLeavesRecoverableFailuresPending(t *testing.T) {
	for _, status := range []int{401, 408, 429, 500, 503} {
		if Permanent(&apiclient.HTTPError{Status: status}) {
			t.Fatalf("transient %d classified permanent", status)
		}
	}
	if Permanent(errors.New("network")) {
		t.Fatal("network permanent")
	}
}

func TestOutboxRepeatedEncodedMessageIsIdentical(t *testing.T) {
	r := reportFixture()
	r.Kind = "messages"
	r.Messages = []wireMsg{{Seq: 1, Type: "text", Content: json.RawMessage(`"<tag>"`)}}
	ob := &Outbox{Dir: t.TempDir()}
	must(t, ob.Put(r))
	must(t, ob.Put(r))
}
func TestOutboxCannotOverwriteQuarantine(t *testing.T) {
	r := reportFixture()
	ob := &Outbox{Dir: t.TempDir(), Send: func(context.Context, Report) error { return &apiclient.HTTPError{Status: 409} }}
	r.Rejections = []time.Time{time.Now().Add(-11 * time.Minute), time.Now().Add(-10 * time.Minute)}
	must(t, ob.Put(r))
	must(t, ob.Flush(context.Background()))
	changed := r
	changed.Error = "different evidence"
	if err := ob.Put(changed); err == nil {
		t.Fatal("accepted conflicting quarantined attempt")
	}
}

func TestOutboxQuarantineCollisionPreservesBothPayloads(t *testing.T) {
	r := reportFixture()
	r.Rejections = []time.Time{time.Now().Add(-11 * time.Minute), time.Now().Add(-10 * time.Minute)}
	ob := &Outbox{Dir: t.TempDir(), Send: func(context.Context, Report) error { return &apiclient.HTTPError{Status: 409} }}
	must(t, ob.Put(r))
	other := r
	other.Error = "earlier quarantine"
	q := filepath.Join(ob.Dir, "quarantine")
	must(t, atomicJSON(q, filepath.Base(ob.file(r)), other))
	if err := ob.Flush(context.Background()); err == nil {
		t.Fatal("silently overwrote quarantine collision")
	}
	saved, err := readReport(filepath.Join(q, filepath.Base(ob.file(r))))
	must(t, err)
	if saved.Error != other.Error {
		t.Fatal("lost existing evidence")
	}
	if _, err := os.Stat(ob.file(r)); err != nil {
		t.Fatal("lost pending evidence", err)
	}
}

func TestAttemptJournalWritesShareDaemonLock(t *testing.T) {
	d := &daemon{cfg: Config{StateDir: t.TempDir()}}
	r := reportFixture()
	d.attemptMu.Lock()
	done := make(chan error, 1)
	go func() { done <- d.journal().Put(r) }()
	select {
	case err := <-done:
		t.Fatalf("attempt write bypassed owner lock: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	d.attemptMu.Unlock()
	must(t, <-done)
	d.attemptMu.Lock()
	go func() { done <- d.forgetAttempt(r) }()
	select {
	case err := <-done:
		t.Fatalf("attempt removal bypassed owner lock: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	d.attemptMu.Unlock()
	must(t, <-done)
}
