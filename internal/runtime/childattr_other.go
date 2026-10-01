//go:build !linux

package runtime

import "syscall"

// ChildAttr is the SysProcAttr for a process a runtime starts on the
// hub's behalf. Only Linux can signal a child when its parent dies; here
// a crashed hub's children are left to see their stdin close, and to the
// next run's Reap and leftover sweeps (#497).
func ChildAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{}
}
