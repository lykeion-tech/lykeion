package agent

import (
	"context"
	"runtime"
)

// GuestIsolationProtocol identifies the enforced Linux guest launch boundary.
const GuestIsolationProtocol = "linux-v1"

// OutputAttestationProtocol advertises script output receipts and scoped replay.
const OutputAttestationProtocol = "json-output-v1"

// Platform is what a daemon reports about the machine its runtimes run on,
// for people reading a run. An empty field was not detected; none is guessed.
type Platform struct {
	OS     string `json:"os"`
	Arch   string `json:"arch"`
	Kernel string `json:"kernel"`
	// Language is the Python the machine runs agents' scripts with, as it
	// reports its own version.
	Language string `json:"language"`
}

// DetectPlatform reads the machine's platform once, at daemon start.
func DetectPlatform(ctx context.Context) Platform {
	p := Platform{OS: runtime.GOOS, Arch: runtime.GOARCH}
	if runtime.GOOS != "windows" {
		p.Kernel = probe(ctx, "uname", "-sr")
	}
	for _, bin := range []string{"python3", "python"} {
		if p.Language = probe(ctx, bin, "--version"); p.Language != "" {
			break
		}
	}
	return p
}

// Clean caps each field at what the server stores.
func (p Platform) Clean() Platform {
	return Platform{OS: capRunes(p.OS, 32), Arch: capRunes(p.Arch, 32), Kernel: VersionLine(p.Kernel), Language: VersionLine(p.Language)}
}

func capRunes(s string, n int) string {
	s = VersionLine(s)
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}
