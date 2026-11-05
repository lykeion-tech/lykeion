package daemon

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"lykeion/server/internal/apiclient"
	studyarchive "lykeion/server/internal/archive"
	"lykeion/server/internal/daemon/envs"
	"lykeion/server/internal/middleware"
	"lykeion/server/internal/records"
	"lykeion/server/internal/service"
	"lykeion/server/internal/toolroute"
	"lykeion/server/pkg/agent"
)

var errFenceBreach = errors.New("tool call outside the fence")

func (d *daemon) runAgentRun(ctx context.Context, c claimed) (localErr error) {
	if c.Coresearch != nil {
		return d.runCoresearchTurn(ctx, c)
	}
	if c.AgentRun.Kind == "rerun" {
		return d.runRerun(ctx, c)
	}
	var runtimeID uuid.UUID
	if c.AgentRun.RuntimeID != nil {
		runtimeID = *c.AgentRun.RuntimeID
	}
	r := Report{AgentRunID: c.AgentRun.ID, WorkspaceID: c.AgentRun.WorkspaceID, RuntimeID: runtimeID, Attempt: c.AgentRun.Attempt, Kind: "complete"}
	dir := filepath.Join(d.cfg.Home, scratchName(r.AgentRunID, r.Attempt))
	created := false
	var cont *continuity
	var final agent.Result
	// A reason already set stays unless the new failure names an infrastructure cause.
	fail := func(err error) { failReport(&r, c.AgentRun.Harness, err) }
	// Registered before the deferred report, so it runs after the finished
	// marker: gc never finds the folder unheld with only an older marker.
	var reservation *agent.ScratchReservation
	defer func() { reservation.Release() }()

	// Journal before the server can enter running or any CLI can launch.
	journalRecord := r
	journalRecord.Kind = "fail"
	journalRecord.FailureReason = agent.ReasonRuntimeOffline
	if err := d.journal().Put(journalRecord); err != nil {
		return err
	}
	defer func() {
		// A kept hand-in is a finished answer: a stopping daemon keeps it.
		if ctx.Err() != nil && r.HandIn == nil {
			r.Kind = "fail"
			r.Error = ctx.Err().Error()
			r.FailureReason = agent.ReasonRuntimeOffline
		}
		if err := d.out.Put(r); err != nil {
			localErr = errors.Join(localErr, err)
			return
		}
		// Only after the final report is durable may a baseline become resumable.
		localErr = errors.Join(localErr, d.finishContinuity(cont, final))
		if created {
			if err := d.markFinished(dir, map[string]any{"ended_at": time.Now().UTC(), "attempt": r.Attempt, "retention_days": journalRecord.RetentionDays}); err != nil {
				localErr = errors.Join(localErr, err)
				return
			}
			// The run saved what it keeps as outputs; what its code installed
			// here can be gigabytes. A folder still there is left to gc; one
			// retired but not wholly deleted is gc's too, under its new name.
			if reservation != nil {
				reservation.Remove()
				if _, err := os.Lstat(dir); errors.Is(err, os.ErrNotExist) {
					localErr = errors.Join(localErr, d.unmarkFinished(dir))
				}
			}
		}
		localErr = errors.Join(localErr, d.forgetAttempt(r))
	}()
	d.mu.Lock()
	owner, knownOwner := d.ownerOf[r.RuntimeID]
	d.mu.Unlock()
	if !knownOwner || owner == uuid.Nil {
		fail(errors.New("unknown runtime owner"))
		return nil
	}
	if c.AgentRun.Harness == "cursor" || c.AgentRun.Harness == "antigravity" {
		fail(agent.ErrHarnessRemoved)
		return nil
	}
	guest := ""
	if c.AgentRun.ResponsibleUserID != owner {
		guest = c.AgentRun.ResponsibleUserID.String()
	}
	lab := c.AgentRun.Kind == "aristotle"
	if lab {
		if err := agent.LabAvailability(c.AgentRun.Harness); err != nil {
			fail(err)
			return nil
		}
	}
	if lab && (guest != "" || c.OutputSchema == "") {
		fail(errors.New("A lab question requires the asker's own machine and a structured hand-in."))
		return nil
	}
	if guest != "" && runtime.GOOS != "linux" {
		message := "shared runs require Linux: process environment isolation is not enforced"
		if runtime.GOOS == "darwin" {
			message = "shared runs are unavailable on macOS: process environment isolation is not enforced"
		}
		fail(errors.New(message))
		return nil
	}
	// The attempt keeps this snapshot even if the policy file changes later.
	pol, err := LoadPolicy(d.cfg.StateDir, c.AgentRun.WorkspaceID)
	if err != nil {
		fail(fmt.Errorf("workspace policy: %w", err))
		return nil
	}
	r.Mode, r.PolicyDigest = ModeSandboxed, pol.Digest
	err = os.Mkdir(dir, 0700)
	if err != nil {
		fail(fmt.Errorf("prepare scratch: %w", err))
		return nil
	}
	created = true
	reservation, err = agent.ReserveScratch(dir)
	if err != nil {
		fail(fmt.Errorf("reserve scratch: %w", err))
		return nil
	}
	prompt := c.AgentRun.Prompt
	if c.AgentRun.Skill != "" {
		if err := os.WriteFile(filepath.Join(dir, roleFile(c.AgentRun.Harness)), []byte(c.AgentRun.Skill), 0600); err != nil {
			fail(err)
			return nil
		}
	}
	backend, err := d.cfg.NewBackend(c.AgentRun.Harness, d.cfg.Harnesses[c.AgentRun.Harness])
	if err != nil {
		fail(err)
		return nil
	}
	base := "/api/daemon/agent-runs/" + r.AgentRunID.String()
	ref := reportBody(r)
	if err = d.api.Do(ctx, "POST", base+"/prepare-lease", ref, nil); err != nil {
		fail(err)
		if retryableControlLoss(err) {
			r.FailureReason = agent.ReasonRuntimeOffline
		}
		return nil
	}
	startPath := base + "/start"
	var launch struct{ Prompt string }
	var startResponse any
	if lab {
		startPath += "?lab_history=1"
		startResponse = &launch
	}
	if err = d.api.Do(ctx, "POST", startPath, ref, startResponse); err != nil {
		fail(err)
		if retryableControlLoss(err) {
			r.FailureReason = agent.ReasonRuntimeOffline
		}
		return nil
	}
	if lab {
		if launch.Prompt == "" {
			fail(errors.New("the server did not provide a checked lab prompt"))
			return nil
		}
		prompt = c.AgentRun.Skill + "\n\n" + launch.Prompt
		// Only re-asks inside this live turn may resume its CLI session.
		c.AgentRun.SessionID, c.AgentRun.ContinuityScope = "", ""
	}
	// The run's token fetches the files, so this follows the start; the CLI
	// never starts with only some of them.
	if err = d.placeAttachments(ctx, c, dir); err != nil {
		fail(err)
		if retryableControlLoss(err) {
			r.FailureReason = agent.ReasonRuntimeOffline
		}
		return nil
	}
	if err = placeClaimedSkills(c.AgentRun.Harness, dir, c.Skills); err != nil {
		fail(err)
		return nil
	}
	prompt += skillsNote(c.Skills)
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	watched := make(chan struct{})
	go func(ref Report) { defer close(watched); d.watchCancel(runCtx, cancel, base, ref) }(r)
	defer func() { cancel(); <-watched }()
	var sinkErr error
	var sequence int32
	var lastTextSeq int32
	var sequenceMu sync.Mutex
	messageRef := Report{AgentRunID: r.AgentRunID, WorkspaceID: r.WorkspaceID, RuntimeID: r.RuntimeID, Attempt: r.Attempt}
	var namer *stepNamer
	appendMessage := func(m agent.Message) (int32, error) {
		sequenceMu.Lock()
		defer sequenceMu.Unlock()
		if sequence == math.MaxInt32 {
			return 0, errors.New("message sequence exhausted")
		}
		sequence++
		return sequence, d.persistMessage(messageRef, sequence, m)
	}
	// breach names the first tool call outside the fence. The call is kept
	// for the person, and the error stops the CLI before its work goes on.
	var breach string
	// doorToken is blanked out of every message before it is kept: the agent's
	// shell can read it from its MCP config or environment, and a transcript
	// is read by the whole workspace.
	var doorToken []byte
	fence := agent.Fenced
	if lab {
		fence = agent.LabFenced
	}
	sink := func(m agent.Message) error {
		if len(doorToken) > 0 {
			m.Content = bytes.ReplaceAll(m.Content, doorToken, []byte("[door token]"))
		}
		seq, err := appendMessage(m)
		if err != nil {
			sinkErr = errors.Join(sinkErr, err)
			return err
		}
		if m.Type == "text" {
			lastTextSeq = seq
		}
		if namer != nil {
			namer.saw(seq, m)
		}
		if fenced, what := fence(c.AgentRun.Harness, m); fenced {
			breach = what
			return errFenceBreach
		}
		return nil
	}
	// The study's add-on folders are the door's alone: code runs only
	// through it, in the sandbox, so the CLI's environment holds none of the
	// study's interpreters or folders, which the study's code may have planted.
	var study *envs.Study
	if d.bases != nil {
		// A study's runs share its add-on folders. A run on no card keeps its
		// own beside them, never in its run folder: code may write the run
		// folder, and a link it left there would be the next run's folder.
		// An attempt's own goes when the run ends.
		studies := filepath.Join(d.bases.Dir, "studies", c.AgentRun.WorkspaceID.String())
		studyDir := filepath.Join(studies, "_run", filepath.Base(dir))
		switch {
		case c.AgentRun.CardID != nil:
			studyDir = filepath.Join(studies, c.AgentRun.CardID.String())
		default:
			defer os.RemoveAll(studyDir)
		}
		if err = os.MkdirAll(studyDir, 0700); err != nil {
			fail(fmt.Errorf("prepare study folder: %w", err))
			return nil
		}
		study = envs.ForStudy(d.bases, studyDir)
		study.PythonVersion = c.PythonVersion
	}
	services, err := d.startServices(runCtx, c, study, guest != "")
	if err != nil {
		fail(err)
		return nil
	}
	defer services.Stop()
	opts := agent.ExecOptions{Lab: lab, ReadOnly: lab, Guest: guest != "", ScratchReservation: reservation, OnMessage: sink, Dir: dir, Resume: c.AgentRun.SessionID, Model: c.AgentRun.RequestedModel, OutputSchema: c.OutputSchema}
	if c.OutputSchema == "" {
		opts.MCPToken = c.Token
	}
	if opts.Env, err = cliEnv(c.OutputSchema != "", c.Token, d.cfg.Server, dir); err != nil {
		fail(err)
		return nil
	}
	opts.Env = append(opts.Env, services.Env()...)
	opts.CacheDir = d.runCacheDir(guest)
	if guest != "" {
		opts.ConfigHome = filepath.Join(d.cfg.StateDir, "guests", guest, c.AgentRun.Harness)
		if err = agent.PrepareGuestHome(c.AgentRun.Harness, agent.AccountHome(c.AgentRun.Harness), opts.ConfigHome); err != nil {
			fail(err)
			return nil
		}
		auth, authErr := agent.GuestAuthEnv(runCtx, c.AgentRun.Harness)
		if authErr != nil {
			fail(authErr)
			return nil
		}
		opts.Env = append(opts.Env, auth...)
	}
	opts.Sandbox = d.cliSandbox(dir, study, opts.ReadOnly, guest)
	if study != nil {
		opts.ReadDirs = append([]string{study.Dir}, d.bases.Folders()...)
	}
	// The CLI's one MCP server; add-on servers are reached through it too. The
	// call token stays with the door. Child reads opts when a local command
	// starts, after any change below.
	private, err := d.privatePaths()
	if err != nil {
		fail(err)
		return nil
	}
	door := &Door{Lab: lab, labURL: d.labURL(), Guest: guest != "", Leader: c.Leader, HandIn: c.OutputSchema != "", Server: d.cfg.Server, Sources: d.sourcesURL(), RunToken: c.Token, CallToken: c.CallToken, Connectors: c.Connectors, WorkspaceConnectors: c.WorkspaceConnectors, Addons: c.MCP, Harness: c.AgentRun.Harness, Study: study, ServiceEnv: services.Env(), PrivatePaths: private, PrivateRoots: []string{d.cfg.Home},
		SandboxErr: d.sandboxErr, Child: func() agent.ExecOptions {
			if guest != "" {
				return guestDoorOptions(opts)
			}
			return opts
		}}
	if err = door.Start(); err != nil {
		fail(err)
		return nil
	}
	defer door.Close()
	doorToken = []byte(door.Token)
	opts.DoorURL, opts.DoorToken = door.URL, door.Token
	opts.Env = append(opts.Env, doorEnv(c.OutputSchema != "", c.AgentRun.Harness, door)...)
	if cont, err = d.beginContinuity(c, pol); err != nil {
		fail(err)
		return nil
	}
	note := func(text string) error {
		return sink(agent.Message{Type: "status", Content: rawJSON(map[string]string{"type": "continuity", "message": text})})
	}
	if cont != nil {
		opts.Resume, opts.ClaudeBaseline = cont.resume, cont.baseline
		if cont.note != "" {
			if err = note(cont.note); err != nil {
				fail(err)
				return errors.Join(err, d.abandonContinuity(cont))
			}
		}
	}
	// Resume only what this run's CLI can find; otherwise continue fresh.
	if opts.Resume != "" && !agent.SessionReachable(c.AgentRun.Harness, opts.Resume, opts.ConfigHome) {
		if err = note("session " + opts.Resume + " is not in the CLI's session store; starting a fresh session"); err != nil {
			fail(err)
			return errors.Join(err, d.abandonContinuity(cont))
		}
		opts.Resume, opts.ClaudeBaseline = "", nil
	}

	if !lab {
		namer = newStepNamer(runCtx, func(ctx context.Context) (toolNamingSession, error) { return d.openToolNamer(ctx, c, opts) }, func(titles agent.StepTitles) error {
			_, err := appendMessage(agent.Message{Type: "status", Content: rawJSON(titles)})
			return err
		})
		defer func() { usage, err := namer.finish(); r.NamingUsage = usage; localErr = errors.Join(localErr, err) }()
	}
	sandboxNoted := false
	sandboxNote := func(runErr error) error {
		if runErr == nil || sandboxNoted {
			return nil
		}
		if hint := sandboxHint(runErr.Error()); hint != "" {
			sandboxNoted = true
			return sink(agent.Message{Type: "status", Content: rawJSON(map[string]string{"type": "sandbox", "message": hint})})
		}
		return nil
	}
	// A Claude turn that hands in takes comments while it runs. Codex can't
	// take input mid-turn: its comments wait for the leader or the next run.
	takesComments := c.AgentRun.Harness == "claude" && c.OutputSchema != "" && c.AgentRun.CardID != nil
	// Recheck cancellations immediately before launch, after setup. The frozen
	// prompt does not absorb comments posted after enqueue.
	var launchComments service.CommentLaunch
	if c.HasCommentSnapshot || len(c.ReservedCommentIDs) > 0 {
		if err := d.api.Do(runCtx, "POST", base+"/comments/launch", ref, &launchComments); err != nil {
			fail(err)
			return err
		}
		prompt = launchComments.Prompt + skillsNote(c.Skills)
	}
	ackComments := func() error {
		if len(launchComments.CommentIDs) == 0 {
			return nil
		}
		body := reportBody(r)
		body["comment_ids"] = launchComments.CommentIDs
		err := d.api.Do(runCtx, "POST", base+"/comments/ack", body, nil)
		if err == nil {
			launchComments.CommentIDs = nil
		}
		return err
	}
	// execute returns the launch error, or the result and the stream error.
	execute := func() (res agent.Result, streamErr, launchErr error) {
		lastTextSeq = 0
		turnOpts := opts
		if takesComments {
			input := make(chan string, 16)
			turnCtx, endTurn := context.WithCancel(runCtx)
			defer endTurn()
			turnOpts.Input = input
			go d.relayComments(turnCtx, base, messageRef, input)
		}
		sess, err := backend.Execute(runCtx, prompt, turnOpts)
		if err != nil {
			return res, sandboxNote(err), err
		}
		for message := range sess.Messages {
			if err := sink(message); err != nil {
				if !errors.Is(err, errFenceBreach) {
					streamErr = errors.Join(streamErr, err)
				}
				cancel()
			}
		}
		res, ok := <-sess.Done
		if !ok {
			res.Err = errors.New("backend closed without a result")
		}
		// Codex's adapter defines Output as the last emitted agent_message.
		if c.AgentRun.Harness == "codex" && res.Output != "" {
			res.OutputMessageSeq = lastTextSeq
		}
		// Execute only starts the process. Even text can be a user echo or
		// rejection; a clean terminal result is our conservative acceptance
		// signal. Keep uncertain failed sessions pending for recovery. Commit
		// delivery before hand-in/settlement can schedule another continuation.
		if res.Err == nil && !res.ResumeRefused {
			if err := ackComments(); err != nil {
				streamErr = errors.Join(streamErr, ackComments())
			}
		}
		streamErr = errors.Join(streamErr, sandboxNote(res.Err))
		return res, streamErr, nil
	}
	res, streamErr, launchErr := execute()
	if launchErr != nil {
		fail(launchErr)
		return errors.Join(d.abandonContinuity(cont), streamErr)
	}
	// A refused resume did no model work, so one fresh session is safe.
	if cont != nil && opts.Resume != "" && res.ResumeRefused && streamErr == nil && sinkErr == nil && runCtx.Err() == nil {
		if err = note("the CLI refused to resume session " + opts.Resume + "; starting a fresh session"); err == nil {
			opts.Resume, opts.ClaudeBaseline = "", nil
			if res, streamErr, launchErr = execute(); launchErr != nil {
				fail(launchErr)
				final.Err = launchErr
				return streamErr
			}
		}
	}
	// The hand-in follows only a turn that ended cleanly inside the fence.
	var handInProblems []string
	var handInErr error
	if c.OutputSchema != "" && res.Err == nil && streamErr == nil && sinkErr == nil && breach == "" && runCtx.Err() == nil {
		// A resumed Claude turn counts its usage from the last turn's snapshot.
		snapshot := res.ClaudeSnapshot
		reask := func(p, resume string) agent.Result {
			prompt, opts.Resume, opts.ClaudeBaseline = p, resume, snapshot
			next, more, launchErr := execute()
			if next.ClaudeSnapshot != nil {
				snapshot = next.ClaudeSnapshot
			}
			streamErr = errors.Join(streamErr, more)
			if next.Err == nil {
				next.Err = errors.Join(launchErr, more)
			}
			if next.Err == nil && breach != "" {
				next.Err = errFenceBreach
			}
			return next
		}
		res, handInProblems, handInErr = d.handIn(runCtx, c, res, reask, sink)
	}
	final = res
	if final.Err == nil && streamErr != nil {
		final.Err = streamErr
	}
	// A breached session is never resumed as ready.
	if final.Err == nil && breach != "" {
		final.Err = errFenceBreach
	}
	if sinkErr != nil {
		var oversized *oversizedMessageError
		if !errors.As(sinkErr, &oversized) {
			localErr = errors.Join(localErr, sinkErr)
		}
	}
	r.Usage, r.SessionID, r.Model = res.Usage, res.SessionID, res.Model
	if res.Err != nil {
		failResult(&r, c.AgentRun.Harness, res)
	}
	if streamErr != nil {
		fail(streamErr)
		var oversized *oversizedMessageError
		if !errors.As(streamErr, &oversized) {
			localErr = streamErr
		}
	}
	// The hand-in step's own failures are the machine's or the server's,
	// never the agent's: the reason is set first so no text match relabels it.
	// A hand-in the server could not take for now waits with the report in
	// the outbox, which posts it until the server answers; any other failure
	// of the step parks the run. A run cancelled meanwhile keeps nothing.
	var unsent *unsentHandIn
	switch {
	case errors.As(handInErr, &unsent) && waitsForServer(unsent.err) && r.Kind == "complete" && breach == "" && (runCtx.Err() == nil || ctx.Err() != nil):
		r.HandIn = handInJSON(res.Output)
		r.HandInMessageSeq = res.OutputMessageSeq
		slog.Warn("hand-in kept until the server answers", "run", r.AgentRunID, "attempt", r.Attempt, "err", handInErr)
	case errors.Is(handInErr, errLabContextChanged):
		r.FailureReason = agent.ReasonLabContextChanged
		fail(handInErr)
	case handInErr != nil:
		r.FailureReason = agent.ReasonRuntimeOffline
		fail(handInErr)
	}
	if len(handInProblems) > 0 {
		r.Kind, r.FailureReason, r.Error = "fail", agent.ReasonHandInInvalid, strings.Join(handInProblems, "\n")
	}
	// A breach outranks whatever the stopped CLI reported; the text is for
	// the person and never reaches the agent.
	if breach != "" {
		fail(fmt.Errorf("used %s, which is outside Lykeion's sources", breach))
		r.FailureReason = agent.ReasonFenceBreach
	}
	if runCtx.Err() != nil && ctx.Err() == nil {
		// The ACK is informational; durable replay carries final usage.
		if err = d.api.Do(ctx, "POST", base+"/cancel-ack", ref, nil); err != nil {
			slog.Warn("cancellation acknowledgement pending", "run", r.AgentRunID, "attempt", r.Attempt, "err", err)
		}
	}
	return localErr
}

