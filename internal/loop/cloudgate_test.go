package loop

import "testing"

// The gate holds cloud loops until a check passes, lets them wake for
// CloudCheckFresh after it, and holds again once the pass is stale, asking
// for a check then. A failed check or no address holds without asking, so
// a failing address is not checked in a loop (ADR-0056).
func TestCloudGateHoldsUntilAFreshPass(t *testing.T) {
	const address = "https://hub.example.test"
	gate := NewCloudGate(func() (string, error) { return address, nil }, signedIn)
	notified := 0
	gate.OnChange(func() { notified++ })
	fresh := CloudCheckFresh.Milliseconds()

	if !gate.Holds(1000) || asked(gate) {
		t.Fatal("a gate no check has passed must hold, without asking for a check")
	}

	gate.Record(address, true, 1000)
	if gate.Holds(1000+fresh-1) || gate.FailedSince() != 0 || !gate.Passed() || !gate.PassedFor(address, 1000+fresh-1) {
		t.Fatal("a fresh pass must let cloud loops wake")
	}
	if !gate.Holds(1000+fresh) || !asked(gate) || gate.PassedFor(address, 1000+fresh) {
		t.Fatal("a stale pass must hold and ask for a check")
	}
	gate.Holds(1000 + fresh)
	gate.Holds(1000 + fresh)
	if !asked(gate) || asked(gate) {
		t.Fatal("asks while one is outstanding must be one ask")
	}

	gate.Record(address, false, 5000)
	gate.Record(address, false, 9000)
	if !gate.Holds(9000) || asked(gate) || gate.FailedSince() != 5000 || gate.Passed() {
		t.Fatalf("failed checks must hold without asking, dated from the first: since %d", gate.FailedSince())
	}

	gate.Clear()
	if !gate.Holds(9000) || asked(gate) || gate.FailedSince() != 0 || gate.Passed() {
		t.Fatal("no address must hold without asking, and is no failure")
	}
	if notified != 4 {
		t.Fatalf("OnChange heard %d checks, want 4", notified)
	}
}

// A pass is for the address it checked: once another is stored, the gate
// holds and asks for a check, and the wake refuses the new address until
// that check passes on it (ADR-0056).
func TestCloudGatePassIsForItsAddress(t *testing.T) {
	stored := "https://old.example.test"
	gate := NewCloudGate(func() (string, error) { return stored, nil }, signedIn)
	gate.Record(stored, true, 1000)
	if gate.Holds(1000) {
		t.Fatal("a fresh pass on the stored address must let cloud loops wake")
	}

	stored = "https://new.example.test"
	if !gate.Holds(1001) || !asked(gate) {
		t.Fatal("a pass on another address must hold and ask for a check")
	}
	if gate.PassedFor(stored, 1001) {
		t.Fatal("the wake must refuse an address no check passed on")
	}
	gate.Record(stored, true, 2000)
	if gate.Holds(2000) || !gate.PassedFor(stored, 2000) {
		t.Fatal("a pass on the new address must let cloud loops wake")
	}
}

// asked reports whether the gate has asked for a check, taking the ask.
func asked(gate *CloudGate) bool {
	select {
	case <-gate.Rechecks():
		return true
	default:
		return false
	}
}

// signedIn is a gate's account reader with one signed in.
func signedIn() (bool, error) { return true, nil }

// With no account signed in the gate holds, whatever the checks found,
// without asking for one; a sign-in tells the loops (ADR-0056).
func TestCloudGateHoldsWithNoAccount(t *testing.T) {
	const address = "https://hub.example.test"
	account := false
	gate := NewCloudGate(func() (string, error) { return address, nil }, func() (bool, error) { return account, nil })
	notified := 0
	gate.OnChange(func() { notified++ })
	gate.Record(address, true, 1000)
	if !gate.Holds(1000) || asked(gate) {
		t.Fatal("a gate with no account must hold, without asking for a check")
	}
	account = true
	gate.AccountChanged()
	if gate.Holds(1000) || notified != 2 {
		t.Fatalf("after a sign-in: holds %v, OnChange heard %d, want open and 2", gate.Holds(1000), notified)
	}
}
