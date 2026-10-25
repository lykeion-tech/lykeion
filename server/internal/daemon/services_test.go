package daemon

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"lykeion/server/internal/apiclient"
	"lykeion/server/internal/daemon/envs"
	"lykeion/server/internal/daemon/sandbox"
	"lykeion/server/internal/service"
	"lykeion/server/pkg/agent"
	"strconv"
)

func serviceFixture(t *testing.T) (*daemon, claimed, *envs.Study) {
	t.Helper()
	dir := t.TempDir()
	source := `package main
import("net";"os";"os/signal";"syscall";"strconv";"path/filepath";"strings";"os/exec";"time")
func main(){
 signal.Ignore(syscall.SIGTERM)
 if len(os.Args)>1&&os.Args[1]=="--child"{for{time.Sleep(time.Hour)}}
 child:=exec.Command(os.Args[0],"--child");if err:=child.Start();err!=nil{panic(err)}
 state:=os.Getenv("LYK_SERVICE_STATE")
 os.WriteFile(filepath.Join(state,"env"),[]byte(strings.Join(os.Environ(),"\n")),0600)
 b,_:=os.ReadFile(filepath.Join(state,"counter")); n,_:=strconv.Atoi(string(b)); os.WriteFile(filepath.Join(state,"counter"),[]byte(strconv.Itoa(n+1)),0600)
 os.WriteFile(filepath.Join(state,"pid"),[]byte(strconv.Itoa(os.Getpid())),0600)
 ln,err:=net.Listen("tcp4","127.0.0.1:"+os.Getenv("LYK_SERVICE_PORT"));if err!=nil{panic(err)}
 for { c,e:=ln.Accept();if e!=nil{return};c.Close() }
}`
	must(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte(source), 0600))
	cmd := exec.Command("go", "build", "-o", filepath.Join(dir, "helper"), filepath.Join(dir, "main.go"))
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOMAXPROCS=2")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("helper: %v %s", err, out)
	}
	binary, err := os.ReadFile(filepath.Join(dir, "helper"))
	must(t, err)
	var blob bytes.Buffer
	tw := tar.NewWriter(&blob)
	for name, data := range map[string][]byte{"helper": binary, "public/client.sh": []byte("#!/bin/sh\necho public\n"), "answers.txt": []byte("HIDDEN ANSWER")} {
		must(t, tw.WriteHeader(&tar.Header{Name: name, Mode: 0755, Size: int64(len(data))}))
		_, err = tw.Write(data)
		must(t, err)
	}
	must(t, tw.Close())
	c := claimed{Token: "ltk_agent", Env: map[string]string{"DATASET": "allowed", "OMITTED": "no", "LYK_SERVICE_PORT": "1", "NODE_OPTIONS": "bad"}}
	c.AgentRun.ID = uuid.New()
	c.AgentRun.WorkspaceID = uuid.New()
	card := uuid.New()
	c.AgentRun.CardID = &card
	c.AgentRun.BoardColumn = "experimentation"
	hash := sha256.Sum256(blob.Bytes())
	c.Services = []service.ClaimedService{{ID: uuid.New(), Name: "evaluator", Start: "exec ./helper", Client: "public/client.sh", Env: []string{"DATASET", "NODE_OPTIONS", "LYK_SERVICE_PORT"}, Sha256: hex.EncodeToString(hash[:]), SizeBytes: int64(blob.Len())}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/daemon/agent-runs/") {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"status":"running"}`))
			return
		}
		if r.URL.Path != "/api/daemon/runs/"+c.AgentRun.ID.String()+"/services/"+c.Services[0].ID.String() || r.Header.Get("Authorization") != "Bearer lyk_person" || r.Header.Get("X-Workspace-ID") != c.AgentRun.WorkspaceID.String() {
			t.Errorf("private fetch %s %v", r.URL.Path, r.Header)
			w.WriteHeader(404)
			return
		}
		w.Write(blob.Bytes())
	}))
	t.Cleanup(srv.Close)
	bases := &envs.Bases{Dir: filepath.Join(dir, "bases")}
	studyDir := filepath.Join(dir, "study")
	must(t, os.Mkdir(studyDir, 0700))
	study := envs.ForStudy(bases, studyDir)
	d := &daemon{cfg: Config{Server: srv.URL, Token: "lyk_person", Home: t.TempDir(), StateDir: t.TempDir()}, api: &apiclient.Client{Server: srv.URL, Token: "lyk_person"}, bases: bases, serviceReadyLimit: 2 * time.Second, serviceStopLimit: 50 * time.Millisecond}
	// These owned helpers exercise lifecycle, not the unresolved bind boundary.
	// They keep the actual filesystem sandbox while using its default network.
	d.serviceCommand = func(p sandbox.Policy, ctx context.Context, name string, args ...string) (*exec.Cmd, error) {
		if !p.LoopbackOnly {
			t.Error("production service did not request loopback-only policy")
		}
		p.LoopbackOnly = false
		return p.Command(ctx, name, args...)
	}
	t.Cleanup(func() { agent.RemoveTree(d.cfg.StateDir) })
	return d, c, study
}

