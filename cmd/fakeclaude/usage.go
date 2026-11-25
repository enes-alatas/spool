package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// usageMain plays `claude -p /usage`, which the hub runs to learn a cloud
// login's plan usage (#1285). It prints $FAKECLAUDE_STATE's usage.txt, the
// table a test writes as the real CLI prints it, or a heading with no
// window while there is none, and adds a line to usage-reads there, so a
// test can count the reads. A run the API refuses (loginRefused) fails as
// the real CLI's would, on stderr with exit 1.
func usageMain() int {
	state := os.Getenv("FAKECLAUDE_STATE")
	if reads, err := os.OpenFile(filepath.Join(state, "usage-reads"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
		_, _ = reads.WriteString("read\n")
		_ = reads.Close()
	}
	if loginRefused(state) {
		fmt.Fprintln(os.Stderr, "Invalid API key · Please run /login")
		return 1
	}
	table, err := os.ReadFile(filepath.Join(state, "usage.txt"))
	if err != nil {
		fmt.Println("You are currently using your subscription to power your Claude Code usage")
		return 0
	}
	_, _ = os.Stdout.Write(table)
	return 0
}
