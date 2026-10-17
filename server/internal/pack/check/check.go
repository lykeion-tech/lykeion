// Package check runs the Starlark checks that field packs ship. A check sees
// only the records it is handed and has no file system, network, clock or
// load; every malformed outcome is an error, never a pass.
package check

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"time"

	starjson "go.starlark.net/lib/json"
	"go.starlark.net/starlark"
)

const (
	childEnv       = "LYKEION_STARLARK_CHILD"
	maxRecordsJSON = 8 << 20
	maxOutput      = 1 << 20
	maxResults     = 1000
	maxText        = 4 << 10
	killGrace      = 2 * time.Second
)

type Record struct {
	Kind, Key, Role, Column string
	Body                    map[string]any
}

type Result struct {
	ID      string
	OK      bool
	Failure string
	Note    string
}

type Limits struct {
	Steps   uint64
	Timeout time.Duration
}

var Default = Limits{Steps: 5_000_000, Timeout: 5 * time.Second}

// request and response are the one JSON message each way between Run and the
// child process.
type request struct {
	Filename string
	Src      []byte
	Records  []Record
	Failures []string
	Limits   Limits
}

type response struct {
	Results []Result
	Error   string
}

// spawn builds the child command; tests wrap it to observe the process.
var spawn = func(ctx context.Context) (*exec.Cmd, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, exe)
	// The child gets no inherited environment, so no secret reaches pack code.
	cmd.Env = []string{childEnv + "=1"}
	return cmd, nil
}

// Run executes src's `check(records)` in a short-lived child process with a
// memory cap and returns its results. failures is the manifest's declared
// failure types; any other value is an error.
func Run(ctx context.Context, filename string, src []byte, recs []Record, failures []string, lim Limits) ([]Result, error) {
	if slices.Contains(failures, "") {
		return nil, errors.New("check: an empty failure type is not allowed")
	}
	if b, err := json.Marshal(recs); err != nil {
		return nil, fmt.Errorf("check records: %w", err)
	} else if len(b) > maxRecordsJSON {
		return nil, fmt.Errorf("check records: %d bytes exceeds the %d byte limit", len(b), maxRecordsJSON)
	}
	in, err := json.Marshal(request{filename, src, recs, failures, lim})
	if err != nil {
		return nil, fmt.Errorf("check request: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, lim.Timeout+killGrace)
	defer cancel()
	cmd, err := spawn(ctx)
	if err != nil {
		return nil, fmt.Errorf("check process: %w", err)
	}
	cmd.Stdin = bytes.NewReader(in)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("check process: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("check process: %w", err)
	}
	out, readErr := io.ReadAll(io.LimitReader(stdout, maxOutput+1))
	oversize := len(out) > maxOutput
	if oversize {
		_ = cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	switch {
	case oversize:
		return nil, fmt.Errorf("check process: output exceeds %d bytes", maxOutput)
	case ctx.Err() != nil:
		return nil, errors.New("check process killed: memory or time limit")
	case readErr != nil:
		return nil, fmt.Errorf("check process: %w", readErr)
	case waitErr != nil:
		return nil, fmt.Errorf("check process failed (memory or time limit, or a crash): %w", waitErr)
	}
	var resp response
	if err := json.Unmarshal(out, &resp); err != nil {
		return nil, fmt.Errorf("check process: undecodable output: %w", err)
	}
	if resp.Error != "" {
		return nil, errors.New(resp.Error)
	}
	if err := capResults(resp.Results); err != nil {
		return nil, err
	}
	return resp.Results, nil
}

func capResults(rs []Result) error {
	if len(rs) > maxResults {
		return fmt.Errorf("check returned %d results, more than %d", len(rs), maxResults)
	}
	for _, r := range rs {
		if len(r.ID) > maxText || len(r.Note) > maxText || len(r.Failure) > maxText {
			return fmt.Errorf("result %q: a text field exceeds %d bytes", r.ID, maxText)
		}
	}
	return nil
}

// toStarlark round-trips the records through JSON so the script gets plain
// dicts and lists with no Go-backed values.
func toStarlark(thread *starlark.Thread, recs []Record) (starlark.Value, error) {
	rows := make([]map[string]any, len(recs))
	for i, r := range recs {
		body := r.Body
		if body == nil {
			body = map[string]any{}
		}
		rows[i] = map[string]any{"kind": r.Kind, "key": r.Key, "role": r.Role, "column": r.Column, "body": body}
	}
	b, err := json.Marshal(rows)
	if err != nil {
		return nil, fmt.Errorf("check records: %w", err)
	}
	decode := starjson.Module.Members["decode"]
	v, err := starlark.Call(thread, decode, starlark.Tuple{starlark.String(b)}, nil)
	if err != nil {
		return nil, fmt.Errorf("check records: %w", err)
	}
	return v, nil
}

func results(v starlark.Value, failures []string) ([]Result, error) {
	list, ok := v.(*starlark.List)
	if !ok {
		return nil, fmt.Errorf("check returned %s, want a list", v.Type())
	}
	out := make([]Result, 0, list.Len())
	for i := range list.Len() {
		d, ok := list.Index(i).(*starlark.Dict)
		if !ok {
			return nil, fmt.Errorf("result %d is %s, want a dict", i, list.Index(i).Type())
		}
		var r Result
		var haveID, haveOK bool
		for _, item := range d.Items() {
			key, _ := starlark.AsString(item[0])
			switch key {
			case "id":
				s, ok := item[1].(starlark.String)
				if !ok || s == "" {
					return nil, fmt.Errorf("result %d: id must be a non-empty string", i)
				}
				r.ID, haveID = string(s), true
			case "ok":
				b, ok := item[1].(starlark.Bool)
				if !ok {
					return nil, fmt.Errorf("result %d: ok must be a bool", i)
				}
				r.OK, haveOK = bool(b), true
			case "failure", "note":
				s, ok := item[1].(starlark.String)
				if !ok {
					return nil, fmt.Errorf("result %d: %s must be a string", i, key)
				}
				if key == "failure" {
					r.Failure = string(s)
				} else {
					r.Note = string(s)
				}
			default:
				return nil, fmt.Errorf("result %d: unknown key %s", i, item[0])
			}
		}
		if !haveID || !haveOK {
			return nil, fmt.Errorf("result %d: id and ok are required", i)
		}
		switch {
		case r.OK && r.Failure != "":
			return nil, fmt.Errorf("result %q: a pass must not set failure", r.ID)
		case !r.OK && !slices.Contains(failures, r.Failure):
			return nil, fmt.Errorf("result %q: failure %q is not declared", r.ID, r.Failure)
		}
		out = append(out, r)
	}
	return out, nil
}
