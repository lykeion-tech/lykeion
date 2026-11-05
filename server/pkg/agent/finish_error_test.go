package agent

import (
	"context"
	"errors"
	"testing"
)

func TestRunRetainsFinishErrorAfterCancellationOrEmitFailure(t *testing.T) {
	for _, failure := range []string{"cancel", "emit"} {
		t.Run(failure, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cmd, err := command(ctx, script(t, "exit 0"), "", ExecOptions{Dir: t.TempDir(), Sandbox: testSandbox})
			if err != nil {
				t.Fatal(err)
			}
			retireErr := errors.New("MCP cleanup failed")
			emitFailure := errors.New("message delivery failed")
			session, err := run(ctx, cmd, func(bool) {}, func([]byte, emitMessage, *Result) (bool, error) { return true, nil }, func(res *Result, emit emitMessage) {
				res.Err = errors.Join(res.Err, retireErr)
				if failure == "cancel" {
					cancel()
				} else {
					emit(Message{Type: "log"})
				}
			}, func(Message) error { return emitFailure })
			if err != nil {
				t.Fatal(err)
			}
			_, res := collect(t, session)
			if !errors.Is(res.Err, retireErr) {
				t.Fatalf("cleanup error lost: %v", res.Err)
			}
			want := emitFailure
			if failure == "cancel" {
				want = context.Canceled
			}
			if !errors.Is(res.Err, want) {
				t.Fatalf("primary error lost: %v", res.Err)
			}
		})
	}
}
