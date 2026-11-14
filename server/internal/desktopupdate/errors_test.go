package desktopupdate

import (
	"errors"
	"fmt"
	"testing"
)

func TestErrorCategoryNeverReturnsRawError(t *testing.T) {
	for _, category := range []Category{Unsupported, Unconfigured, Network, InvalidMetadata, CacheFull, CorruptState, Generic} {
		if got := ErrorCategory(fmt.Errorf("secret path: %w", checkError{category})); got != category {
			t.Fatalf("category %q became %q", category, got)
		}
	}
	for _, err := range []error{errors.New("https://example.test/?token=secret"), checkError{"secret path"}, nil} {
		if got := ErrorCategory(err); got != Generic {
			t.Fatalf("unexpected category %q", got)
		}
	}
}
