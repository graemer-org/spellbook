//go:build !unix

package cli

// spawnDetached is not available here; the caller does the work itself.
func spawnDetached(env string) bool { return false }
