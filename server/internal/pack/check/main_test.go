package check

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	MaybeChild()
	os.Exit(m.Run())
}
