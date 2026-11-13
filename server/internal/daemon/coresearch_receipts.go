package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"lykeion/server/internal/coresearch"
)

type coreDeliveryError struct{ error }

// The registry lock protects only bookkeeping. Publication and delivery for
// different receipts never wait on each other's disk or network operations.
type coreReceiptFlight struct {
	mu   sync.Mutex
	refs int
}

func (d *daemon) lockCoreReceipt(dir string, wait bool) (func(), bool) {
	d.coreReceiptMu.Lock()
	if d.coreReceiptFlights == nil {
		d.coreReceiptFlights = make(map[string]*coreReceiptFlight)
	}
	flight := d.coreReceiptFlights[dir]
	if flight == nil {
		flight = &coreReceiptFlight{}
		d.coreReceiptFlights[dir] = flight
	}
	flight.refs++
	d.coreReceiptMu.Unlock()
	releaseRef := func() {
		d.coreReceiptMu.Lock()
		flight.refs--
		if flight.refs == 0 {
			delete(d.coreReceiptFlights, dir)
		}
		d.coreReceiptMu.Unlock()
	}
	if wait {
		flight.mu.Lock()
	} else if !flight.mu.TryLock() {
		releaseRef()
		return nil, false
	}
	return func() { flight.mu.Unlock(); releaseRef() }, true
}

// Settlement has its own lifetime: cancellation stops execution, but a bounded
// final delivery still needs to report the outcome. A failed heartbeat never
// authorizes delivery; the finish endpoint checks membership and generation.
func (d *daemon) renewCoreReceipt(parent context.Context, r coreReceipt) func() {
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			attempt, stop := context.WithTimeout(ctx, 5*time.Second)
			_ = d.coreAPI(r.Work.WorkspaceID).Do(attempt, "POST", d.coreWorkPath(r.Work, r.Kind+"/"+r.ID.String()+"/heartbeat"), nil, nil)
			stop()
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return func() { cancel(); <-done }
}

type coreOutputReceipt struct {
	Path string `json:"path"`
	File string `json:"file"`
	SHA  string `json:"sha"`
	Size int64  `json:"size"`
}
type coreReceipt struct {
	Work      coresearch.WorkContext `json:"work"`
	ID        uuid.UUID              `json:"id"`
	Kind      string                 `json:"kind"`
	Body      json.RawMessage        `json:"body,omitempty"`
	Outputs   []coreOutputReceipt    `json:"outputs,omitempty"`
	Rejection string                 `json:"rejection,omitempty"`
}

func (d *daemon) coreWorkPath(w coresearch.WorkContext, path string) string {
	return d.corePath(path) + "&claim_token=" + w.ClaimToken.String()
}
func (d *daemon) coreReceiptDir(r coreReceipt) string {
	return filepath.Join(d.cfg.StateDir, "coresearch-receipts", r.ID.String()+"-"+r.Work.ClaimToken.String())
}
func (d *daemon) journalCoresearch(w coresearch.WorkContext, kind string, id uuid.UUID) error {
	if w.ClaimToken == uuid.Nil || id == uuid.Nil {
		return errors.New("Coresearch claim token required")
	}
	r := coreReceipt{Work: w, Kind: kind, ID: id}
	dir := d.coreReceiptDir(r)
	if err := privateDir(filepath.Dir(dir)); err != nil {
		return err
	}
	if err := atomicJSON(dir, "claim.json", r); err != nil {
		return err
	}
	// No code may start until both the claim and its directory entries are durable.
	return errors.Join(syncDir(filepath.Dir(dir)), syncDir(d.cfg.StateDir))
}
func coreFailure(kind string, err error) map[string]any {
	reason := "execution stopped before a terminal receipt was recorded; code was not replayed"
	if err != nil {
		reason = err.Error()
	}
	switch kind {
	case "cells":
		return map[string]any{"status": "interrupted", "outputs": []map[string]string{{"type": "error", "ename": "RuntimeLost", "evalue": reason}}}
	case "builds":
		return map[string]any{"status": "failed", "reason": reason, "pages": 0}
	case "setups":
		return map[string]any{"status": "failed", "log": reason}
	default:
		return map[string]any{"status": "failed", "error": reason, "outputs": map[string]string{}}
	}
}

