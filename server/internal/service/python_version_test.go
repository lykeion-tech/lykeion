package service

import "testing"

func TestValidPythonVersion(t *testing.T) {
	for v, want := range map[string]bool{"3.9": true, "3.15": true, "3.8": false, "3.16": false} {
		if ValidPythonVersion(v) != want {
			t.Errorf("%s: want %v", v, want)
		}
	}
}
