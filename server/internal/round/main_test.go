package round

import (
	"os"
	"testing"

	"lykeion/server/internal/pack/check"
)

func TestMain(m *testing.M) {
	check.MaybeChild()
	os.Exit(m.Run())
}
