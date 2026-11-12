//go:build darwin || linux

package desktopupdate

import "testing"

func TestUnverifiedPlatformGatesRemainClosed(t *testing.T) {
	for _, target := range [][2]string{{"darwin", "arm64"}, {"darwin", "amd64"}, {"linux", "arm64"}, {"linux", "amd64"}, {"windows", "amd64"}, {"linux", "386"}} {
		if platformProofReady(target[0], target[1]) {
			t.Fatalf("unverified target enabled: %s/%s", target[0], target[1])
		}
	}
}
