//go:build !linux && !darwin

package processidentity

import "errors"

func Start(int) (string, error) { return "", errors.New("process identity requires macOS or Linux") }
