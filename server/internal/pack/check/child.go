package check

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	starjson "go.starlark.net/lib/json"
	"go.starlark.net/lib/math"
	"go.starlark.net/starlark"
	"go.starlark.net/syntax"
)

// MaybeChild serves a check request when this process was started as the
// check child, and never returns in that case. Call it first in main and in
// any TestMain of a package that runs checks.
func MaybeChild() {
	if os.Getenv(childEnv) == "" {
		return
	}
	resp := serve(os.Stdin)
	if err := json.NewEncoder(os.Stdout).Encode(resp); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func serve(in io.Reader) response {
	if err := limitMemory(); err != nil {
		return response{Error: "check process: " + err.Error()}
	}
	var req request
	if err := json.NewDecoder(io.LimitReader(in, 2*maxRecordsJSON+(8<<20))).Decode(&req); err != nil {
		return response{Error: "check process: bad request: " + err.Error()}
	}
	rs, err := runScript(req)
	if err != nil {
		return response{Error: err.Error()}
	}
	return response{Results: rs}
}

// runScript executes the script in-process under the step and time limits,
// with no load, no print output and only the json and math modules.
func runScript(req request) (rs []Result, err error) {
	defer func() {
		if p := recover(); p != nil {
			rs, err = nil, fmt.Errorf("check %s: panic: %v", req.Filename, p)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), req.Limits.Timeout)
	defer cancel()

	thread := &starlark.Thread{
		Name:  req.Filename,
		Print: func(*starlark.Thread, string) {},
		Load: func(*starlark.Thread, string) (starlark.StringDict, error) {
			return nil, errors.New("load is not allowed in a check")
		},
	}
	thread.SetMaxExecutionSteps(req.Limits.Steps)
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
	globals, err := starlark.ExecFileOptions(&syntax.FileOptions{While: true}, thread, req.Filename, req.Src, predeclared)
	if err != nil {
		return nil, fmt.Errorf("check %s: %w", req.Filename, err)
	}
	fn, ok := globals["check"].(starlark.Callable)
	if !ok {
		return nil, fmt.Errorf("check %s: no check function", req.Filename)
	}
	arg, err := toStarlark(thread, req.Records)
	if err != nil {
		return nil, err
	}
	arg.Freeze()
	out, err := starlark.Call(thread, fn, starlark.Tuple{arg}, nil)
	if err != nil {
		return nil, fmt.Errorf("check %s: %w", req.Filename, err)
	}
	rs, err = results(out, req.Failures)
	if err == nil {
		err = capResults(rs)
	}
	return rs, err
}