// placeAttachments puts the study's files in the run folder's attachments
// folder, each read-only; the sandbox keeps the folder read-only too. Files
// come through a cache in the daemon's state folder, keyed by sha256, which
// no run can see. Verified size and mtime avoid hashing unchanged files.
// A changed file is checked and fetched again if needed. The run's copy is a
// hard link to the cached file where the two share a file system: neither
// path is writable in the sandbox, and Seatbelt refuses a link to a path it
// denies writing, while
// bubblewrap's read-only mount keeps a link from leaving it, so code can't
// reach the cached file through another name.
func (d *daemon) placeAttachments(ctx context.Context, c claimed, dir string) error {
	if len(c.Attachments) == 0 {
		return nil
	}
	cache := filepath.Join(d.cfg.StateDir, "cache", "attachments")
	if err := os.MkdirAll(cache, 0o700); err != nil {
		return fmt.Errorf("attached files: %w", err)
	}
	into := filepath.Join(dir, "attachments")
	if err := os.Mkdir(into, 0o700); err != nil {
		return fmt.Errorf("attached files: %w", err)
	}
	// The server bounds a file's download; the daemon's client timeout would
	// cut a large one short.
	api := &apiclient.Client{Server: d.cfg.Server, Token: c.Token, HTTP: &http.Client{}}
	names := make([]string, len(c.Attachments))
	for i, a := range c.Attachments {
		names[i] = a.DiskName
	}
	var mappings []string
	for _, a := range c.Attachments {
		folder, err := placeAttachment(ctx, api, cache, into, a, names)
		if err != nil {
			return fmt.Errorf("attached file %q: %w", a.DiskName, err)
		}
		if folder != "" {
			names = append(names, folder)
			mappings = append(mappings, fmt.Sprintf("- %q is unpacked at %q.", "attachments/"+a.DiskName, "attachments/"+folder+"/"))
		}
	}
	if len(mappings) != 0 {
		f, err := os.OpenFile(filepath.Join(dir, roleFile(c.AgentRun.Harness)), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(f, "\n\nArchive attachments (read-only):\n%s\n", strings.Join(mappings, "\n"))
		return errors.Join(err, f.Close())
	}
	return nil
}

func placeAttachment(ctx context.Context, api *apiclient.Client, cache, into string, a service.ClaimedAttachment, names []string) (string, error) {
	// The server sanitised the name; it is checked again before it names a path.
	if a.DiskName == "." || !filepath.IsLocal(a.DiskName) || strings.ContainsAny(a.DiskName, "/\\\x00") {
		return "", errors.New("unsafe file name")
	}
	if sum, err := hex.DecodeString(a.Sha256); err != nil || len(sum) != sha256.Size || a.SizeBytes < 0 || a.UnpackedBytes < 0 || a.InnerFiles < 0 {
		return "", errors.New("invalid digest or size")
	}
	cached := filepath.Join(cache, a.Sha256)
	release := lockAttachment(cached)
	defer release()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if !matches(cached, a) {
		if err := fetchAttachment(ctx, api, cache, a); err != nil {
			return "", err
		}
	}
	f, err := os.Open(cached)
	if err != nil {
		return "", err
	}
	head := make([]byte, 512)
	n, err := io.ReadFull(f, head)
	f.Close()
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return "", err
	}
	kind := studyarchive.Kind(head[:n], a.DiskName)
	folder := ""
	if kind != studyarchive.None {
		if err := unpackAttachment(ctx, cached, kind, a); err != nil {
			return "", err
		}
		base := archiveFolderName(a.DiskName)
		allocated := records.DiskNames(append(slices.Clone(names), base))
		folder = allocated[len(allocated)-1]
		if err := linkReadOnlyTree(cached+".d", filepath.Join(into, folder)); err != nil {
			return "", err
		}
	}
	dst := filepath.Join(into, a.DiskName)
	if os.Link(cached, dst) == nil {
		return folder, nil
	}
	return folder, copyReadOnly(cached, dst)
}

