package runtime

import "syscall"

// ChildAttr is the SysProcAttr for a process a runtime starts on the
// hub's behalf: Linux signals it when the hub dies, so a crashed hub
// leaves no claude or docker client running behind it. Every runtime's
// Reap still sweeps what a previous run left, which is all other
// platforms get (#497).
func ChildAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM}
}
