package daemon

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"lykeion/server/internal/processidentity"
)

// Diagnostics contains only live, non-credential daemon information. Uptime is seconds.
type Diagnostics struct {
	Status         string  `json:"status"`
	PID            int     `json:"pid,omitempty"`
	Uptime         float64 `json:"uptime"`
	DaemonID       string  `json:"daemon_id,omitempty"`
	Profile        string  `json:"profile"`
	ServerURL      string  `json:"server_url,omitempty"`
	DeviceName     string  `json:"device_name,omitempty"`
	WorkspaceCount int     `json:"workspace_count"`
	OS             string  `json:"os,omitempty"`
}

// HealthRecord identifies the process serving this private loopback port.
type HealthRecord struct {
	Port        int    `json:"port"`
	PID         int    `json:"pid"`
	Started     string `json:"started"`
	Nonce       string `json:"nonce"`
	Profile     string `json:"profile"`
	ServerURL   string `json:"server_url"`
	DaemonID    string `json:"daemon_id"`
	ShutdownKey string `json:"shutdown_key,omitempty"`
}

type healthResponse struct {
	Diagnostics
	Started string `json:"started"`
	Nonce   string `json:"nonce"`
}

func healthURL(port int) string { return fmt.Sprintf("http://127.0.0.1:%d/health", port) }

func shutdownProof(key, role, challenge, nonce string) string {
	mac := hmac.New(sha256.New, []byte(key))
	_, _ = io.WriteString(mac, role+"\x00"+challenge+"\x00"+nonce)
	return hex.EncodeToString(mac.Sum(nil))
}

type shutdownChallenge struct {
	client  string
	expires time.Time
}

type shutdownChallenges struct {
	mu      sync.Mutex
	entries map[string]shutdownChallenge
	limit   int
	ttl     time.Duration
}

func newShutdownChallenges(limit int, ttl time.Duration) *shutdownChallenges {
	return &shutdownChallenges{entries: make(map[string]shutdownChallenge), limit: limit, ttl: ttl}
}

func (s *shutdownChallenges) issue(client string, now time.Time) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, value := range s.entries {
		if !now.Before(value.expires) {
			delete(s.entries, key)
		}
	}
	if len(s.entries) >= s.limit {
		return "", errors.New("too many shutdown challenges")
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	server := hex.EncodeToString(b)
	if _, exists := s.entries[server]; exists {
		return "", errors.New("duplicate shutdown challenge")
	}
	s.entries[server] = shutdownChallenge{client: client, expires: now.Add(s.ttl)}
	return server, nil
}

func (s *shutdownChallenges) consume(server, client string, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, exists := s.entries[server]
	if !exists {
		return false
	}
	delete(s.entries, server)
	return entry.client == client && now.Before(entry.expires)
}

func readHealthRecord(path string) (HealthRecord, error) {
	return readHealthRecordWithOpen(path, openReportFile)
}

