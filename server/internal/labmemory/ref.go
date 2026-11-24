// Package labmemory reads live lab evidence. Coresearch is deliberately excluded.
package labmemory

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Ref names one piece of lab evidence. Node is set only on a report ref, and
// names one node of that study's report.
type Ref struct {
	Kind string
	ID   uuid.UUID
	Node string
}

// maxNode bounds a report node id in a citation.
const maxNode = 512

func ParseRef(s string) (Ref, error) {
	kind, rest, ok := strings.Cut(s, ":")
	if !ok {
		return Ref{}, fmt.Errorf("invalid fact reference %q", s)
	}
	switch kind {
	case "study", "record", "comment", "decision", "lesson", "workspace", "report", "proposal":
	default:
		return Ref{}, fmt.Errorf("unknown fact kind %q", kind)
	}
	id, node, fragment := strings.Cut(rest, "#")
	if fragment && (kind != "report" || node == "" || len(node) > maxNode || !utf8.ValidString(node)) {
		return Ref{}, fmt.Errorf("invalid fact reference %q", s)
	}
	if kind == "workspace" && id == "context" {
		return Ref{Kind: kind}, nil
	}
	u, err := uuid.Parse(id)
	if err != nil || u == uuid.Nil {
		return Ref{}, fmt.Errorf("invalid fact ID %q", id)
	}
	return Ref{Kind: kind, ID: u, Node: node}, nil
}

func (r Ref) String() string {
	if r.Kind == "workspace" && r.ID == uuid.Nil {
		return "workspace:context"
	}
	if r.Node != "" {
		return r.Kind + ":" + r.ID.String() + "#" + r.Node
	}
	return r.Kind + ":" + r.ID.String()
}

// Base is the ref without its report node.
func (r Ref) Base() Ref {
	r.Node = ""
	return r
}