// The daemon owns its state directory exclusively; concurrent runs share a
// per-blob lock until their cache verification, extraction and placement end.
var attachmentLocks = struct {
	sync.Mutex
	entries map[string]*attachmentLock
}{entries: make(map[string]*attachmentLock)}

type attachmentLock struct {
	sync.Mutex
	users int
}

func lockAttachment(path string) func() {
	attachmentLocks.Lock()
	lock := attachmentLocks.entries[path]
	if lock == nil {
		lock = &attachmentLock{}
		attachmentLocks.entries[path] = lock
	}
	lock.users++
	attachmentLocks.Unlock()
	lock.Lock()
	return func() {
		lock.Unlock()
		attachmentLocks.Lock()
		lock.users--
		if lock.users == 0 {
			delete(attachmentLocks.entries, path)
		}
		attachmentLocks.Unlock()
	}
}

type attachmentOK struct {
	Size    int64              `json:"size"`
	Mtime   int64              `json:"mtime"`
	Archive *attachmentArchive `json:"archive,omitempty"`
}
type attachmentArchive struct {
	Bytes int64 `json:"bytes"`
	Files int   `json:"files"`
}

func readAttachmentOK(path string) attachmentOK {
	var marker attachmentOK
	b, err := os.ReadFile(path + ".ok")
	if err == nil {
		if json.Unmarshal(b, &marker) != nil {
			return attachmentOK{}
		}
	}
	return marker
}
func writeAttachmentOK(path string, extracted *attachmentArchive) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	return atomicJSON(filepath.Dir(path), filepath.Base(path)+".ok", attachmentOK{Size: fi.Size(), Mtime: fi.ModTime().UnixNano(), Archive: extracted})
}

