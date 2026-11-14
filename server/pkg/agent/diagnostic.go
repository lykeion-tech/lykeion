package agent

import (
	"regexp"
	"strings"
	"sync"
)

var diagnosticSecrets = regexp.MustCompile(`(?i)(authorization|auth|api[_-]?key|token|secret|password)(["']?\s*[:=]\s*["']?)([^\s,;"']+)`)
var diagnosticTokens = regexp.MustCompile(`(?:lyk_|ltk_|sk-)[A-Za-z0-9_-]+`)
var diagnosticBearer = regexp.MustCompile(`(?i)bearer\s+[^\s,;"']+`)

// diagnosticTail bounds retained stderr and redacts common credential shapes
// before they become persisted failure text. exec writes from its copy goroutine.
type diagnosticTail struct {
	mu sync.Mutex
	b  []byte
}

func (t *diagnosticTail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	const limit = 4096
	if len(p) >= limit {
		t.b = append(t.b[:0], p[len(p)-limit:]...)
	} else {
		t.b = append(t.b, p...)
		if len(t.b) > limit {
			t.b = t.b[len(t.b)-limit:]
		}
	}
	return len(p), nil
}
func (t *diagnosticTail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	s := strings.ToValidUTF8(string(t.b), "")
	s = strings.Map(func(r rune) rune {
		if r < 32 && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, s)
	s = diagnosticBearer.ReplaceAllString(s, "Bearer [REDACTED]")
	s = diagnosticSecrets.ReplaceAllString(s, "$1$2[REDACTED]")
	return strings.TrimSpace(diagnosticTokens.ReplaceAllString(s, "[REDACTED]"))
}
