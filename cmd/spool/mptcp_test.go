package main

import (
	"net"
	"testing"
)

// TestHubListenersAreNotMPTCP: go.mod's godebug block turns off the MPTCP
// listeners Go has opened by default since 1.24, so every hub listener is
// plain TCP (ADR-0062).
func TestHubListenersAreNotMPTCP(t *testing.T) {
	var config net.ListenConfig
	if config.MultipathTCP() {
		t.Fatal("a listener the hub opens would use MPTCP; want go.mod's godebug multipathtcp=0")
	}
}