var newAttachmentHash = sha256.New

// matches trusts the verified marker while size and mtime agree, otherwise
// verifies the digest and publishes a fresh marker without a derived tree.
func matches(path string, a service.ClaimedAttachment) bool {
	fi, err := os.Lstat(path)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() != a.SizeBytes {
		return false
	}
	marker := readAttachmentOK(path)
	if marker.Size == fi.Size() && marker.Mtime == fi.ModTime().UnixNano() {
		return true
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	h := newAttachmentHash()
	if _, err := io.Copy(h, f); err != nil {
		return false
	}
	if hex.EncodeToString(h.Sum(nil)) != a.Sha256 {
		return false
	}
	return writeAttachmentOK(path, nil) == nil
}

func archiveFolderName(name string) string {
	lower := strings.ToLower(name)
	for _, suffix := range []string{".tar.gz", ".tar.xz", ".tar.bz2", ".tar.zst", ".tgz", ".zip", ".tar"} {
		if strings.HasSuffix(lower, suffix) {
			return name[:len(name)-len(suffix)]
		}
	}
	return strings.TrimSuffix(name, filepath.Ext(name))
}

func unpackAttachment(ctx context.Context, cached string, kind studyarchive.Format, a service.ClaimedAttachment) error {
	tree := cached + ".d"
	marker := readAttachmentOK(cached)
	if marker.Archive != nil {
		if marker.Archive.Bytes > a.UnpackedBytes || marker.Archive.Files > int(a.InnerFiles) {
			return studyarchive.ErrTooBig
		}
		if fi, err := os.Lstat(tree); err == nil && fi.IsDir() {
			return nil
		}
	}
	// These paths are daemon-owned and derived only from a verified digest.
	for _, path := range []string{tree + ".part", tree} {
		if err := agent.RemoveTree(path); err != nil {
			return err
		}
	}
	if err := studyarchive.Extract(ctx, cached, kind, tree, a.UnpackedBytes, int(a.InnerFiles)); err != nil {
		return errors.Join(err, agent.RemoveTree(tree+".part"), agent.RemoveTree(tree))
	}
	// Keep the validated archive totals, including overwritten members, so a
	// later claim's stricter bounds cannot be bypassed by the derived cache.
	listing, err := studyarchive.ListContext(ctx, cached, kind)
	if err != nil {
		return errors.Join(err, agent.RemoveTree(tree))
	}
	if listing.Bytes > a.UnpackedBytes || listing.Files > int(a.InnerFiles) {
		return errors.Join(studyarchive.ErrTooBig, agent.RemoveTree(tree))
	}
	if err := makeTreeReadOnly(tree); err != nil {
		return errors.Join(err, agent.RemoveTree(tree))
	}
	if err := writeAttachmentOK(cached, &attachmentArchive{Bytes: listing.Bytes, Files: listing.Files}); err != nil {
		return errors.Join(err, agent.RemoveTree(tree))
	}
	return nil
}

func makeTreeReadOnly(root string) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		mode := os.FileMode(0444)
		if entry.IsDir() {
			mode = 0555
		} else if !entry.Type().IsRegular() {
			return errors.New("unsafe cached archive entry")
		}
		return os.Chmod(path, mode)
	})
}