func serviceState(d *daemon, c claimed) string {
	return filepath.Join(d.cfg.StateDir, "services", c.AgentRun.WorkspaceID.String(), c.AgentRun.CardID.String(), "evaluator", "state")
}

func TestServicesStartOnlyForExperimentation(t *testing.T) {
	d := &daemon{}
	c := claimed{Services: []service.ClaimedService{{Name: "broken"}}}
	for _, col := range []string{"grounding", "hypotheses", "validation", "reporting", ""} {
		c.AgentRun.BoardColumn = col
		s, err := d.startServices(context.Background(), c, nil)
		must(t, err)
		if len(s.Env()) != 0 {
			t.Fatal(s.Env())
		}
		s.Stop()
	}
}
func TestAServiceGetsItsPortStateAndNamedVariables(t *testing.T) {
	d, c, study := serviceFixture(t)
	t.Setenv("PERSON_SECRET", "private")
	s, err := d.startServices(context.Background(), c, study)
	must(t, err)
	defer s.Stop()
	b, err := os.ReadFile(filepath.Join(serviceState(d, c), "env"))
	must(t, err)
	got := string(b)
	for _, want := range []string{"DATASET=allowed", "LYK_SERVICE_STATE=" + serviceState(d, c), "LYK_SERVICE_PORT="} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q: %s", want, got)
		}
	}
	for _, bad := range []string{"OMITTED=", "PERSON_SECRET=", "NODE_OPTIONS=", "lyk_person", "ltk_agent", "LYK_SERVICE_PORT=1\n"} {
		if strings.Contains(got, bad) {
			t.Errorf("leaked %q: %s", bad, got)
		}
	}
}
func TestTheAgentGetsOnlyTheURL(t *testing.T) {
	d, c, study := serviceFixture(t)
	s, err := d.startServices(context.Background(), c, study)
	must(t, err)
	defer s.Stop()
	if len(s.Env()) != 1 || !strings.HasPrefix(s.Env()[0], "LYK_SERVICE_EVALUATOR_URL=http://127.0.0.1:") {
		t.Fatal(s.Env())
	}
}
func TestServiceStateSurvivesRuns(t *testing.T) {
	d, c, study := serviceFixture(t)
	for i := 0; i < 2; i++ {
		s, err := d.startServices(context.Background(), c, study)
		must(t, err)
		s.Stop()
	}
	b, err := os.ReadFile(filepath.Join(serviceState(d, c), "counter"))
	must(t, err)
	if string(b) != "2" {
		t.Fatal(string(b))
	}
}
func TestAServiceThatNeverListensFailsTheRun(t *testing.T) {
	d, c, study := serviceFixture(t)
	d.serviceReadyLimit = 100 * time.Millisecond
	c.Services[0].Start = "echo 'PRIVATE FAILURE'; exec sleep 30"
	_, err := d.startServices(context.Background(), c, study)
	if err == nil || err.Error() != "hidden service evaluator did not start; see the daemon log" {
		t.Fatal(err)
	}
}
func TestTheClientLandsInStudyBin(t *testing.T) {
	d, c, study := serviceFixture(t)
	s, err := d.startServices(context.Background(), c, study)
	must(t, err)
	defer s.Stop()
	f, err := os.Open(filepath.Join(study.Dir, "bin", "client.sh"))
	must(t, err)
	defer f.Close()
	b, err := io.ReadAll(f)
	must(t, err)
	fi, err := f.Stat()
	must(t, err)
	if string(b) != "#!/bin/sh\necho public\n" || fi.Mode().Perm() != 0555 {
		t.Fatalf("client %s %v", b, fi.Mode())
	}
}
func TestServiceClientCannotFollowAStudySymlink(t *testing.T) {
	for _, part := range []string{"bin", "file"} {
		t.Run(part, func(t *testing.T) {
			d, c, study := serviceFixture(t)
			outside := t.TempDir()
			target := filepath.Join(outside, "client.sh")
			must(t, os.WriteFile(target, []byte("untouched"), 0600))
			if part == "bin" {
				must(t, os.Symlink(outside, filepath.Join(study.Dir, "bin")))
			} else {
				must(t, os.Mkdir(filepath.Join(study.Dir, "bin"), 0700))
				must(t, os.Symlink(target, filepath.Join(study.Dir, "bin", "client.sh")))
			}
			s, err := d.startServices(context.Background(), c, study)
			if s != nil {
				s.Stop()
			}
			if err == nil {
				t.Fatal("accepted escaping symlink")
			}
			b, err := os.ReadFile(target)
			must(t, err)
			if string(b) != "untouched" {
				t.Fatal("changed outside file")
			}
		})
	}
}
func TestServicesStopWithTheRun(t *testing.T) {
	d, c, study := serviceFixture(t)
	s, err := d.startServices(context.Background(), c, study)
	must(t, err)
	url := strings.TrimPrefix(strings.SplitN(s.Env()[0], "=", 2)[1], "http://")
	s.Stop()
	s.Stop()
	for _, child := range s.children {
		waitFor(t, func() bool { return !serviceGroupAlive(child.cmd) })
	}
	if conn, err := net.DialTimeout("tcp4", url, 100*time.Millisecond); err == nil {
		conn.Close()
		t.Fatal("service still listening")
	}
}

