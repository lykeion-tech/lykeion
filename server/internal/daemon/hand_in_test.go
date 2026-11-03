package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"lykeion/server/internal/apiclient"
	"lykeion/server/pkg/agent"
)

// execCall is what the fake backend was started with.
type execCall struct{ prompt, resume, schema string }

// handInRun runs the fixture's run on a fake backend whose every turn ends
// with a hand-in, answering the n-th hand-in post with reply(n), a 200 unless
// it names another status. It returns the backend's calls and the paths the
// daemon requested, in order, and the body of the daemon's failure report.
func handInRun(t *testing.T, f *daemonFixture, turn func(o agent.ExecOptions), reply func(n int) (int, string)) ([]execCall, []string, map[string]any) {
	t.Helper()
	var mu sync.Mutex
	var calls []execCall
	var paths []string
	var failed map[string]any
	posts := 0
	hook := func(w http.ResponseWriter, r *http.Request) bool {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		if strings.HasSuffix(r.URL.Path, "/fail") {
			b, err := io.ReadAll(r.Body)
			must(t, err)
			_ = json.Unmarshal(b, &failed)
			r.Body = io.NopCloser(strings.NewReader(string(b)))
		}
		if !strings.HasSuffix(r.URL.Path, "/hand-in") || reply == nil {
			mu.Unlock()
			return false
		}
		posts++
		n := posts
		mu.Unlock()
		// Other requests go on while a reply takes its time.
		code, body := reply(n)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		io.WriteString(w, body)
		return true
	}
	f.intercept.Store(&hook)
	f.cfg.NewBackend = func(string, string) (agent.Backend, error) {
		return backendFunc(func(_ context.Context, p string, o agent.ExecOptions) (*agent.Session, error) {
			mu.Lock()
			calls = append(calls, execCall{p, o.Resume, o.OutputSchema})
			mu.Unlock()
			if turn != nil {
				turn(o)
			}
			msgs := make(chan agent.Message)
			done := make(chan agent.Result, 1)
			close(msgs)
			done <- agent.Result{SessionID: "s1", Output: `{"done":true}`, Usage: []agent.Usage{{Model: "m", InputTokens: 10, OutputTokens: 1}}}
			close(done)
			return &agent.Session{Messages: msgs, Done: done}, nil
		}), nil
	}
	cancel, done := startDaemon(t, f.cfg)
	waitFor(t, func() bool {
		s := f.status(t)
		return s == "completed" || s == "failed" || f.parked(t) || s == "cancelled" && f.lateUsage(t)
	})
	stopDaemon(t, cancel, done)
	mu.Lock()
	defer mu.Unlock()
	return slices.Clone(calls), slices.Clone(paths), failed
}

// reasks are the content of the run's re-ask status messages, in order.
func (f *daemonFixture) reasks(t *testing.T) []string {
	t.Helper()
	rows, err := f.env.Owner.Query(context.Background(), "SELECT content::text FROM agent_run_message WHERE agent_run_id=$1 AND type='status' AND content ? 'handin_reask' ORDER BY seq", f.id)
	must(t, err)
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		must(t, rows.Scan(&s))
		out = append(out, s)
	}
	must(t, rows.Err())
	return out
}

func (f *daemonFixture) outcome(t *testing.T) (status, reason, msg string, input int64) {
	t.Helper()
	must(t, f.env.Owner.QueryRow(context.Background(), `SELECT status, coalesce(failure_reason, ''), error,
		(SELECT coalesce(sum(input_tokens), 0) FROM agent_run_usage u WHERE u.agent_run_id = t.id)::bigint FROM agent_run t WHERE id=$1`, f.id).Scan(&status, &reason, &msg, &input))
	return
}

const oneProblem = `{"accepted":false,"problems":["claim 1 cites no source"]}`