func linkReadOnlyTree(src, dst string) (err error) {
	if err := os.Mkdir(dst, 0700); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, agent.RemoveTree(dst))
		}
	}()
	err = filepath.WalkDir(src, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if rel == "." {
			return nil
		}
		if entry.IsDir() {
			return os.Mkdir(target, 0700)
		}
		if !entry.Type().IsRegular() {
			return errors.New("unsafe cached archive entry")
		}
		if os.Link(path, target) == nil {
			return nil
		}
		return copyReadOnly(path, target)
	})
	if err == nil {
		err = makeTreeReadOnly(dst)
	}
	return err
}

// fetchAttachment downloads a into the cache: to a temporary file beside its
// place, checked for size and digest, then renamed into place read-only.
func fetchAttachment(ctx context.Context, api *apiclient.Client, cache string, a service.ClaimedAttachment) error {
	tmp, err := os.CreateTemp(cache, a.Sha256+"-*.part")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	ctx, cancel := context.WithTimeout(ctx, middleware.AttachmentRequestTimeout)
	defer cancel()
	h := newAttachmentHash()
	w := &capWriter{w: io.MultiWriter(tmp, h), left: a.SizeBytes}
	if err := api.Download(ctx, "/api/role/attachments/"+a.ID.String(), w); err != nil {
		return err
	}
	if w.left != 0 || hex.EncodeToString(h.Sum(nil)) != a.Sha256 {
		return errors.New("downloaded bytes don't match the file's size and sha256")
	}
	if err := tmp.Chmod(0o444); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	cached := filepath.Join(cache, a.Sha256)
	if err := os.Remove(cached + ".ok"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(tmp.Name(), cached); err != nil {
		return err
	}
	return writeAttachmentOK(cached, nil)
}

// copyReadOnly copies src to a new file dst of mode 0444.
func copyReadOnly(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o444)
	if err != nil {
		return err
	}
	if _, err = io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// capWriter refuses bytes beyond left.
type capWriter struct {
	w    io.Writer
	left int64
}

func (c *capWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > c.left {
		return 0, errors.New("more bytes than the file's size")
	}
	c.left -= int64(len(p))
	return c.w.Write(p)
}

func rawJSON(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
func messageBody(r Report) map[string]any {
	return map[string]any{"workspace_id": r.WorkspaceID, "runtime_id": r.RuntimeID, "attempt": r.Attempt, "messages": r.Messages}
}

type oversizedMessageError struct {
	seq  int32
	size int
}

func (e *oversizedMessageError) Error() string {
	return fmt.Sprintf("message %d encoded size %d exceeds %d bytes; original evidence retained in daemon state", e.seq, e.size, middleware.MaxDaemonMessageBytes)
}

// stream persists byte-bounded batches before any send and drains through child
// exit even during cancellation. Network outages cannot discard its final batch.
func (d *daemon) stream(r Report, msgs <-chan agent.Message, cancel context.CancelFunc) error {
	r.Kind = "messages"
	var pending []wireMsg
	var seq int32
	var firstErr error
	flush := func() {
		if len(pending) == 0 {
			return
		}
		r.Messages = pending
		if err := d.messages.Put(r); err != nil {
			// Oversize is a run failure with preserved evidence. Persistence
			// failure must additionally stop the daemon and reach its caller.
			var oversized *oversizedMessageError
			if firstErr == nil || errors.As(firstErr, &oversized) {
				firstErr = err
			}
			cancel()
		}
		pending = nil
	}
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case m, ok := <-msgs:
			if !ok {
				flush()
				return firstErr
			}
			if seq == math.MaxInt32 {
				if firstErr == nil {
					firstErr = errors.New("message sequence exhausted")
				}
				cancel()
				continue
			}
			seq++
			content := m.Content
			if len(content) == 0 {
				content = json.RawMessage(`null`)
			}
			wm := wireMsg{Seq: seq, Type: m.Type, Tool: m.Tool, Content: content}
			candidate := append(pending, wm)
			r.Messages = candidate
			b, err := json.Marshal(messageBody(r))
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				cancel()
				continue
			}
			if len(b) > middleware.MaxDaemonMessageBytes {
				flush()
				r.Messages = []wireMsg{wm}
				b, err = json.Marshal(messageBody(r))
				if err != nil {
					if firstErr == nil {
						firstErr = err
					}
					cancel()
					continue
				}
				if len(b) > middleware.MaxDaemonMessageBytes {
					evidenceDir := filepath.Join(d.cfg.StateDir, "oversized-messages")
					if err := atomicJSON(evidenceDir, fmt.Sprintf("%s-%010d-%010d.json", r.AgentRunID, r.Attempt, seq), r); err != nil {
						firstErr = err
					} else if firstErr == nil {
						firstErr = &oversizedMessageError{seq: seq, size: len(b)}
					}
					cancel()
					continue
				}
				pending = []wireMsg{wm}
			} else {
				pending = candidate
			}
			if len(pending) >= 50 {
				flush()
			}
		case <-tick.C:
			flush()
		}
	}
}
func (d *daemon) watchCancel(ctx context.Context, cancel context.CancelFunc, base string, r Report) {
	q := url.Values{"workspace_id": {r.WorkspaceID.String()}, "runtime_id": {r.RuntimeID.String()}, "attempt": {strconv.Itoa(int(r.Attempt))}}
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			var s struct{ Status string }
			err := d.api.Do(ctx, "GET", base+"/status?"+q.Encode(), nil, &s)
			var he *apiclient.HTTPError
			if (err == nil && s.Status != "running") || (errors.As(err, &he) && (he.Status == 404 || he.Status == 409)) {
				cancel()
				return
			}
		}
	}
}

