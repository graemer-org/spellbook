//go:build unix

package cli

import (
	"os"
	"os/exec"
	"syscall"
)

// spawnDetached starts this program again with the same arguments in a
// session of its own, so it keeps running when the process that started it
// is ended. It reports whether the child was started.
func spawnDetached(env string) bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	child := exec.Command(exe, os.Args[1:]...)
	child.Env = append(os.Environ(), env)
	child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := child.Start(); err != nil {
		return false
	}
	return child.Process.Release() == nil
}