func TestHandInReasksTwiceThenFails(t *testing.T) {
	f := fixture(t)
	calls, _, _ := handInRun(t, f, nil, func(int) (int, string) { return 200, oneProblem })
	if len(calls) != 3 {
		t.Fatalf("backend ran %d times, want 3: %+v", len(calls), calls)
	}
	if calls[0].resume != "" || strings.Contains(calls[0].prompt, "could not accept") {
		t.Fatalf("first turn = %+v", calls[0])
	}
	want := "Lykeion could not accept your hand-in:\n- claim 1 cites no source\nFix these and give your hand-in again."
	for _, c := range calls {
		if c.schema == "" {
			t.Fatalf("a turn ran without the hand-in schema: %+v", c)
		}
	}
	for _, c := range calls[1:] {
		if c.resume != "s1" || c.prompt != want {
			t.Fatalf("re-ask turn = %+v", c)
		}
	}
	reasks := f.reasks(t)
	if len(reasks) != 2 {
		t.Fatalf("re-ask messages = %q", reasks)
	}
	for i, m := range reasks {
		var got struct {
			N        int      `json:"handin_reask"`
			Problems []string `json:"problems"`
		}
		must(t, json.Unmarshal([]byte(m), &got))
		if got.N != i+1 || !slices.Equal(got.Problems, []string{"claim 1 cites no source"}) {
			t.Fatalf("re-ask message %d = %s", i+1, m)
		}
	}
	status, reason, msg, input := f.outcome(t)
	if status != "failed" || reason != agent.ReasonHandInInvalid || msg != "claim 1 cites no source" {
		t.Fatalf("outcome = %s/%s %q", status, reason, msg)
	}
	if input != 30 {
		t.Fatalf("input tokens = %d, want every turn's 30", input)
	}
}

func TestHandInAcceptedOnSecondTry(t *testing.T) {
	f := fixture(t)
	calls, _, _ := handInRun(t, f, nil, func(n int) (int, string) {
		if n == 1 {
			return 200, oneProblem
		}
		return 200, `{"accepted":true}`
	})
	if len(calls) != 2 {
		t.Fatalf("backend ran %d times, want 2", len(calls))
	}
	if n := len(f.reasks(t)); n != 1 {
		t.Fatalf("%d re-ask messages, want 1", n)
	}
	if status, reason, msg, input := f.outcome(t); status != "completed" || input != 20 {
		t.Fatalf("outcome = %s/%s %q, %d input tokens", status, reason, msg, input)
	}
}

func TestOutputsSavedBeforeSubmit(t *testing.T) {
	f := fixture(t)
	write := func(o agent.ExecOptions) {
		must(t, os.MkdirAll(filepath.Join(o.Dir, "outputs", "sub"), 0700))
		must(t, os.WriteFile(filepath.Join(o.Dir, "outputs", "a.csv"), []byte("x,y\n1,2\n"), 0600))
		must(t, os.WriteFile(filepath.Join(o.Dir, "outputs", "sub", "b.png"), []byte("\x89PNG\r\n"), 0600))
	}
	_, paths, _ := handInRun(t, f, write, func(int) (int, string) { return 200, `{"accepted":true}` })
	submit := slices.IndexFunc(paths, func(p string) bool { return strings.HasSuffix(p, "/hand-in") })
	uploads := 0
	for i, p := range paths {
		if p == "/api/role/outputs" {
			uploads++
			if submit < 0 || i > submit {
				t.Fatalf("an output went after the hand-in: %q", paths)
			}
		}
	}
	if submit < 0 || uploads != 2 {
		t.Fatalf("%d uploads, hand-in at %d: %q", uploads, submit, paths)
	}
	rows, err := f.env.Owner.Query(context.Background(), "SELECT body->>'name' FROM record WHERE card_id=$1 AND kind='output' ORDER BY 1", f.card)
	must(t, err)
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		must(t, rows.Scan(&n))
		names = append(names, n)
	}
	if !slices.Equal(names, []string{"outputs/a.csv", "outputs/sub/b.png"}) {
		t.Fatalf("saved outputs = %q", names)
	}
}

func TestRunWithoutSchemaUnchanged(t *testing.T) {
	f := fixture(t)
	// Every study column owes a hand-in; a run outside the board owes none.
	_, err := f.env.Owner.Exec(context.Background(), "UPDATE agent_run SET board_column = '' WHERE id = $1", f.id)
	must(t, err)
	calls, paths, _ := handInRun(t, f, nil, func(int) (int, string) { return 200, oneProblem })
	if len(calls) != 1 || calls[0].schema != "" {
		t.Fatalf("backend calls = %+v", calls)
	}
	if slices.ContainsFunc(paths, func(p string) bool { return strings.HasSuffix(p, "/hand-in") || p == "/api/role/outputs" }) {
		t.Fatalf("a run with no schema handed in: %q", paths)
	}
	if status, _, _, _ := f.outcome(t); status != "completed" {
		t.Fatalf("status = %s", status)
	}
}

