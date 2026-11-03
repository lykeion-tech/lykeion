// Package labmemory reads live lab evidence. Coresearch is deliberately excluded.
package labmemory

import (
	"fmt"
	"github.com/google/uuid"
	"strings"
)

type Ref struct {
	Kind string
	ID   uuid.UUID
}

func ParseRef(s string) (Ref, error) {
	kind, id, ok := strings.Cut(s, ":")
	if !ok {
		return Ref{}, fmt.Errorf("invalid fact reference %q", s)
	}
	switch kind {
	case "study", "record", "comment", "decision", "lesson", "workspace":
	default:
		return Ref{}, fmt.Errorf("unknown fact kind %q", kind)
	}
	if kind == "workspace" && id == "context" {
		return Ref{Kind: kind}, nil
	}
	u, err := uuid.Parse(id)
	if err != nil || u == uuid.Nil {
		return Ref{}, fmt.Errorf("invalid fact ID %q", id)
	}
	return Ref{kind, u}, nil
}
func (r Ref) String() string {
	if r.Kind == "workspace" && r.ID == uuid.Nil {
		return "workspace:context"
	}
	return r.Kind + ":" + r.ID.String()
}
