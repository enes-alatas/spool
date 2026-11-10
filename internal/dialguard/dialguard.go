// Package dialguard judges the address a hub dial connects to, after
// resolution, for the calls the hub makes to an address an admin typed:
// a connection check (#1323) and the Langfuse read (#1300). A name that
// resolves somewhere else at dial time than when it was saved gains
// nothing.
package dialguard

import (
	"errors"
	"net"
	"net/netip"
	"slices"
	"syscall"
	"time"
)

// ErrRefused is the guard refusing the address a name resolved to.
var ErrRefused = errors.New("address refused")

// metadataAddrs are the cloud instance metadata addresses outside
// link-local, which the guard refuses by range: AWS's on IPv6, a
// unique-local address, and Alibaba Cloud's, in the shared address space.
var metadataAddrs = []netip.Addr{
	netip.MustParseAddr("fd00:ec2::254"),
	netip.MustParseAddr("100.100.100.200"),
}

// MayDial reports whether a guarded dial may connect to addr. For a
// loopback url that is this machine, an unspecified address included;
// for any other, no loopback, unspecified, link-local, multicast or
// cloud metadata address.
func MayDial(addr netip.Addr, loopback bool) bool {
	addr = addr.Unmap()
	if loopback {
		return addr.IsLoopback() || addr.IsUnspecified()
	}
	return addr.IsValid() && !addr.IsLoopback() && !addr.IsUnspecified() &&
		!addr.IsLinkLocalUnicast() && !addr.IsLinkLocalMulticast() && !addr.IsMulticast() &&
		!slices.Contains(metadataAddrs, addr)
}

// Dialer dials only what MayDial allows, giving up after timeout. Its
// transport must set no Proxy: through a proxy the guard would judge
// the proxy.
func Dialer(timeout time.Duration, loopback bool) *net.Dialer {
	return &net.Dialer{
		Timeout: timeout,
		Control: func(_, address string, _ syscall.RawConn) error {
			addrPort, err := netip.ParseAddrPort(address)
			if err != nil || !MayDial(addrPort.Addr(), loopback) {
				return ErrRefused
			}
			return nil
		},
	}
}
