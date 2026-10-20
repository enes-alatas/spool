//go:build !linux

package main

import "os"

// isTerminal is false off Linux, where tier 2 doesn't run.
func isTerminal(*os.File) bool { return false }