// queueCoresearch commits evidence before network I/O. Source snapshots remain
// intact if committing the receipt fails, and are never used to replay code.
func (d *daemon) queueCoresearch(w coresearch.WorkContext, kind string, id uuid.UUID, body any, outputs []recordedOutput) (bool, error) {
	r := coreReceipt{Work: w, Kind: kind, ID: id, Body: mustMarshal(body)}
	stopRenewal := d.renewCoreReceipt(context.Background(), r)
	defer stopRenewal()
	dir := d.coreReceiptDir(r)
	unlock, _ := d.lockCoreReceipt(dir, true)
	defer unlock()
	if err := privateDir(dir); err != nil {
		return false, err
	}
	if old, err := d.readDurableCoreReceipt(filepath.Join(dir, "finish.json")); err == nil {
		same := old.ID == r.ID && old.Kind == r.Kind && bytes.Equal(mustMarshal(old.Work), mustMarshal(r.Work)) && bytes.Equal(old.Body, r.Body) && len(old.Outputs) == len(outputs)
		if same {
			for i, out := range outputs {
				saved := old.Outputs[i]
				if saved.Path != out.path || saved.SHA != out.sha || saved.Size != out.size {
					same = false
					break
				}
			}
		}
		if !same {
			return false, errors.New("conflicting immutable Coresearch receipt")
		}
		if err := d.cleanupBuildScratch(old, dir); err != nil {
			return true, err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return true, d.sendCoreReceipt(ctx, old)
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	for i, out := range outputs {
		name := fmt.Sprintf("output-%08d", i)
		target := filepath.Join(dir, name)
		input, err := coresearch.OpenSnapshot(out.file, out.size, out.sha)
		if err != nil {
			return false, err
		}
		f, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			input.Close()
			return false, err
		}
		_, err = io.Copy(f, input)
		err = errors.Join(err, input.Close(), f.Sync(), f.Close())
		if err != nil {
			return false, err
		}
		r.Outputs = append(r.Outputs, coreOutputReceipt{Path: out.path, File: name, SHA: out.sha, Size: out.size})
	}
	if err := atomicJSON(dir, "finish.json", r); err != nil {
		return false, err
	}
	published, err := d.readDurableCoreReceipt(filepath.Join(dir, "finish.json"))
	if err != nil {
		return false, err
	}
	if !bytes.Equal(mustMarshal(published), mustMarshal(r)) {
		return false, errors.New("Coresearch receipt changed during publication")
	}
	if err := d.cleanupBuildScratch(r, dir); err != nil {
		return true, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return true, d.sendCoreReceipt(ctx, r)
}
func readCoreReceipt(path string) (coreReceipt, error) {
	var r coreReceipt
	f, err := openReportFile(path)
	if err != nil {
		return r, err
	}
	defer f.Close()
	return decodeCoreReceipt(f)
}

func decodeCoreReceipt(f *os.File) (coreReceipt, error) {
	var r coreReceipt
	info, err := f.Stat()
	if err != nil {
		return r, err
	}
	if !info.Mode().IsRegular() || info.Size() > coresearch.MaxCellOutputBytes+(1<<20) {
		return r, errors.New("invalid Coresearch receipt")
	}
	b, err := io.ReadAll(io.LimitReader(f, coresearch.MaxCellOutputBytes+(1<<20)+1))
	if err != nil {
		return r, err
	}
	if len(b) > coresearch.MaxCellOutputBytes+(1<<20) {
		return r, errors.New("invalid Coresearch receipt")
	}
	if err = json.Unmarshal(b, &r); err != nil {
		return r, err
	}
	if r.ID == uuid.Nil || r.Work.ClaimToken == uuid.Nil || r.Work.WorkspaceID == uuid.Nil || (r.Work.ChatID == uuid.Nil && r.Kind != "builds") || r.Work.ProjectID == uuid.Nil || (r.Kind != "cells" && r.Kind != "runs" && r.Kind != "setups" && r.Kind != "builds") {
		return r, errors.New("invalid Coresearch receipt identity")
	}
	for i, out := range r.Outputs {
		if out.File != fmt.Sprintf("output-%08d", i) || out.Size < 0 || out.Size > 50<<20 {
			return r, errors.New("invalid Coresearch output receipt")
		}
	}
	return r, nil
}

func (d *daemon) readDurableCoreReceipt(path string) (coreReceipt, error) {
	syncFile := d.coreReceiptSync
	if syncFile == nil {
		syncFile = (*os.File).Sync
	}
	return readDurableCoreReceipt(path, syncFile)
}

// Retry publication before allowing cleanup or delivery after an ambiguous sync.
// This does not protect against arbitrary replacement of parent directories.
func readDurableCoreReceipt(path string, syncFile func(*os.File) error) (r coreReceipt, result error) {
	f, err := openReportFile(path)
	if err != nil {
		return r, err
	}
	defer f.Close()
	defer func() {
		if errors.Is(result, os.ErrNotExist) {
			result = fmt.Errorf("published Coresearch receipt disappeared: %v", result)
		}
	}()
	r, err = decodeCoreReceipt(f)
	if err != nil {
		return r, err
	}
	if err = syncFile(f); err != nil {
		return r, err
	}
	opened, err := f.Stat()
	if err != nil {
		return r, err
	}
	entry, err := os.Lstat(path)
	if err != nil {
		return r, err
	}
	if !entry.Mode().IsRegular() || !os.SameFile(opened, entry) {
		return r, errors.New("Coresearch receipt replaced during sync")
	}
	dir := filepath.Dir(path)
	for _, p := range []string{dir, filepath.Dir(dir), filepath.Dir(filepath.Dir(dir))} {
		d, err := os.Open(p)
		if err != nil {
			return r, err
		}
		err = errors.Join(syncFile(d), d.Close())
		if err != nil {
			return r, err
		}
	}
	return r, nil
}
func (d *daemon) sendCoreReceipt(ctx context.Context, r coreReceipt) error {
	if r.Rejection != "" {
		return nil
	}
	dir := d.coreReceiptDir(r)
	var err error
	if r.Kind == "runs" || r.Kind == "builds" {
		var facts map[string]any
		if err = json.Unmarshal(r.Body, &facts); err != nil {
			return err
		}
		outputs := make([]recordedOutput, 0, len(r.Outputs))
		for _, out := range r.Outputs {
			outputs = append(outputs, recordedOutput{path: out.Path, sha: out.SHA, file: filepath.Join(dir, out.File), size: out.Size})
		}
		if r.Kind == "builds" {
			err = d.finishBuild(ctx, r, outputs)
		} else {
			err = d.finishRecorded(ctx, coresearch.ClaimedRun{WorkContext: r.Work, ID: r.ID}, facts, outputs)
		}
	} else {
		err = d.coreAPI(r.Work.WorkspaceID).Do(ctx, "POST", d.coreWorkPath(r.Work, r.Kind+"/"+r.ID.String()+"/finish"), r.Body, nil)
	}
	if err != nil {
		if Permanent(err) {
			r.Rejection = err.Error()
			if saveErr := atomicJSON(dir, "finish.json", r); saveErr != nil {
				return errors.Join(err, saveErr)
			}
		}
		return coreDeliveryError{err}
	}
	if err = d.cleanupBuildScratch(r, dir); err != nil {
		return err
	}
	root := filepath.Dir(dir)
	acknowledged := filepath.Join(root, ".ack-"+filepath.Base(dir))
	if err = os.Rename(dir, acknowledged); err != nil {
		return err
	}
	if err = syncDir(root); err != nil {
		return err
	}
	if err = os.RemoveAll(acknowledged); err != nil {
		return err
	}
	return syncDir(root)
}
func (d *daemon) flushCoresearch(ctx context.Context) error {
	root := filepath.Join(d.cfg.StateDir, "coresearch-receipts")
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var workers sync.WaitGroup
	results := make(chan error, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".ack-") {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		workers.Add(1)
		go func() { defer workers.Done(); results <- d.flushCoreReceipt(ctx, dir) }()
	}
	workers.Wait()
	close(results)
	var errs []error
	for err := range results {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}
func (d *daemon) flushCoreReceipt(ctx context.Context, dir string) error {
	if ctx.Err() != nil {
		return nil // Shutdown retains the receipt for the next startup.
	}
	unlock, acquired := d.lockCoreReceipt(dir, false)
	if !acquired {
		return nil
	}
	defer unlock()
	r, err := d.readDurableCoreReceipt(filepath.Join(dir, "finish.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if dir != d.coreReceiptDir(r) {
		return errors.New("Coresearch receipt path mismatch")
	}
	if r.Rejection != "" {
		return nil
	}
	delivery, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	stopRenewal := d.renewCoreReceipt(delivery, r)
	defer stopRenewal()
	err = d.sendCoreReceipt(delivery, r)
	var failed coreDeliveryError
	if errors.As(err, &failed) {
		slog.Warn("Coresearch receipt retained", "work", r.ID, "err", err)
		return nil
	}
	return err
}

// Unpublished directories contain no execution authority. Preserve them outside
// the active scan, without interpreting temporary JSON or preventing unrelated
// published work from recovering. A valid finish receipt always takes priority.
func (d *daemon) quarantineCoresearch(dir string) error {
	root := filepath.Join(d.cfg.StateDir, "coresearch-unpublished")
	if err := privateDir(root); err != nil {
		return err
	}
	if err := syncDir(d.cfg.StateDir); err != nil {
		return err
	}
	if err := os.Rename(dir, filepath.Join(root, filepath.Base(dir)+"-"+uuid.NewString())); err != nil {
		return err
	}
	return errors.Join(syncDir(root), syncDir(filepath.Dir(dir)))
}
func validCoreReceiptName(name string) bool {
	if len(name) != 73 || name[36] != '-' {
		return false
	}
	id, first := uuid.Parse(name[:36])
	token, second := uuid.Parse(name[37:])
	return first == nil && second == nil && id != uuid.Nil && token != uuid.Nil
}
func (d *daemon) recoverCoresearch() error {
	root := filepath.Join(d.cfg.StateDir, "coresearch-receipts")
	if err := privateDir(root); err != nil {
		return err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			return errors.New("unexpected Coresearch receipt entry")
		}
		dir := filepath.Join(root, entry.Name())
		if strings.HasPrefix(entry.Name(), ".ack-") {
			name := strings.TrimPrefix(entry.Name(), ".ack-")
			if !validCoreReceiptName(name) {
				return errors.New("invalid acknowledged receipt")
			}
			if e := os.RemoveAll(dir); e != nil {
				return e
			}
			if e := syncDir(root); e != nil {
				return e
			}
			continue
		}
		if !validCoreReceiptName(entry.Name()) {
			return errors.New("invalid receipt directory")
		}
		if completed, err := d.readDurableCoreReceipt(filepath.Join(dir, "finish.json")); err == nil {
			if d.coreReceiptDir(completed) != dir {
				return errors.New("Coresearch receipt identity mismatch")
			}
			if e := d.cleanupBuildScratch(completed, dir); e != nil {
				return e
			}
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		r, e := readCoreReceipt(filepath.Join(dir, "claim.json"))
		if errors.Is(e, os.ErrNotExist) {
			if e = d.quarantineCoresearch(dir); e != nil {
				return e
			}
			continue
		}
		if e != nil {
			return e
		}
		if d.coreReceiptDir(r) != dir {
			return errors.New("Coresearch journal identity mismatch")
		}
		r.Body = mustMarshal(coreFailure(r.Kind, nil))
		if e = atomicJSON(dir, "finish.json", r); e != nil {
			return e
		}
		published, e := d.readDurableCoreReceipt(filepath.Join(dir, "finish.json"))
		if e != nil {
			return e
		}
		if !bytes.Equal(mustMarshal(published), mustMarshal(r)) {
			return errors.New("Coresearch receipt changed during recovery")
		}
		if e = d.cleanupBuildScratch(r, dir); e != nil {
			return e
		}
	}
	return nil
}
