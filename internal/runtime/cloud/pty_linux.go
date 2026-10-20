package cloud

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"unsafe"
)

// startOnTerminal starts cmd on a fresh pseudo-terminal, as its session's
// controlling terminal on stdin, stdout and stderr, and returns the
// terminal's other end: `claude --cloud` refuses to start a session without
// one (ADR-0059). The caller reads it, so the CLI never blocks on a full
// terminal, and closes it once cmd has exited.
func startOnTerminal(cmd *exec.Cmd) (*os.File, error) {
	terminal, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("cloud: opening a terminal: %w", err)
	}
	var unlock int32
	var number uint32
	if err := ioctl(terminal, syscall.TIOCSPTLCK, unsafe.Pointer(&unlock)); err != nil {
		_ = terminal.Close()
		return nil, fmt.Errorf("cloud: unlocking the terminal: %w", err)
	}
	if err := ioctl(terminal, syscall.TIOCGPTN, unsafe.Pointer(&number)); err != nil {
		_ = terminal.Close()
		return nil, fmt.Errorf("cloud: naming the terminal: %w", err)
	}
	side, err := os.OpenFile("/dev/pts/"+strconv.FormatUint(uint64(number), 10), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		_ = terminal.Close()
		return nil, fmt.Errorf("cloud: opening the terminal's side: %w", err)
	}
	defer side.Close() // the child holds its own copies
	// a terminal of no size is one a TUI may refuse to draw on
	size := struct{ rows, cols, x, y uint16 }{rows: 50, cols: 200}
	if err := ioctl(side, syscall.TIOCSWINSZ, unsafe.Pointer(&size)); err != nil {
		_ = terminal.Close()
		return nil, fmt.Errorf("cloud: sizing the terminal: %w", err)
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = side, side, side
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		_ = terminal.Close()
		return nil, err
	}
	return terminal, nil
}

func ioctl(file *os.File, request uintptr, arg unsafe.Pointer) error {
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, file.Fd(), request, uintptr(arg)); errno != 0 {
		return errno
	}
	return nil
}

// killTerminal ends a command startOnTerminal started, with every process
// it started in turn: they share its process group, and one left behind
// would hold the terminal open.
func killTerminal(cmd *exec.Cmd) {
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