func TestConcurrentServicesKeepIndependentProcesses(t *testing.T) {
	d, c, study := serviceFixture(t)
	first, err := d.startServices(context.Background(), c, study)
	must(t, err)
	defer first.Stop()
	c.AgentRun.ID = uuid.New()
	second, err := d.startServices(context.Background(), c, study)
	must(t, err)
	if first.Env()[0] == second.Env()[0] {
		t.Fatal("services reused a process/port")
	}
	second.Stop()
	address := strings.TrimPrefix(strings.SplitN(first.Env()[0], "=", 2)[1], "http://")
	conn, err := net.DialTimeout("tcp4", address, time.Second)
	must(t, err)
	conn.Close()
	b, err := os.ReadFile(filepath.Join(filepath.Dir(serviceState(d, c)), "code", "answers.txt"))
	must(t, err)
	if string(b) != "HIDDEN ANSWER" {
		t.Fatal("live service's code changed")
	}
}
func TestServiceLogKeepsOnlyABoundedPrivateTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log")
	first := &serviceLog{path: path}
	second := &serviceLog{path: path}
	_, err := first.Write(bytes.Repeat([]byte("a"), 2*serviceLogBytes))
	must(t, err)
	_, err = second.Write([]byte("ending"))
	must(t, err)
	b, err := os.ReadFile(path)
	must(t, err)
	if len(b) != serviceLogBytes || !strings.HasSuffix(string(b), "ending") || len(first.String()) != serviceLogBytes {
		t.Fatalf("tail length %d", len(b))
	}
}
func TestServiceCodeLockHonorsCancellation(t *testing.T) {
	unlock, err := lockServiceCode(context.Background(), "test service lock")
	must(t, err)
	defer unlock()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = lockServiceCode(ctx, "test service lock"); err != context.Canceled {
		t.Fatal(err)
	}
}
func TestServiceArchiveHashFailureLeavesNoCodeOrPartial(t *testing.T) {
	d, c, study := serviceFixture(t)
	c.Services[0].Sha256 = strings.Repeat("0", 64)
	s, err := d.startServices(context.Background(), c, study)
	if s != nil {
		s.Stop()
	}
	if err == nil {
		t.Fatal("accepted invalid digest")
	}
	root := filepath.Dir(serviceState(d, c))
	entries, err := os.ReadDir(root)
	must(t, err)
	if len(entries) != 0 {
		t.Fatalf("left private partials: %v", entries)
	}
	if _, err = os.Stat(filepath.Join(d.cfg.StateDir, "attachments")); !os.IsNotExist(err) {
		t.Fatal("service entered attachment cache")
	}
}
func TestTheDoorGetsServiceURLsWithoutPrivateServiceState(t *testing.T) {
	l := newLykeion(t)
	door := newRunDoor(t, l, t.TempDir(), func(d *Door) { d.ServiceEnv = []string{"LYK_SERVICE_EVALUATOR_URL=http://127.0.0.1:12345"} }, envs.Python)
	must(t, door.sandboxReady())
	got, err := door.runEnv(context.Background())
	must(t, err)
	if value, _ := lookup(got, "LYK_SERVICE_EVALUATOR_URL"); value != "http://127.0.0.1:12345" {
		t.Fatal(got)
	}
	if _, present := lookup(got, "LYK_SERVICE_STATE"); present {
		t.Fatal(got)
	}
}

