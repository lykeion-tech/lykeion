//go:build !darwin && !linux

package daemon

import (
	"errors"
	"os"
)

// Other platforms retain their existing queue behavior; daemon ownership is
// currently unsupported there and no Unix durability guarantee is implied.
func syncExistingReport(path string, want Report, _ func(*os.File) error) error {
	got, err := readReport(path)
	if err != nil {
		return err
	}
	if !sameReport(got, want) {
		return errors.New("conflicting durable report")
	}
	return nil
}
