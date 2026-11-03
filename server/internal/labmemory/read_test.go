package labmemory

import (
	"github.com/google/uuid"
	"testing"
)

func TestParseRefRoundTrip(t *testing.T) {
	for _, kind := range []string{"study", "record", "comment", "decision", "lesson", "workspace"} {
		s := kind + ":" + uuid.NewString()
		r, err := ParseRef(s)
		if err != nil || r.String() != s {
			t.Fatalf("%s: %v %v", s, r, err)
		}
	}
	r, err := ParseRef("workspace:context")
	if err != nil || r.String() != "workspace:context" {
		t.Fatalf("sentinel: %v %v", r, err)
	}
	for _, s := range []string{"coresearch:" + uuid.NewString(), "record:", "study:not-a-uuid", "study:" + uuid.Nil.String()} {
		if _, err := ParseRef(s); err == nil {
			t.Fatalf("accepted %q", s)
		}
	}
}