// The CLI has not started: ambiguous control-plane acknowledgements are safe
// to settle as runtime loss, fenced by attempt, without rerunning the command.
func retryableControlLoss(err error) bool {
	var response *apiclient.HTTPError
	if errors.As(err, &response) {
		return response.Status == 408 || response.Status == 429 || response.Status >= 500
	}
	var transport *url.Error
	return errors.As(err, &transport) || errors.Is(err, context.DeadlineExceeded)
}

// The real adapter invokes this synchronously before accepting its next record.
func (d *daemon) persistMessage(r Report, seq int32, m agent.Message) error {
	r.Kind = "messages"
	content := m.Content
	if len(content) == 0 {
		content = json.RawMessage(`null`)
	}
	r.Messages = []wireMsg{{Seq: seq, Type: m.Type, Tool: m.Tool, Content: content}}
	b, err := json.Marshal(messageBody(r))
	if err != nil {
		return err
	}
	if len(b) > middleware.MaxDaemonMessageBytes {
		dir := filepath.Join(d.cfg.StateDir, "oversized-messages")
		if err := atomicJSON(dir, fmt.Sprintf("%s-%010d-%010d.json", r.AgentRunID, r.Attempt, seq), r); err != nil {
			return err
		}
		return &oversizedMessageError{seq: seq, size: len(b)}
	}
	return d.messages.Put(r)
}

