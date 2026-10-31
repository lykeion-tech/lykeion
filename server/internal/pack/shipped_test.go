package pack

import (
	"slices"
	"testing"

	"lykeion/server/internal/board"
)

func TestShippedTeamsParseOnePerStage(t *testing.T) {
	var stages []string
	for _, name := range ShippedNames(Shipped()) {
		_, p, err := ReadShipped(Shipped(), name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		stages = append(stages, p.Manifest.Stage)
	}
	slices.Sort(stages)
	want := slices.Sorted(slices.Values(board.Stages))
	if !slices.Equal(stages, want) {
		t.Fatalf("shipped stages = %v, want %v", stages, want)
	}
}
