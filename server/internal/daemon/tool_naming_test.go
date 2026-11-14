package daemon

import (
	"context"
	"errors"
	"fmt"
	"lykeion/server/pkg/agent"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

type scriptedNaming struct {
	answer string
	err    error
	usage  []agent.Usage
	mu     sync.Mutex
	closed bool
}

func (s *scriptedNaming) Ask(context.Context, string) (string, error) { return s.answer, s.err }
func (s *scriptedNaming) Close() agent.Result {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return agent.Result{Usage: s.usage}
}
func TestStepNamesStayAttachedToTheirOriginalNumber(t *testing.T) {
	batch := []namingStep{{seq: 7, id: "a"}, {seq: 13, id: "b"}, {seq: 18, id: "c"}}
	got := parseStepNames("3. Save analysis results\n1. Read the dataset\n99. Wrong index\n2. First ambiguous title\n2. Second ambiguous title", batch)
	if len(got) != 2 || got[0].Seq != 7 || got[0].Title != "Read the dataset" || got[1].Seq != 18 || got[1].ToolUseID != "c" {
		t.Fatalf("wrong association: %+v", got)
	}
}
func TestToolNamesArriveLiveAndDrainUsageBeforeFinish(t *testing.T) {
	session := &scriptedNaming{answer: "1. Calculate the sample mean", usage: []agent.Usage{{Model: "m", InputTokens: 5}}}
	emitted := make(chan agent.StepTitles, 1)
	n := newStepNamer(context.Background(), func(context.Context) (toolNamingSession, error) { return session, nil }, func(titles agent.StepTitles) error { emitted <- titles; return nil })
	n.saw(4, agent.Message{Type: "tool_use", Tool: "Bash", Content: []byte(`{"id":"a","input":{"command":"python mean.py"}}`)})
	select {
	case got := <-emitted:
		if len(got.Titles) != 1 || got.Titles[0].Seq != 4 || got.Titles[0].Title != "Calculate the sample mean" {
			t.Fatalf("%+v", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no live title before run ended")
	}
	usage, err := n.finish()
	if err != nil || len(usage) != 1 || usage[0].InputTokens != 5 || !session.closed {
		t.Fatalf("usage %+v closed %v err %v", usage, session.closed, err)
	}
}
func TestNamingFailureFallsBackAndPersistenceFailureIsReported(t *testing.T) {
	tries := 0
	n := newStepNamer(context.Background(), func(context.Context) (toolNamingSession, error) { tries++; return nil, errors.New("unavailable") }, func(agent.StepTitles) error { t.Error("invented title"); return nil })
	n.saw(1, agent.Message{Type: "tool_use", Tool: "Bash", Content: []byte(`{"command":"ls"}`)})
	if _, err := n.finish(); err != nil || tries > 2 {
		t.Fatalf("fallback err %v attempts %d", err, tries)
	}
	failure := errors.New("disk unavailable")
	n = newStepNamer(context.Background(), func(context.Context) (toolNamingSession, error) {
		return &scriptedNaming{answer: "1. Read the dataset"}, nil
	}, func(agent.StepTitles) error { return failure })
	n.saw(1, agent.Message{Type: "tool_use", Tool: "Bash", Content: []byte(`{"command":"ls"}`)})
	if _, err := n.finish(); !errors.Is(err, failure) {
		t.Fatalf("lost durable write failure: %v", err)
	}
}

func TestPendingNameUsesLaterArgumentsAndRejectsInvalidDuplicate(t *testing.T) {
	batch := []namingStep{{seq: 1, id: "a"}}
	if got := parseStepNames("1. This title has far too many words to accept\n1. Read data", batch); len(got) != 0 {
		t.Fatalf("ambiguous number accepted: %+v", got)
	}
	var prompt string
	session := &captureNaming{capture: func(text string) { prompt = text }}
	n := newStepNamer(context.Background(), func(context.Context) (toolNamingSession, error) { return session, nil }, func(agent.StepTitles) error { return nil })
	n.saw(1, agent.Message{Type: "tool_use", Content: []byte(`{"step_update":{"step_index":3,"tool_name":"run_command","tool_info":{}}}`)})
	n.saw(2, agent.Message{Type: "tool_result", Content: []byte(`{"step_update":{"step_index":3,"tool_name":"run_command","tool_info":{"parameters":{"CommandLine":"python actual_analysis.py"}}}}`)})
	n.finish()
	if !strings.Contains(prompt, "actual_analysis.py") {
		t.Fatalf("named without late evidence: %q", prompt)
	}
}

type captureNaming struct{ capture func(string) }

func (c *captureNaming) Ask(_ context.Context, prompt string) (string, error) {
	c.capture(prompt)
	return "1. Analyze the recorded data", nil
}
func (c *captureNaming) Close() agent.Result { return agent.Result{} }

type timedNaming struct {
	began chan struct{}
	once  sync.Once
	delay time.Duration
}

func (s *timedNaming) Ask(ctx context.Context, _ string) (string, error) {
	if s.began != nil {
		s.once.Do(func() { close(s.began) })
	}
	if s.delay > 0 {
		select {
		case <-time.After(s.delay):
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	var b strings.Builder
	for i := 1; i <= 20; i++ {
		fmt.Fprintf(&b, "%d. Inspect recorded call\n", i)
	}
	return b.String(), nil
}
func (s *timedNaming) Close() agent.Result { return agent.Result{} }
func TestNamingBeyondTwoHundredAcrossBatches(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var titles []agent.StepTitle
		n := newStepNamer(context.Background(), func(context.Context) (toolNamingSession, error) { return &timedNaming{}, nil }, func(meta agent.StepTitles) error { titles = append(titles, meta.Titles...); return nil })
		for start := 1; start <= 201; start += 100 {
			for seq := start; seq < start+100; seq++ {
				n.saw(int32(seq), agent.Message{Type: "tool_use", Tool: "Bash", Content: []byte(fmt.Sprintf(`{"id":"call-%d","input":{"command":"true"}}`, seq))})
			}
			time.Sleep(2 * stepSettle)
		}
		if _, err := n.finish(); err != nil {
			t.Fatal(err)
		}
		if len(titles) != 300 || titles[299].Seq != 300 || titles[299].ToolUseID != "call-300" {
			t.Fatalf("lost titles: %d", len(titles))
		}
	})
}
func TestNamingFinalDrainBoundsAnAlreadyRunningBatch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		began := make(chan struct{})
		session := &timedNaming{began: began, delay: 4 * time.Second}
		n := newStepNamer(context.Background(), func(context.Context) (toolNamingSession, error) { return session, nil }, func(agent.StepTitles) error { return nil })
		for seq := 1; seq <= 200; seq++ {
			n.saw(int32(seq), agent.Message{Type: "tool_use", Tool: "Bash", Content: []byte(fmt.Sprintf(`{"id":"call-%d","input":{"command":"true"}}`, seq))})
		}
		<-began
		started := time.Now()
		n.finish()
		if elapsed := time.Since(started); elapsed > 26*time.Second {
			t.Fatalf("final drain continued processing live batches for %v", elapsed)
		}
	})
}