var sandboxDiagnosticPath = regexp.MustCompile(`"(/[^"\r\n]+)"|'(/[^'\r\n]+)'|(/[^\s:;'"<>]+)`)

func sandboxHint(text string) string {
	if strings.Contains(text, "Not logged in") {
		return "The CLI couldn't read your sign-in inside Lykeion's sandbox. Sign in to the CLI on this machine, then run again."
	}
	if strings.Contains(text, "failed to initialize in-process app-server client") {
		return "Codex couldn't write its state inside Lykeion's sandbox."
	}
	for _, line := range strings.Split(text, "\n") {
		if !strings.Contains(line, "Operation not permitted") {
			continue
		}
		if match := sandboxDiagnosticPath.FindStringSubmatch(line); match != nil {
			for _, path := range match[1:] {
				if path != "" {
					return "Lykeion's sandbox blocked " + path + ". A personal MCP server or hook that needs it won't work in Lykeion runs."
				}
			}
		}
	}
	return ""
}

// placeClaimedSkills writes a Team member's own skills where its CLI reads
// skills, so the CLI finds them when it starts. Routing may later place the
// same skill again; it replaces the copy. The folder is made even with no
// skills: Claude Code only notices skills added to a folder it saw at start.
func placeClaimedSkills(harness, dir string, skills []toolroute.RoutedSkill) error {
	root, err := agent.OpenSkillsRoot(harness, dir, true)
	if err != nil {
		return err
	}
	defer root.Close()

	for _, s := range skills {
		if err := placeRoutedRoot(root, s); err != nil {
			return fmt.Errorf("skill %s: %w", s.Name, err)
		}
	}
	return nil
}

