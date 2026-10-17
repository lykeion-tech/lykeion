package handler

import (
	"os"
	"testing"

	"lykeion/server/internal/pack/check"
)

// TestMain serves pack checks when the test binary is started as a check child.
func TestMain(m *testing.M) {
	check.MaybeChild()
	os.Exit(m.Run())
}