func TestNamingRetriesMissing(t *testing.T) {
	calls := 0
	var titles []agent.StepTitle
	n := newStepNamer(context.Background(), func(context.Context) (toolNamingSession, error) {
		return &functionNaming{ask: func(context.Context, string) (string, error) {
			calls++
			if calls == 1 {
				return "1. Read recorded data\n2. This title has far too many words to accept", nil
			}
			return "1. Inspect recorded call", nil
		}}, nil
	}, func(meta agent.StepTitles) error { titles = append(titles, meta.Titles...); return nil })
	for seq := 1; seq <= 2; seq++ {
		n.saw(int32(seq), agent.Message{Type: "tool_use", Tool: "Bash", Content: []byte(fmt.Sprintf(`{"id":"call-%d","input":{"command":"true"}}`, seq))})
	}
	usage, err := n.finish()
	if err != nil || calls != 2 || len(titles) != 2 || titles[1].Seq != 2 || len(usage) != 1 {
		t.Fatalf("calls %d titles %+v usage %+v err %v", calls, titles, usage, err)
	}
}

type functionNaming struct {
	ask func(context.Context, string) (string, error)
}

func (s *functionNaming) Ask(c context.Context, p string) (string, error) { return s.ask(c, p) }
func (s *functionNaming) Close() agent.Result {
	return agent.Result{Usage: []agent.Usage{{Model: "m", InputTokens: 2}}}
}

