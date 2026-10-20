package daemon

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"lykeion/server/pkg/agent"
)

// ModeSandboxed is the only supported run mode.
const ModeSandboxed = "sandboxed"

const (
	policyLimit       = 4 << 10
	recordLimit       = 2 << 20
	defaultRetention  = 30
	maxRetentionDays  = 3650
	sessionReady      = "ready"
	sessionInUse      = "in_use"
	sessionUnknown    = "unknown"
	recordVersion     = 1
	policyVersion     = 1
	defaultPolicyHash = "default"
)

// continuityScope is built only by the server, e.g. card:<id>/role:<role>.
var continuityScope = regexp.MustCompile(`^[a-z]+:[0-9a-f-]{36}(/[a-z]+:[A-Za-z0-9_.-]{1,64})*$`)

// Policy is a workspace's daemon-local runtime preference. It holds no secrets
// and cannot add skills or tool servers to a run.
type Policy struct {
	Version       int `json:"version"`
	RetentionDays int `json:"session_retention_days,omitempty"`
}

// PolicySnapshot is what one attempt was started under.
type PolicySnapshot struct {
	Policy
	Digest   string
	Explicit bool
}

func profileDir(stateDir string, ws uuid.UUID) string {
	return filepath.Join(stateDir, "workspace-profiles", ws.String())
}

// readBounded reads a regular, non-link file of at most limit bytes.
func readBounded(p string, limit int64) ([]byte, error) {
	st, err := os.Lstat(p)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file: %s", p)
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("file exceeds %d bytes: %s", limit, p)
	}
	return b, nil
}

// checkProfileDir refuses a planted link where a profile directory belongs.
func checkProfileDir(p string) error {
	for _, d := range []string{filepath.Dir(p), p} {
		st, err := os.Lstat(d)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("workspace profile path is not a real directory: %s", d)
		}
	}
	return nil
}

// LoadPolicy returns the workspace's policy, or the retention default when it
// has none. An explicit but invalid policy is an error, never a fallback.
func LoadPolicy(stateDir string, ws uuid.UUID) (PolicySnapshot, error) {
	dir := profileDir(stateDir, ws)
	if err := checkProfileDir(dir); err != nil {
		return PolicySnapshot{}, err
	}
	b, err := readBounded(filepath.Join(dir, "policy.json"), policyLimit)
	if errors.Is(err, os.ErrNotExist) {
		return PolicySnapshot{Policy: Policy{Version: policyVersion, RetentionDays: defaultRetention}, Digest: defaultPolicyHash}, nil
	}
	if err != nil {
		return PolicySnapshot{}, err
	}
	var stored struct {
		Policy
		LegacyMode json.RawMessage `json:"mode,omitempty"`
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&stored); err != nil {
		return PolicySnapshot{}, fmt.Errorf("invalid workspace policy: %w", err)
	}
	p := stored.Policy
	if err := p.check(); err != nil {
		return PolicySnapshot{}, err
	}
	if p.RetentionDays == 0 {
		p.RetentionDays = defaultRetention
	}
	canonical, err := json.Marshal(p)
	if err != nil {
		return PolicySnapshot{}, err
	}
	sum := sha256.Sum256(canonical)
	return PolicySnapshot{Policy: p, Digest: hex.EncodeToString(sum[:]), Explicit: true}, nil
}
func (p Policy) check() error {
	if p.Version != policyVersion {
		return fmt.Errorf("unsupported workspace policy version %d", p.Version)
	}
	if p.RetentionDays < 0 || p.RetentionDays > maxRetentionDays {
		return fmt.Errorf("session_retention_days must be 0..%d", maxRetentionDays)
	}
	return nil
}

// SavePolicy applies to future claims only.
func SavePolicy(stateDir string, ws uuid.UUID, p Policy) error {
	p.Version = policyVersion
	if err := p.check(); err != nil {
		return err
	}
	dir := profileDir(stateDir, ws)
	if err := checkProfileDir(dir); err != nil {
		return err
	}
	if err := privateDir(filepath.Dir(dir)); err != nil {
		return err
	}
	return atomicJSON(dir, "policy.json", p)
}

// ResetProfile forgets Lykeion's policy and session registry for a workspace.
// The account's own CLI history and sign-in are untouched.
func ResetProfile(stateDir string, ws uuid.UUID) error {
	dir := profileDir(stateDir, ws)
	if err := checkProfileDir(dir); err != nil {
		return err
	}
	return os.RemoveAll(dir)
}

type sessionScope struct {
	Workspace uuid.UUID `json:"workspace_id"`
	User      uuid.UUID `json:"responsible_user_id"`
	Harness   string    `json:"harness"`
	Scope     string    `json:"continuity_scope"`
}

func (s sessionScope) key() string {
	sum := sha256.Sum256([]byte(strings.Join([]string{s.Workspace.String(), s.User.String(), s.Harness, s.Scope}, "\x00")))
	return hex.EncodeToString(sum[:])
}

