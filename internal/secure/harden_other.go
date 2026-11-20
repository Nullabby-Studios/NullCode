//go:build !linux && !windows

package secure

// HardenProcess is a no-op where mlockall does not exist.
func HardenProcess() {}