func readHealthRecordWithOpen(path string, open func(string) (*os.File, error)) (HealthRecord, error) {
	var record HealthRecord
	info, err := os.Lstat(path)
	if err != nil {
		return record, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > 4096 {
		return record, errors.New("invalid daemon health file")
	}
	f, err := open(path)
	if err != nil {
		return record, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return record, err
	}
	if !os.SameFile(info, opened) {
		return record, errors.New("daemon health file changed")
	}
	record, err = decodeHealthRecord(f)
	if err != nil {
		return record, err
	}
	current, err := os.Lstat(path)
	if err != nil {
		return HealthRecord{}, err
	}
	if !os.SameFile(opened, current) {
		return HealthRecord{}, errors.New("daemon health file changed")
	}
	return record, nil
}

func decodeHealthRecord(f *os.File) (HealthRecord, error) {
	var record HealthRecord
	info, err := f.Stat()
	if err != nil {
		return record, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > 4096 {
		return record, errors.New("invalid daemon health file")
	}
	b, err := io.ReadAll(io.LimitReader(f, 4097))
	if err != nil {
		return record, err
	}
	if len(b) > 4096 {
		return record, errors.New("invalid daemon health file")
	}
	if err := json.Unmarshal(b, &record); err != nil {
		return record, err
	}
	if record.Port < 1 || record.Port > 65535 || record.PID <= 0 || record.Started == "" || record.Nonce == "" || record.ServerURL == "" || record.DaemonID == "" {
		return record, errors.New("invalid daemon health record")
	}
	return record, nil
}

func writeHealthRecord(path string, record HealthRecord) error {
	b, err := json.Marshal(record)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".daemon-health-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(0600); err != nil {
		return errors.Join(err, f.Close())
	}
	if _, err := f.Write(b); err != nil {
		return errors.Join(err, f.Close())
	}
	if err := f.Sync(); err != nil {
		return errors.Join(err, f.Close())
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return err
	}
	return syncDir(dir)
}

func clearHealthRecord(path string, record HealthRecord) {
	current, err := readHealthRecord(path)
	if err == nil && current == record {
		_ = os.Remove(path)
	}
}

// ProbeHealth refuses stale processes and records, including ports reused by a different daemon.
func ProbeHealth(ctx context.Context, path, profile, server string) (Diagnostics, error) {
	record, err := readHealthRecord(path)
	if err != nil {
		return Diagnostics{}, err
	}
	if record.Profile != profile || record.ServerURL != strings.TrimRight(server, "/") {
		return Diagnostics{}, errors.New("daemon health profile or server mismatch")
	}
	started, err := processidentity.Start(record.PID)
	if err != nil || started != record.Started {
		return Diagnostics{}, errors.New("daemon health process is stale")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL(record.Port), nil)
	if err != nil {
		return Diagnostics{}, err
	}
	transport := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: time.Second}).DialContext, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("daemon health redirects are refused") }}
	response, err := client.Do(req)
	if err != nil {
		return Diagnostics{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Diagnostics{}, errors.New("daemon health is unavailable")
	}
	var live healthResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, 16384)).Decode(&live); err != nil {
		return Diagnostics{}, err
	}
	if live.Status != "running" || live.PID != record.PID || live.Started != record.Started || live.Nonce != record.Nonce || live.Profile != record.Profile || live.ServerURL != record.ServerURL || live.DaemonID != record.DaemonID {
		return Diagnostics{}, errors.New("daemon health identity mismatch")
	}
	current, err := readHealthRecord(path)
	if err != nil || current != record {
		return Diagnostics{}, errors.New("daemon health record changed during probe")
	}
	started, err = processidentity.Start(record.PID)
	if err != nil || started != record.Started {
		return Diagnostics{}, errors.New("daemon process changed during probe")
	}
	return live.Diagnostics, nil
}

// RequestDesktopShutdown requests the verified daemon to stop itself; it never signals a PID.
func RequestDesktopShutdown(ctx context.Context, path, profile, server string, pid int) error {
	record, err := readHealthRecord(path)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(profile, "desktop-") || record.Profile != profile || record.ServerURL != strings.TrimRight(server, "/") || record.PID != pid || len(record.ShutdownKey) != 64 {
		return errors.New("desktop shutdown identity cannot be verified")
	}
	if _, err := ProbeHealth(ctx, path, profile, server); err != nil {
		return err
	}
	current, err := readHealthRecord(path)
	if err != nil || current != record {
		return errors.New("desktop health record changed")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	transport := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: time.Second}).DialContext, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error {
		return errors.New("desktop shutdown redirects are refused")
	}}
	challengeBytes := make([]byte, 32)
	if _, err := rand.Read(challengeBytes); err != nil {
		return err
	}
	challenge := hex.EncodeToString(challengeBytes)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/shutdown-challenge?nonce=%s", record.Port, challenge), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+shutdownProof(record.ShutdownKey, "request", challenge, record.Nonce))
	res, err := client.Do(req)
	if err != nil {
		return errors.New("desktop shutdown challenge failed")
	}
	var answer struct {
		Proof       string `json:"proof"`
		ServerNonce string `json:"server_nonce"`
	}
	decodeErr := json.NewDecoder(io.LimitReader(res.Body, 4096)).Decode(&answer)
	res.Body.Close()
	serverBytes, serverErr := hex.DecodeString(answer.ServerNonce)
	if res.StatusCode != http.StatusOK || decodeErr != nil || serverErr != nil || len(serverBytes) != 32 || subtle.ConstantTimeCompare([]byte(answer.Proof), []byte(shutdownProof(record.ShutdownKey, "server", challenge+answer.ServerNonce, record.Nonce))) != 1 {
		return errors.New("desktop shutdown server cannot be authenticated")
	}
	current, err = readHealthRecord(path)
	if err != nil || current != record {
		return errors.New("desktop health record changed")
	}
	req, err = http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d/shutdown", record.Port), nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Lykeion-Challenge", challenge)
	req.Header.Set("X-Lykeion-Server-Nonce", answer.ServerNonce)
	req.Header.Set("Authorization", "Bearer "+shutdownProof(record.ShutdownKey, "client", challenge+answer.ServerNonce, record.Nonce))
	res, err = client.Do(req)
	if err != nil {
		return errors.New("desktop shutdown request failed")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		return errors.New("desktop shutdown refused")
	}
	return nil
}

