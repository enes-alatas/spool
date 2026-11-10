package dialguard

import (
	"net/netip"
	"testing"
)

// A guarded dial reaches this machine only for a loopback url, and
// otherwise no loopback, unspecified, link-local, multicast or cloud
// metadata address, however it is spelled.
func TestMayDial(t *testing.T) {
	for _, tc := range []struct {
		addr     string
		loopback bool
		want     bool
	}{
		{"127.0.0.1", true, true},
		{"::1", true, true},
		{"0.0.0.0", true, true},
		{"::", true, true},
		{"100.100.100.200", false, false},
		{"100.100.100.201", false, true},
		{"93.184.215.14", true, false},
		{"93.184.215.14", false, true},
		{"10.0.0.5", false, true},
		{"127.0.0.1", false, false},
		{"::ffff:127.0.0.1", false, false},
		{"0.0.0.0", false, false},
		{"169.254.169.254", false, false},
		{"::ffff:169.254.169.254", false, false},
		{"fe80::1", false, false},
		{"fd00:ec2::254", false, false},
		{"224.0.0.1", false, false},
	} {
		if got := MayDial(netip.MustParseAddr(tc.addr), tc.loopback); got != tc.want {
			t.Errorf("MayDial(%s, loopback %v) = %v, want %v", tc.addr, tc.loopback, got, tc.want)
		}
	}
}