func TestServiceStartupFailureStopsEarlierServices(t *testing.T) {
	d, c, study := serviceFixture(t)
	d.serviceReadyLimit = 2 * time.Second
	second := c.Services[0]
	second.Name = "broken"
	second.Client = ""
	second.Start = "echo PRIVATE_DIAGNOSTIC; exec sleep 30"
	c.Services = append(c.Services, second)
	_, err := d.startServices(context.Background(), c, study)
	if err == nil || err.Error() != "hidden service broken did not start; see the daemon log" {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(serviceState(d, c), "pid"))
	must(t, err)
	pid, err := strconv.Atoi(string(b))
	must(t, err)
	proc, err := os.FindProcess(pid)
	must(t, err)
	if serviceGroupAlive(&exec.Cmd{Process: proc}) {
		t.Fatal("earlier service group survived later startup failure")
	}
}
func TestServicesStopAfterWholeRunCompletion(t *testing.T) {
	d, c, _ := serviceFixture(t)
	runtimeID := uuid.New()
	c.AgentRun.RuntimeID = &runtimeID
	c.AgentRun.Attempt = 1
	c.AgentRun.Harness = "claude"
	d.out = &Outbox{Dir: filepath.Join(d.cfg.StateDir, "outbox")}
	d.messages = &Outbox{Dir: filepath.Join(d.cfg.StateDir, "messages")}
	launched := false
	address := ""
	d.cfg.NewBackend = func(string, string) (agent.Backend, error) {
		return backendFunc(func(ctx context.Context, _ string, o agent.ExecOptions) (*agent.Session, error) {
			launched = true
			value, ok := lookup(o.Env, "LYK_SERVICE_EVALUATOR_URL")
			if !ok {
				t.Fatal("CLI missing service URL")
			}
			address = strings.TrimPrefix(value, "http://")
			conn, err := net.DialTimeout("tcp4", address, time.Second)
			must(t, err)
			conn.Close()
			for _, key := range []string{"LYK_SERVICE_STATE", "LYK_SERVICE_PORT", "VIRTUAL_ENV", "JULIA_PROJECT"} {
				if _, ok := lookup(o.Env, key); ok {
					t.Errorf("CLI got %s", key)
				}
			}
			if path, _ := lookup(o.Env, "PATH"); strings.Contains(path, d.bases.Dir) {
				t.Error("CLI got study PATH")
			}
			messages := make(chan agent.Message)
			close(messages)
			done := make(chan agent.Result, 1)
			done <- agent.Result{}
			close(done)
			return &agent.Session{Messages: messages, Done: done}, nil
		}), nil
	}
	must(t, d.runAgentRun(t.Context(), c))
	report, err := readReport(d.out.file(Report{AgentRunID: c.AgentRun.ID, Attempt: c.AgentRun.Attempt}))
	must(t, err)
	if !launched || report.Kind != "complete" {
		t.Fatalf("launch %v report %+v", launched, report)
	}
	if conn, err := net.DialTimeout("tcp4", address, time.Second); err == nil {
		conn.Close()
		t.Fatal("service survived whole run")
	}
}

func TestServiceCancellationStopsItsGroup(t *testing.T) {
	d, c, study := serviceFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	s, err := d.startServices(ctx, c, study)
	must(t, err)
	cancel()
	s.Stop()
	for _, child := range s.children {
		waitFor(t, func() bool { return !serviceGroupAlive(child.cmd) })
	}
}
func TestServiceStartupRefusesUnavailableSandbox(t *testing.T) {
	d, c, study := serviceFixture(t)
	d.sandboxErr = fmt.Errorf("sandbox unavailable")
	_, err := d.startServices(context.Background(), c, study)
	if err == nil || err.Error() != "hidden service evaluator did not start; see the daemon log" {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(d.cfg.StateDir, "services")); !os.IsNotExist(err) {
		t.Fatal("prepared service despite unavailable sandbox")
	}
}

