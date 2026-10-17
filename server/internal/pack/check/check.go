// Package check runs the Starlark checks that field packs ship. A check sees
// only the records it is handed and has no file system, network, clock or
// load; every malformed outcome is an error, never a pass.
package check

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	starjson "go.starlark.net/lib/json"
	"go.starlark.net/lib/math"
	"go.starlark.net/starlark"
	"go.starlark.net/syntax"
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

// Run executes src's `check(records)` and returns its results. failures is the
// manifest's declared failure types; any other value is an error.
func Run(ctx context.Context, filename string, src []byte, recs []Record, failures []string, lim Limits) ([]Result, error) {
	ctx, cancel := context.WithTimeout(ctx, lim.Timeout)
	defer cancel()

	thread := &starlark.Thread{
		Name:  filename,
		Print: func(*starlark.Thread, string) {},
		Load: func(*starlark.Thread, string) (starlark.StringDict, error) {
			return nil, errors.New("load is not allowed in a check")
		},
	}
	thread.SetMaxExecutionSteps(lim.Steps)
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			thread.Cancel(ctx.Err().Error())
		case <-done:
		}
	}()

	predeclared := starlark.StringDict{"json": starjson.Module, "math": math.Module}
	globals, err := starlark.ExecFileOptions(&syntax.FileOptions{}, thread, filename, src, predeclared)
	if err != nil {
		return nil, fmt.Errorf("check %s: %w", filename, err)
	}
	fn, ok := globals["check"].(starlark.Callable)
	if !ok {
		return nil, fmt.Errorf("check %s: no check function", filename)
	}

	arg, err := toStarlark(thread, recs)
	if err != nil {
		return nil, err
	}
	arg.Freeze()
	out, err := starlark.Call(thread, fn, starlark.Tuple{arg}, nil)
	if err != nil {
		return nil, fmt.Errorf("check %s: %w", filename, err)
	}
	return results(out, failures)
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
