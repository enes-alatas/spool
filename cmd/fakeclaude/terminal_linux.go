package main

import (
	"os"
	"syscall"
	"unsafe"
)

// isTerminal reports whether file is a terminal, as the CLI checks before
// starting a cloud session.
func isTerminal(file *os.File) bool {
	var termios syscall.Termios
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, file.Fd(), syscall.TCGETS, uintptr(unsafe.Pointer(&termios)))
	return errno == 0
}
