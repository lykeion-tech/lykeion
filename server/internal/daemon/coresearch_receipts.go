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
	"time"

	"github.com/google/uuid"
	"lykeion/server/internal/coresearch"
)

type coreDeliveryError struct{ error }

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
	return atomicJSON(dir, "claim.json", r)
}
func coreFailure(kind string, err error) map[string]any {
	reason := "execution stopped before a terminal receipt was recorded; code was not replayed"
	if err != nil {
		reason = err.Error()
	}
	switch kind {
	case "cells":
		return map[string]any{"status": "interrupted", "outputs": []map[string]string{{"type": "error", "ename": "RuntimeLost", "evalue": reason}}}
	case "setups":
		return map[string]any{"status": "failed", "log": reason}
	default:
		return map[string]any{"status": "failed", "error": reason, "outputs": map[string]string{}}
	}
}

// queueCoresearch commits evidence before network I/O. Source snapshots remain
// intact if committing the receipt fails, and are never used to replay code.
func (d *daemon) queueCoresearch(w coresearch.WorkContext, kind string, id uuid.UUID, body any, outputs []recordedOutput) (bool, error) {
	d.coreReceiptMu.Lock()
	defer d.coreReceiptMu.Unlock()
	r := coreReceipt{Work: w, Kind: kind, ID: id, Body: mustMarshal(body)}
	dir := d.coreReceiptDir(r)
	if err := privateDir(dir); err != nil {
		return false, err
	}
	if old, err := readCoreReceipt(filepath.Join(dir, "finish.json")); err == nil {
		same := bytes.Equal(old.Body, r.Body) && len(old.Outputs) == len(outputs)
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
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return true, d.sendCoreReceipt(ctx, r)
}
func readCoreReceipt(path string) (coreReceipt, error) {
	var r coreReceipt
	info, err := os.Lstat(path)
	if err != nil {
		return r, err
	}
	if !info.Mode().IsRegular() || info.Size() > coresearch.MaxCellOutputBytes+(1<<20) {
		return r, errors.New("invalid Coresearch receipt")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return r, err
	}
	if err = json.Unmarshal(b, &r); err != nil {
		return r, err
	}
	if r.ID == uuid.Nil || r.Work.ClaimToken == uuid.Nil || r.Work.WorkspaceID == uuid.Nil || r.Work.ChatID == uuid.Nil || r.Work.ProjectID == uuid.Nil || (r.Kind != "cells" && r.Kind != "runs" && r.Kind != "setups") {
		return r, errors.New("invalid Coresearch receipt identity")
	}
	for i, out := range r.Outputs {
		if out.File != fmt.Sprintf("output-%08d", i) || out.Size < 0 || out.Size > 50<<20 {
			return r, errors.New("invalid Coresearch output receipt")
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
	if r.Kind == "runs" {
		renewCtx, stop := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() {
			defer close(done)
			ticker := time.NewTicker(10 * time.Second)
			defer ticker.Stop()
			for {
				_ = d.coreAPI(r.Work.WorkspaceID).Do(renewCtx, "POST", d.coreWorkPath(r.Work, "runs/"+r.ID.String()+"/heartbeat"), nil, nil)
				select {
				case <-renewCtx.Done():
					return
				case <-ticker.C:
				}
			}
		}()
		defer func() { stop(); <-done }()
		var facts map[string]any
		if err = json.Unmarshal(r.Body, &facts); err != nil {
			return err
		}
		outputs := make([]recordedOutput, 0, len(r.Outputs))
		for _, out := range r.Outputs {
			outputs = append(outputs, recordedOutput{path: out.Path, sha: out.SHA, file: filepath.Join(dir, out.File), size: out.Size})
		}
		err = d.finishRecorded(ctx, coresearch.ClaimedRun{WorkContext: r.Work, ID: r.ID}, facts, outputs)
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
	d.coreReceiptMu.Lock()
	defer d.coreReceiptMu.Unlock()
	root := filepath.Join(d.cfg.StateDir, "coresearch-receipts")
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var errs []error
	for _, entry := range entries {
		if ctx.Err() != nil {
			return errors.Join(append(errs, ctx.Err())...)
		}
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".ack-") {
			continue
		}
		path := filepath.Join(root, entry.Name(), "finish.json")
		r, e := readCoreReceipt(path)
		if errors.Is(e, os.ErrNotExist) {
			continue
		}
		if e != nil {
			errs = append(errs, e)
			continue
		}
		if filepath.Dir(path) != d.coreReceiptDir(r) {
			errs = append(errs, errors.New("Coresearch receipt path mismatch"))
			continue
		}
		e = d.sendCoreReceipt(ctx, r)
		var delivery coreDeliveryError
		if errors.As(e, &delivery) {
			slog.Warn("Coresearch receipt retained", "work", r.ID, "err", e)
		} else {
			errs = append(errs, e)
		}
	}
	return errors.Join(errs...)
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
			if len(name) != 73 {
				return errors.New("invalid acknowledged receipt")
			}
			if _, e := uuid.Parse(name[:36]); e != nil {
				return e
			}
			if _, e := uuid.Parse(name[37:]); e != nil {
				return e
			}
			if e := os.RemoveAll(dir); e != nil {
				return e
			}
			if e := syncDir(root); e != nil {
				return e
			}
			continue
		}
		if strings.Contains(entry.Name(), "/") {
			return errors.New("invalid receipt directory")
		}
		r, e := readCoreReceipt(filepath.Join(dir, "claim.json"))
		if e != nil {
			return e
		}
		if d.coreReceiptDir(r) != dir {
			return errors.New("Coresearch journal identity mismatch")
		}
		if _, e = readCoreReceipt(filepath.Join(dir, "finish.json")); e == nil {
			continue
		} else if !errors.Is(e, os.ErrNotExist) {
			return e
		}
		r.Body = mustMarshal(coreFailure(r.Kind, nil))
		if e = atomicJSON(dir, "finish.json", r); e != nil {
			return e
		}
	}
	return nil
}
