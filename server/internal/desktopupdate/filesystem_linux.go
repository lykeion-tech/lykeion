//go:build linux

package desktopupdate

// No Linux filesystem has passed the required real-host checks yet.
func privateFilesystem(int) bool { return false }