// ForgetScope deletes one scope's private folder and session record, e.g. when
// its chat is deleted. A folder a run holds stays, and the forget fails for the
// caller to retry. The account's own CLI history is untouched.
func (d *daemon) ForgetScope(ws, user uuid.UUID, harness, scope string) error {
	key := sessionScope{Workspace: ws, User: user, Harness: harness, Scope: scope}.key()
	name := "scope-" + key
	if !d.holdScratch(name) {
		return errors.New("the scope's folder is in use")
	}
	defer d.releaseScratch(name)
	reservation, err := agent.ReserveScratch(filepath.Join(d.cfg.Home, name))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if reservation != nil {
		defer reservation.Release()
		if err = reservation.Remove(); err != nil {
			return err
		}
	}
	if err = os.Remove(finishedRecord(d.cfg.StateDir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	dir := sessionsDir(d.cfg.StateDir, ws)
	if err := checkProfileDir(filepath.Dir(dir)); err != nil {
		return err
	}
	if err := checkProfileDir(dir); err != nil {
		return err
	}
	err = os.Remove(filepath.Join(dir, key+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return syncDir(dir)
}

type sessionRecord struct {
	Version       int             `json:"version"`
	Scope         sessionScope    `json:"scope"`
	State         string          `json:"state"`
	Session       string          `json:"session_id,omitempty"`
	ContextDigest string          `json:"context_digest"`
	PolicyDigest  string          `json:"policy_digest"`
	AgentRun      uuid.UUID       `json:"agent_run_id"`
	Attempt       int32           `json:"attempt"`
	Baseline      json.RawMessage `json:"claude_baseline,omitempty"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

func sessionsDir(stateDir string, ws uuid.UUID) string {
	return filepath.Join(profileDir(stateDir, ws), "sessions")
}
func readRecord(p string) (*sessionRecord, error) {
	b, err := readBounded(p, recordLimit)
	if err != nil {
		return nil, err
	}
	var r sessionRecord
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	if r.Version != recordVersion {
		return nil, fmt.Errorf("unsupported session record version %d", r.Version)
	}
	return &r, nil
}

// continuity is one attempt's hold on its scope. Everything it records was
// fixed when the attempt began.
type continuity struct {
	dir, key  string
	owned     bool
	rec       sessionRecord
	prev      *sessionRecord
	resume    string
	baseline  []byte
	note      string
	retention time.Duration
}

func contextDigest(pol PolicySnapshot, c claimed) string {
	h := sha256.New()
	servers := make([]string, 0, len(c.MCP))
	for _, s := range c.MCP {
		servers = append(servers, s.Name+"\x00"+s.URL)
	}
	slices.Sort(servers)
	for _, part := range append([]string{pol.Digest, c.AgentRun.Harness, c.AgentRun.Skill}, servers...) {
		fmt.Fprintf(h, "%d:%s", len(part), part)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// beginContinuity claims the scope
// and durably consumes its previous session before any launch, so a crash can
// never subtract a stale baseline. The daemon alone chooses what to resume.
func (d *daemon) beginContinuity(c claimed, pol PolicySnapshot) (*continuity, error) {
	run := c.AgentRun
	if run.ContinuityScope != "" && !continuityScope.MatchString(run.ContinuityScope) {
		return nil, fmt.Errorf("invalid continuity scope %q", run.ContinuityScope)
	}
	if run.ContinuityScope == "" {
		// The account's setup applies, but nothing is recorded or resumed.
		return &continuity{}, nil
	}
	if run.ResponsibleUserID == uuid.Nil || run.WorkspaceID == uuid.Nil {
		return nil, errors.New("agent run has no workspace or responsible user")
	}
	scope := sessionScope{Workspace: run.WorkspaceID, User: run.ResponsibleUserID, Harness: run.Harness, Scope: run.ContinuityScope}
	cont := &continuity{dir: sessionsDir(d.cfg.StateDir, run.WorkspaceID), key: scope.key(), retention: time.Duration(pol.RetentionDays) * 24 * time.Hour,
		rec: sessionRecord{Version: recordVersion, Scope: scope, State: sessionInUse, ContextDigest: contextDigest(pol, c), PolicyDigest: pol.Digest, AgentRun: run.ID, Attempt: run.Attempt}}
	d.mu.Lock()
	busy := d.scopes[cont.key]
	if !busy {
		d.scopes[cont.key] = true
	}
	d.mu.Unlock()
	if busy {
		// Lykeion runs never share a live session. This one starts fresh and
		// leaves the scope's record to its holder.
		cont.note = "another Lykeion run is using this card and role's session; starting a fresh session"
		return cont, nil
	}
	cont.owned = true
	fail := func(err error) (*continuity, error) { d.releaseScope(cont); return nil, err }
	if err := checkProfileDir(filepath.Dir(cont.dir)); err != nil {
		return fail(err)
	}
	if err := checkProfileDir(cont.dir); err != nil {
		return fail(err)
	}
	prev, err := readRecord(filepath.Join(cont.dir, cont.key+".json"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		cont.note = "the previous session record is unreadable; starting a fresh session"
		prev = nil
	}
	if prev != nil && prev.Scope != scope {
		return fail(errors.New("session record does not match its scope"))
	}
	cont.prev = prev
	switch {
	case prev == nil:
	case prev.State != sessionReady:
		cont.note = "the previous session ended without a trustworthy usage baseline; starting a fresh session"
	case prev.ContextDigest != cont.rec.ContextDigest:
		cont.note = "the role's context changed since the previous session; starting a fresh session"
	case prev.Session == "" || (scope.Harness == "claude" && len(prev.Baseline) == 0):
		cont.note = "the previous session has no usage baseline; starting a fresh session"
	default:
		cont.resume, cont.baseline = prev.Session, prev.Baseline
	}
	held := cont.rec
	held.Session = cont.resume
	held.UpdatedAt = time.Now().UTC()
	if err := atomicJSON(cont.dir, cont.key+".json", held); err != nil {
		return fail(err)
	}
	return cont, nil
}

func (d *daemon) releaseScope(cont *continuity) {
	if cont == nil || !cont.owned {
		return
	}
	d.mu.Lock()
	delete(d.scopes, cont.key)
	d.mu.Unlock()
	cont.owned = false
}

// abandonContinuity restores the consumed record when no CLI ever started.
func (d *daemon) abandonContinuity(cont *continuity) error {
	if cont == nil || !cont.owned {
		return nil
	}
	defer d.releaseScope(cont)
	if cont.prev == nil {
		err := os.Remove(filepath.Join(cont.dir, cont.key+".json"))
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return errors.Join(err, syncDir(cont.dir))
	}
	return atomicJSON(cont.dir, cont.key+".json", cont.prev)
}

// finishContinuity runs only after the attempt's final report is durable. A
// session without a clean baseline is kept visible but never resumed.
func (d *daemon) finishContinuity(cont *continuity, res agent.Result) error {
	if cont == nil || !cont.owned {
		return nil
	}
	defer d.releaseScope(cont)
	rec := cont.rec
	rec.UpdatedAt = time.Now().UTC()
	rec.Session = res.SessionID
	if res.Err == nil && res.SessionID != "" && (rec.Scope.Harness != "claude" || len(res.ClaudeSnapshot) > 0) {
		rec.State, rec.Baseline = sessionReady, res.ClaudeSnapshot
	} else {
		rec.State = sessionUnknown
	}
	if err := atomicJSON(cont.dir, rec.Scope.key()+".json", rec); err != nil {
		return err
	}
	return pruneSessions(cont.dir, cont.retention, rec.UpdatedAt)
}

// pruneSessions applies the explicit registry retention. Native CLI history
// stays with the provider.
func pruneSessions(dir string, retention time.Duration, now time.Time) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	var errs []error
	for _, e := range entries {
		if !e.Type().IsRegular() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		p := filepath.Join(dir, e.Name())
		r, err := readRecord(p)
		if err == nil && (r.State == sessionInUse || now.Sub(r.UpdatedAt) <= retention) {
			continue
		}
		errs = append(errs, os.Remove(p))
	}
	return errors.Join(errs...)
}

// recoverSessions runs while this daemon exclusively owns its state: any held
// record belongs to a dead attempt whose usage baseline is unknown.
func (d *daemon) recoverSessions() error {
	root := filepath.Join(d.cfg.StateDir, "workspace-profiles")
	if err := checkProfileDir(root); err != nil {
		return err
	}
	workspaces, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, w := range workspaces {
		ws, err := uuid.Parse(w.Name())
		if err != nil || !w.IsDir() {
			continue
		}
		dir := sessionsDir(d.cfg.StateDir, ws)
		if err := checkProfileDir(dir); err != nil {
			return err
		}
		entries, err := os.ReadDir(dir)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		for _, e := range entries {
			if !e.Type().IsRegular() || !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			r, err := readRecord(filepath.Join(dir, e.Name()))
			if err != nil || r.State != sessionInUse {
				continue
			}
			r.State, r.Baseline, r.UpdatedAt = sessionUnknown, nil, time.Now().UTC()
			if err := atomicJSON(dir, e.Name(), r); err != nil {
				return err
			}
		}
		pol, err := LoadPolicy(d.cfg.StateDir, ws)
		if err != nil {
			continue
		}
		if err := pruneSessions(dir, time.Duration(pol.RetentionDays)*24*time.Hour, time.Now().UTC()); err != nil {
			return err
		}
	}
	return nil
}

// ProfileStatus describes a workspace's retention and its recorded sessions.
func ProfileStatus(stateDir string, ws uuid.UUID) (PolicySnapshot, map[string]int, error) {
	pol, err := LoadPolicy(stateDir, ws)
	if err != nil {
		return pol, nil, err
	}
	counts := map[string]int{}
	entries, err := os.ReadDir(sessionsDir(stateDir, ws))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return pol, nil, err
	}
	for _, e := range entries {
		if r, err := readRecord(filepath.Join(sessionsDir(stateDir, ws), e.Name())); err == nil {
			counts[r.State]++
		}
	}
	return pol, counts, nil
}