func (d *daemon) startHealth() (func(), error) {
	started, err := processidentity.Start(os.Getpid())
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	record := HealthRecord{Port: listener.Addr().(*net.TCPAddr).Port, PID: os.Getpid(), Started: started, Nonce: hex.EncodeToString(nonce), Profile: d.cfg.Profile, ServerURL: strings.TrimRight(d.cfg.Server, "/"), DaemonID: d.id}
	if d.stopControl != nil && strings.HasPrefix(d.cfg.Profile, "desktop-") {
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			listener.Close()
			return nil, err
		}
		record.ShutdownKey = hex.EncodeToString(key)
	}
	began := time.Now()
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		d.mu.Lock()
		live := healthResponse{Diagnostics: Diagnostics{Status: "running", PID: record.PID, Uptime: time.Since(began).Seconds(), DaemonID: d.id, Profile: d.cfg.Profile, ServerURL: record.ServerURL, DeviceName: d.cfg.Device, WorkspaceCount: d.workspaceCount, OS: runtime.GOOS}, Started: record.Started, Nonce: record.Nonce}
		d.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(live)
	})
	if record.ShutdownKey != "" {
		var shutdownOnce sync.Once
		challenges := newShutdownChallenges(1024, 10*time.Second)
		mux.HandleFunc("/shutdown-challenge", func(w http.ResponseWriter, r *http.Request) {
			challenge := r.URL.Query().Get("nonce")
			decoded, err := hex.DecodeString(challenge)
			if r.Method != http.MethodGet || err != nil || len(decoded) != 32 || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+shutdownProof(record.ShutdownKey, "request", challenge, record.Nonce))) != 1 {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			serverNonce, err := challenges.issue(challenge, time.Now())
			if err != nil {
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			w.Header().Set("Cache-Control", "no-store")
			_ = json.NewEncoder(w).Encode(map[string]string{"server_nonce": serverNonce, "proof": shutdownProof(record.ShutdownKey, "server", challenge+serverNonce, record.Nonce)})
		})
		mux.HandleFunc("/shutdown", func(w http.ResponseWriter, r *http.Request) {
			challenge := r.Header.Get("X-Lykeion-Challenge")
			serverNonce := r.Header.Get("X-Lykeion-Server-Nonce")
			if r.Method != http.MethodPost || !challenges.consume(serverNonce, challenge, time.Now()) || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+shutdownProof(record.ShutdownKey, "client", challenge+serverNonce, record.Nonce))) != 1 {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			shutdownOnce.Do(d.stopControl)
		})
	}
	server := &http.Server{Handler: mux, ReadHeaderTimeout: time.Second, ReadTimeout: 2 * time.Second, WriteTimeout: 2 * time.Second, IdleTimeout: 2 * time.Second, MaxHeaderBytes: 4096}
	if err := writeHealthRecord(d.cfg.HealthPath, record); err != nil {
		listener.Close()
		clearHealthRecord(d.cfg.HealthPath, record)
		return nil, err
	}
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			d.fatal(fmt.Errorf("daemon health listener: %w", err))
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			_ = server.Close()
			clearHealthRecord(d.cfg.HealthPath, record)
		})
	}, nil
}