// Output that is not JSON still goes as the hand-in, a refusal naming no
// problem is one, and a session that can't be resumed ends the asking with
// the problems unresolved and every turn's usage kept.
func TestHandInEdges(t *testing.T) {
	var posted []json.RawMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/role/records":
			io.WriteString(w, `[]`)
		case strings.HasSuffix(r.URL.Path, "/hand-in"):
			var body struct {
				HandIn json.RawMessage `json:"hand_in"`
			}
			must(t, json.NewDecoder(r.Body).Decode(&body))
			posted = append(posted, body.HandIn)
			io.WriteString(w, `{"accepted":false}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	d := &daemon{cfg: Config{Server: srv.URL, Home: t.TempDir()}, api: &apiclient.Client{Server: srv.URL}}
	var c claimed
	c.AgentRun.ID, c.AgentRun.Attempt = uuid.New(), 1
	must(t, os.Mkdir(filepath.Join(d.cfg.Home, scratchName(c.AgentRun.ID, 1)), 0700))
	var said []agent.Message
	execs := 0
	first := agent.Result{SessionID: "s1", Output: "I am done.", Usage: []agent.Usage{{Model: "m", InputTokens: 5}}}
	res, probs, err := d.handIn(t.Context(), c, first, func(string, string) agent.Result {
		execs++
		return agent.Result{ResumeRefused: true, Err: errors.New("no such session"), Usage: []agent.Usage{{Model: "m", InputTokens: 1}}}
	}, func(m agent.Message) error { said = append(said, m); return nil })
	must(t, err)
	if len(posted) != 1 || string(posted[0]) != `"I am done."` {
		t.Fatalf("posted %q", posted)
	}
	if !slices.Equal(probs, []string{"the hand-in was not accepted"}) || execs != 1 || len(said) != 1 {
		t.Fatalf("problems %q, %d turns, %d messages", probs, execs, len(said))
	}
	if res.Err != nil || res.SessionID != "s1" || len(res.Usage) != 1 || res.Usage[0].InputTokens != 6 {
		t.Fatalf("result = %+v", res)
	}
}

// An output the server refuses is a problem for the agent, and the hand-in
// waits until the outputs are saved.
func TestRefusedOutputIsAProblem(t *testing.T) {
	handIns := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/role/records":
			io.WriteString(w, `[]`)
		case r.URL.Path == "/api/role/outputs":
			w.WriteHeader(400)
			io.WriteString(w, `{"error":"A run can save at most 100 outputs"}`)
		case strings.HasSuffix(r.URL.Path, "/hand-in"):
			handIns++
			io.WriteString(w, `{"accepted":true}`)
		}
	}))
	defer srv.Close()
	d := &daemon{cfg: Config{Server: srv.URL, Home: t.TempDir()}, api: &apiclient.Client{Server: srv.URL}}
	var c claimed
	c.AgentRun.ID, c.AgentRun.Attempt = uuid.New(), 1
	out := filepath.Join(d.cfg.Home, scratchName(c.AgentRun.ID, 1), "outputs")
	must(t, os.MkdirAll(out, 0700))
	must(t, os.WriteFile(filepath.Join(out, "x.csv"), []byte("1"), 0600))
	_, probs, err := d.handIn(t.Context(), c, agent.Result{SessionID: "s1", Output: `{}`}, func(string, string) agent.Result {
		return agent.Result{Err: errors.New("stop")}
	}, func(agent.Message) error { return nil })
	must(t, err)
	if handIns != 0 || probs != nil {
		t.Fatalf("%d hand-ins, problems %q", handIns, probs)
	}
	_, probs, err = d.handIn(t.Context(), c, agent.Result{SessionID: "s1", Output: `{}`}, func(string, string) agent.Result {
		return agent.Result{ResumeRefused: true}
	}, func(agent.Message) error { return nil })
	must(t, err)
	if !slices.Equal(probs, []string{"outputs/x.csv was not saved: A run can save at most 100 outputs"}) || handIns != 0 {
		t.Fatalf("%d hand-ins, problems %q", handIns, probs)
	}
}

// A hand-in the server answers with an error is the machine's failure, never
// the agent's or its sign-in's: the run is parked as offline.
func TestHandInServerErrorIsInfrastructure(t *testing.T) {
	for _, code := range []int{400, 401} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			f := fixture(t)
			_, _, failed := handInRun(t, f, nil, func(int) (int, string) {
				return code, `{"error":"` + strconv.Itoa(code) + ` ` + http.StatusText(code) + `"}`
			})
			if failed["failure_reason"] != agent.ReasonRuntimeOffline {
				t.Fatalf("failure report = %v", failed)
			}
		})
	}
}

// Refused outputs give a bounded list: once the server refuses one, the rest
// are counted, not sent; a hard-linked file is never sent.
func TestRefusedOutputsAreBounded(t *testing.T) {
	uploads := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/role/records":
			io.WriteString(w, `[]`)
		case "/api/role/outputs":
			uploads++
			w.WriteHeader(400)
			io.WriteString(w, `{"error":"A run can save at most 100 outputs"}`)
		}
	}))
	defer srv.Close()
	role := &apiclient.Client{Server: srv.URL}
	dir := t.TempDir()
	out := filepath.Join(dir, "outputs")
	must(t, os.MkdirAll(out, 0700))
	for i := range 30 {
		must(t, os.WriteFile(filepath.Join(out, fmt.Sprintf("f%02d.csv", i)), []byte("1"), 0600))
	}
	probs, err := sendOutputs(t.Context(), role, dir, noneSaved, nil)
	must(t, err)
	want := []string{"outputs/f00.csv was not saved: A run can save at most 100 outputs", "and 29 more files were not saved"}
	if uploads != 1 || !slices.Equal(probs, want) {
		t.Fatalf("%d uploads, problems %q", uploads, probs)
	}

	uploads = 0
	linked := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(linked, "outputs"), 0700))
	must(t, os.WriteFile(filepath.Join(linked, "secret"), []byte("s"), 0600))
	must(t, os.Link(filepath.Join(linked, "secret"), filepath.Join(linked, "outputs", "s.txt")))
	probs, err = sendOutputs(t.Context(), role, linked, noneSaved, nil)
	must(t, err)
	if uploads != 0 || !slices.Equal(probs, []string{"outputs/s.txt was not saved: it is a hard link to another file"}) {
		t.Fatalf("%d uploads, problems %q", uploads, probs)
	}

	uploads = 0
	big := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(big, "outputs"), 0700))
	for i := range 25 {
		f, err := os.Create(filepath.Join(big, "outputs", fmt.Sprintf("b%02d", i)))
		must(t, err)
		must(t, f.Truncate(maxOutputBytes+1))
		f.Close()
	}
	probs, err = sendOutputs(t.Context(), role, big, noneSaved, nil)
	must(t, err)
	if uploads != 0 || len(probs) != maxOutputProblems || probs[len(probs)-1] != "and 6 more files were not saved" {
		t.Fatalf("%d uploads, %d problems, last %q", uploads, len(probs), probs[len(probs)-1])
	}
}

// An Autoresearch run has no study, so its token may not list or save
// outputs: its hand-in is posted without either.
func TestAutoresearchHandInSendsNoOutputs(t *testing.T) {
	handIns := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/role/"):
			w.WriteHeader(403)
			io.WriteString(w, `{"error":"this agent run's token may not do that"}`)
		case strings.HasSuffix(r.URL.Path, "/hand-in"):
			handIns++
			io.WriteString(w, `{"accepted":true}`)
		}
	}))
	defer srv.Close()
	d := &daemon{cfg: Config{Server: srv.URL, Home: t.TempDir()}, api: &apiclient.Client{Server: srv.URL}}
	var c claimed
	c.AgentRun.ID, c.AgentRun.Attempt, c.AgentRun.Kind = uuid.New(), 1, "autoresearch"
	out := filepath.Join(d.cfg.Home, scratchName(c.AgentRun.ID, 1), "outputs")
	must(t, os.MkdirAll(out, 0700))
	must(t, os.WriteFile(filepath.Join(out, "x.csv"), []byte("1"), 0600))
	_, probs, err := d.handIn(t.Context(), c, agent.Result{SessionID: "s1", Output: `{}`}, func(string, string) agent.Result {
		return agent.Result{Err: errors.New("no re-ask expected")}
	}, func(agent.Message) error { return nil })
	if err != nil || probs != nil || handIns != 1 {
		t.Fatalf("err %v, problems %q, %d hand-ins", err, probs, handIns)
	}
}

func handInPosts(paths []string) int {
	n := 0
	for _, p := range paths {
		if strings.HasSuffix(p, "/hand-in") {
			n++
		}
	}
	return n
}

// A hand-in the server can't take for now (no answer, a 5xx, 408 or 429)
// waits with the attempt's final report and is posted again; the agent's
// work is neither lost nor done again.
func TestHandInWaitsForTheServer(t *testing.T) {
	for _, code := range []int{503, 429} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			f := fixture(t)
			calls, paths, _ := handInRun(t, f, nil, func(n int) (int, string) {
				if n == 1 {
					return code, `{"error":"unavailable"}`
				}
				return 200, `{"accepted":true}`
			})
			if len(calls) != 1 || handInPosts(paths) != 2 {
				t.Fatalf("backend ran %d times, %d hand-in posts", len(calls), handInPosts(paths))
			}
			if status, reason, msg, _ := f.outcome(t); status != "completed" {
				t.Fatalf("outcome = %s/%s %q", status, reason, msg)
			}
		})
	}
}

// A kept hand-in the server then finds problems with can't be corrected: the
// session that would correct it is gone. The run is tried again without the
// agent hearing why, as for any machine failure.
func TestKeptHandInWithProblemsIsTriedAgain(t *testing.T) {
	f := fixture(t)
	calls, _, failed := handInRun(t, f, nil, func(n int) (int, string) {
		if n == 1 {
			return 502, `{"error":"bad gateway"}`
		}
		return 200, oneProblem
	})
	if len(calls) != 1 || len(f.reasks(t)) != 0 {
		t.Fatalf("backend ran %d times, %d re-asks", len(calls), len(f.reasks(t)))
	}
	if failed["failure_reason"] != agent.ReasonRuntimeOffline || !strings.Contains(fmt.Sprint(failed["error"]), "claim 1 cites no source") {
		t.Fatalf("failure report = %v", failed)
	}
}

func noneSaved() (map[string]bool, error) { return map[string]bool{}, nil }

func (f *daemonFixture) lateUsage(t *testing.T) bool {
	t.Helper()
	var ok bool
	must(t, f.env.Owner.QueryRow(context.Background(), "SELECT late_usage_at IS NOT NULL FROM agent_run WHERE id=$1", f.id).Scan(&ok))
	return ok
}

// A kept hand-in the server then refuses for good (the attempt was swept or
// is no longer running) is dropped: the report goes as a machine failure, so
// the outbox delivers it instead of quarantining it.
func TestKeptHandInRefusedLaterStillReports(t *testing.T) {
	f := fixture(t)
	calls, paths, failed := handInRun(t, f, nil, func(n int) (int, string) {
		if n == 1 {
			return 503, `{"error":"unavailable"}`
		}
		return 409, `{"error":"the agent run is not in a state that allows this"}`
	})
	if len(calls) != 1 || handInPosts(paths) != 2 {
		t.Fatalf("backend ran %d times, %d hand-in posts", len(calls), handInPosts(paths))
	}
	if failed["failure_reason"] != agent.ReasonRuntimeOffline {
		t.Fatalf("failure report = %v", failed)
	}
	left, err := filepath.Glob(filepath.Join(f.cfg.StateDir, "outbox", "*.json"))
	must(t, err)
	quarantined, err := filepath.Glob(filepath.Join(f.cfg.StateDir, "outbox", "quarantine", "*.json"))
	must(t, err)
	if len(left)+len(quarantined) != 0 {
		t.Fatalf("outbox still holds %q, quarantine %q", left, quarantined)
	}
}

// A run cancelled while its hand-in is being posted keeps no hand-in: its
// report is the cancelled attempt's, with its usage.
func TestCancelledRunKeepsNoHandIn(t *testing.T) {
	f := fixture(t)
	_, paths, _ := handInRun(t, f, nil, func(n int) (int, string) {
		if n > 1 {
			return 409, `{"error":"the agent run is not in a state that allows this"}`
		}
		must(t, f.runs.Cancel(context.Background(), f.ws, f.user, f.id))
		// Long enough for the daemon to see the cancellation and stop the
		// run while this post is still unanswered.
		time.Sleep(6 * time.Second)
		return 503, `{"error":"unavailable"}`
	})
	if n := handInPosts(paths); n != 1 {
		t.Fatalf("%d hand-in posts, want 1", n)
	}
	if status, _, _, input := f.outcome(t); status != "cancelled" || input != 10 {
		t.Fatalf("outcome = %s, %d input tokens", status, input)
	}
}