func TestNamingSaturationDoesNotBlockAndLaterCallsRemainEligible(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		began := make(chan struct{})
		release := make(chan struct{})
		session := &functionNaming{ask: func(ctx context.Context, _ string) (string, error) {
			select {
			case <-began:
			default:
				close(began)
			}
			select {
			case <-release:
			case <-ctx.Done():
				return "", ctx.Err()
			}
			var b strings.Builder
			for i := 1; i <= 20; i++ {
				fmt.Fprintf(&b, "%d. Inspect recorded call\n", i)
			}
			return b.String(), nil
		}}
		var last int32
		n := newStepNamer(context.Background(), func(context.Context) (toolNamingSession, error) { return session, nil }, func(meta agent.StepTitles) error {
			for _, title := range meta.Titles {
				last = title.Seq
			}
			return nil
		})
		n.saw(1, agent.Message{Type: "tool_use", Tool: "Bash", Content: []byte(`{"id":"first","input":{"command":"true"}}`)})
		<-began
		for seq := 2; seq <= 1000; seq++ {
			n.saw(int32(seq), agent.Message{Type: "tool_use", Tool: "Bash", Content: []byte(fmt.Sprintf(`{"id":"call-%d","input":{"command":"true"}}`, seq))})
		}
		close(release)
		time.Sleep(2 * stepSettle)
		n.saw(1001, agent.Message{Type: "tool_use", Tool: "Bash", Content: []byte(`{"id":"later","input":{"command":"true"}}`)})
		n.finish()
		if last != 1001 {
			t.Fatalf("later call lost: %d", last)
		}
	})
}
func TestNamingProviderFailureUsageIsNonfatal(t *testing.T) {
	calls := 0
	n := newStepNamer(context.Background(), func(context.Context) (toolNamingSession, error) {
		calls++
		return &scriptedNaming{err: errors.New("provider unavailable"), usage: []agent.Usage{{Model: "m", InputTokens: 3}}}, nil
	}, func(agent.StepTitles) error { t.Fatal("invented title"); return nil })
	n.saw(1, agent.Message{Type: "tool_use", Tool: "Bash", Content: []byte(`{"command":"true"}`)})
	usage, err := n.finish()
	if err != nil || calls != 2 || len(usage) != 1 || usage[0].InputTokens != 6 {
		t.Fatalf("usage %+v calls %d err %v", usage, calls, err)
	}
}

func TestNamingInvalidResponseRetryIsBounded(t *testing.T) {
	calls := 0
	n := newStepNamer(context.Background(), func(context.Context) (toolNamingSession, error) {
		return &functionNaming{ask: func(context.Context, string) (string, error) { calls++; return "not numbered", nil }}, nil
	}, func(agent.StepTitles) error { t.Fatal("invalid title persisted"); return nil })
	n.saw(1, agent.Message{Type: "tool_use", Tool: "Bash", Content: []byte(`{"id":"a","input":{"command":"true"}}`)})
	usage, err := n.finish()
	if calls != 2 || err != nil || len(usage) != 1 {
		t.Fatalf("calls %d usage %+v err %v", calls, usage, err)
	}
}
