package check

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

var recs = []Record{{Kind: "claim", Key: "c1", Role: "main", Column: "grounding", Body: map[string]any{"unit": "mg", "n": 3}}}

func run(t *testing.T, src string, lim Limits) ([]Result, error) {
	t.Helper()
	return Run(context.Background(), "t.star", []byte(src), recs, []string{"unit_mismatch"}, lim)
}

func TestRunPassAndFail(t *testing.T) {
	res, err := run(t, `
def check(records):
    r = records[0]
    return [
        {"id": "a", "ok": True, "note": "fine"},
        {"id": "b", "ok": r["body"]["unit"] == "kg", "failure": "unit_mismatch", "note": json.encode(r["body"])},
    ]
`, Default)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 || !res[0].OK || res[0].Note != "fine" || res[1].OK || res[1].Failure != "unit_mismatch" {
		t.Fatalf("got %+v", res)
	}
}

func TestRunRefusesLoad(t *testing.T) {
	_, err := run(t, "load(\"x.star\", \"y\")\ndef check(records):\n    return []\n", Default)
	if err == nil || !strings.Contains(err.Error(), "load is not allowed") {
		t.Fatalf("got %v", err)
	}
}

func TestRunNoOpen(t *testing.T) {
	_, err := run(t, "def check(records):\n    open(\"/etc/passwd\")\n    return []\n", Default)
	if err == nil || !strings.Contains(err.Error(), "undefined") {
		t.Fatalf("got %v", err)
	}
}

func TestRunStepLimit(t *testing.T) {
	_, err := run(t, "def check(records):\n    while True:\n        pass\n", Limits{Steps: 1000, Timeout: 5 * time.Second})
	if err == nil || !strings.Contains(err.Error(), "too many steps") {
		t.Fatalf("got %v", err)
	}
}

func TestRunTimeout(t *testing.T) {
	start := time.Now()
	_, err := run(t, "def check(records):\n    while True:\n        pass\n", Limits{Steps: 1 << 60, Timeout: 10 * time.Millisecond})
	if err == nil || !strings.Contains(err.Error(), "deadline") || time.Since(start) > 2*time.Second {
		t.Fatalf("err=%v after %v", err, time.Since(start))
	}
}

func TestRunMalformed(t *testing.T) {
	for name, ret := range map[string]string{
		"not a list":      `{"id": "a", "ok": True}`,
		"missing id":      `[{"ok": True}]`,
		"ok not bool":     `[{"id": "a", "ok": 1}]`,
		"undeclared":      `[{"id": "a", "ok": False, "failure": "other"}]`,
		"fail no failure": `[{"id": "a", "ok": False}]`,
		"ok with failure": `[{"id": "a", "ok": True, "failure": "unit_mismatch"}]`,
		"item not dict":   `["a"]`,
		"note not string": `[{"id": "a", "ok": True, "note": 5}]`,
		"unknown key":     `[{"id": "a", "ok": True, "extra": 1}]`,
	} {
		t.Run(name, func(t *testing.T) {
			if res, err := run(t, "def check(records):\n    return "+ret+"\n", Default); err == nil {
				t.Fatalf("accepted: %+v", res)
			}
		})
	}
}

func TestRunNoCheckFunction(t *testing.T) {
	if _, err := run(t, "x = 1\n", Default); err == nil {
		t.Fatal("no error")
	}
}

func TestRunRecordsFrozen(t *testing.T) {
	for _, stmt := range []string{`records[0]["kind"] = "x"`, `records[0]["body"]["unit"] = "x"`, `records.append(1)`} {
		if _, err := run(t, "def check(records):\n    "+stmt+"\n    return []\n", Default); err == nil {
			t.Fatalf("%s accepted", stmt)
		}
	}
}

func TestRunPrintContained(t *testing.T) {
	res, err := run(t, "def check(records):\n    print(\"hi\")\n    return [{\"id\": \"a\", \"ok\": True}]\n", Default)
	if err != nil || len(res) != 1 {
		t.Fatalf("%v %v", res, err)
	}
}

func TestRunOversizeInput(t *testing.T) {
	spawned := 0
	orig := spawn
	spawn = func(ctx context.Context) (*exec.Cmd, error) { spawned++; return orig(ctx) }
	defer func() { spawn = orig }()
	big := []Record{{Kind: "k", Body: map[string]any{"x": strings.Repeat("a", 9<<20)}}}
	_, err := Run(context.Background(), "t.star", []byte("def check(records):\n    return []\n"), big, nil, Default)
	if err == nil || spawned != 0 {
		t.Fatalf("err=%v spawned=%d", err, spawned)
	}
}

func TestRunTooManyResults(t *testing.T) {
	_, err := run(t, "def check(records):\n    return [{\"id\": str(i), \"ok\": True} for i in range(1001)]\n", Default)
	if err == nil || !strings.Contains(err.Error(), "more than") {
		t.Fatalf("got %v", err)
	}
	_, err = run(t, "def check(records):\n    return [{\"id\": \"a\", \"ok\": True, \"note\": \"x\" * 5000}]\n", Default)
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("got %v", err)
	}
}

func TestRunEmptyFailureType(t *testing.T) {
	if _, err := Run(context.Background(), "t.star", []byte("def check(records):\n    return []\n"), recs, []string{""}, Default); err == nil {
		t.Fatal("empty failure type accepted")
	}
}

func TestRunRunsInChildProcess(t *testing.T) {
	var cmd *exec.Cmd
	orig := spawn
	spawn = func(ctx context.Context) (*exec.Cmd, error) {
		c, err := orig(ctx)
		cmd = c
		return c, err
	}
	defer func() { spawn = orig }()
	if _, err := run(t, "def check(records):\n    return []\n", Default); err != nil {
		t.Fatal(err)
	}
	if cmd == nil || cmd.ProcessState == nil || cmd.ProcessState.Pid() == os.Getpid() {
		t.Fatal("script did not run in another process")
	}
}