func TestServicesStopAfterWholeRunLaunchFailure(t *testing.T) {
	d, c, _ := serviceFixture(t)
	runtimeID := uuid.New()
	c.AgentRun.RuntimeID = &runtimeID
	c.AgentRun.Attempt = 1
	c.AgentRun.Harness = "claude"
	d.out = &Outbox{Dir: filepath.Join(d.cfg.StateDir, "outbox")}
	d.messages = &Outbox{Dir: filepath.Join(d.cfg.StateDir, "messages")}
	address := ""
	d.cfg.NewBackend = func(string, string) (agent.Backend, error) {
		return backendFunc(func(_ context.Context, _ string, o agent.ExecOptions) (*agent.Session, error) {
			value, _ := lookup(o.Env, "LYK_SERVICE_EVALUATOR_URL")
			address = strings.TrimPrefix(value, "http://")
			return nil, fmt.Errorf("owned backend failed after services started")
		}), nil
	}
	must(t, d.runAgentRun(t.Context(), c))
	report, err := readReport(d.out.file(Report{AgentRunID: c.AgentRun.ID, Attempt: c.AgentRun.Attempt}))
	must(t, err)
	if address == "" || report.Kind != "fail" {
		t.Fatalf("address %q report %+v", address, report)
	}
	if conn, err := net.DialTimeout("tcp4", address, time.Second); err == nil {
		conn.Close()
		t.Fatal("service survived later launch failure")
	}
}
func TestServiceFailureReportContainsNoPrivateDiagnostics(t *testing.T) {
	d, c, _ := serviceFixture(t)
	c.Services[0].Start = "echo PRIVATE_ANSWERS; exit 1"
	runtimeID := uuid.New()
	c.AgentRun.RuntimeID = &runtimeID
	c.AgentRun.Attempt = 1
	c.AgentRun.Harness = "claude"
	d.out = &Outbox{Dir: filepath.Join(d.cfg.StateDir, "outbox")}
	d.messages = &Outbox{Dir: filepath.Join(d.cfg.StateDir, "messages")}
	d.cfg.NewBackend = func(string, string) (agent.Backend, error) {
		return backendFunc(func(context.Context, string, agent.ExecOptions) (*agent.Session, error) {
			t.Error("CLI started after service failure")
			return nil, fmt.Errorf("unexpected launch")
		}), nil
	}
	must(t, d.runAgentRun(t.Context(), c))
	report, err := readReport(d.out.file(Report{AgentRunID: c.AgentRun.ID, Attempt: c.AgentRun.Attempt}))
	must(t, err)
	if report.Kind != "fail" || report.Error != "hidden service evaluator did not start; see the daemon log" {
		t.Fatalf("private diagnostics reached report: %+v", report)
	}
	entries, err := os.ReadDir(d.messages.Dir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatal("service diagnostics reached agent messages")
	}
}

func TestServiceLeaderExitStopsItsDescendants(t *testing.T) {
	d, c, study := serviceFixture(t)
	c.Services[0].Start = "./helper & sleep 0.6"
	s, err := d.startServices(context.Background(), c, study)
	must(t, err)
	defer s.Stop()
	select {
	case <-s.children[0].done:
	case <-time.After(12 * time.Second):
		t.Fatal("service leader was not reaped")
	}
	waitFor(t, func() bool { return !serviceGroupAlive(s.children[0].cmd) })
}

func TestServiceForgedClientCollisionFailsBeforeAnyStartup(t *testing.T) {
	d, c, study := serviceFixture(t)
	second := c.Services[0]
	second.ID = uuid.New()
	second.Name = "second"
	second.Client = "other/CLIENT.SH"
	c.Services = append(c.Services, second)
	s, err := d.startServices(context.Background(), c, study)
	if s != nil {
		s.Stop()
	}
	if err == nil {
		t.Fatal("accepted colliding public clients")
	}
	if _, err = os.Stat(filepath.Join(d.cfg.StateDir, "services")); !os.IsNotExist(err) {
		t.Fatal("downloaded or started a service before refusing forged collision")
	}
}