// skillsNote tells the run its staged skills by the names its CLI lists them
// under, each with the name its Team's instructions use.
func skillsNote(skills []toolroute.RoutedSkill) string {
	if len(skills) == 0 {
		return ""
	}
	names := make([]string, len(skills))
	for i, s := range skills {
		names[i] = "`" + agent.PinnedName(s.Name) + "` (the Team calls it " + s.Name + ")"
	}
	return "\n\nYour skills are installed under these names: " + strings.Join(names, ", ") + ". Use one when its task comes up."
}

// roleFile is the instructions file the harness's CLI loads from its working
// folder on its own, so a run's role reaches it before the first turn.
func roleFile(harness string) string {
	if harness == "claude" {
		return "CLAUDE.md"
	}
	return "AGENTS.md"
}

// guestDoorOptions excludes CLI-only authentication from connectors and code.
// An empty override also prevents inheritance from the daemon environment.
func guestDoorOptions(opts agent.ExecOptions) agent.ExecOptions {
	opts.Guest = true
	opts.Env = slices.DeleteFunc(slices.Clone(opts.Env), func(entry string) bool { return strings.HasPrefix(entry, "CLAUDE_CODE_OAUTH_TOKEN=") })
	opts.Env = append(opts.Env, "CLAUDE_CODE_OAUTH_TOKEN=")
	return opts
}
