package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// The two listeners are what keeps the operator's API out of a workstation's
// reach (#238), so an --mcp-listen that collides with --listen has to be
// refused rather than quietly allowlisted.
func TestSplitMCPListen(t *testing.T) {
	cases := []struct {
		name    string
		api     string
		mcp     string
		want    string
		wantErr bool
	}{
		{name: "defaults", api: "127.0.0.1:8080", mcp: "127.0.0.1:8081", want: "8081"},
		{name: "mcp on the bridge, api on loopback", api: "127.0.0.1:8080", mcp: "0.0.0.0:8081", want: "8081"},
		{name: "same host and port", api: "127.0.0.1:8080", mcp: "127.0.0.1:8080", wantErr: true},
		{name: "api wildcard covers the mcp host", api: "0.0.0.0:8080", mcp: "127.0.0.1:8080", wantErr: true},
		{name: "mcp wildcard covers the api host", api: "127.0.0.1:8080", mcp: "0.0.0.0:8080", wantErr: true},
		{name: "different hosts, same port", api: "127.0.0.1:8080", mcp: "192.168.1.5:8080", want: "8080"},
		{name: "no port", api: "127.0.0.1:8080", mcp: "127.0.0.1", wantErr: true},
		{name: "empty port", api: "127.0.0.1:8080", mcp: "127.0.0.1:", wantErr: true},
		// one socket spelled two ways is still one socket
		{name: "localhost and its address", api: "localhost:8080", mcp: "127.0.0.1:8080", wantErr: true},
		// the kernel picks each, and never picks a port already bound
		{name: "both ports left to the kernel", api: "127.0.0.1:0", mcp: "127.0.0.1:0", want: "0"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, got, err := splitMCPListen(tc.api, tc.mcp)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("splitMCPListen(%q, %q) = %q, want an error", tc.api, tc.mcp, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("splitMCPListen(%q, %q): %v", tc.api, tc.mcp, err)
			}
			if got != tc.want {
				t.Errorf("splitMCPListen(%q, %q) = %q, want %q", tc.api, tc.mcp, got, tc.want)
			}
		})
	}
}

// A workstation's view of the hub turns on whether the bridge gateway is
// an address of this machine (#474): loopback always is, a documentation
// address never is, and a name is not an address.
func TestIsLocalAddress(t *testing.T) {
	for _, tc := range []struct {
		addr string
		want bool
	}{
		{addr: "127.0.0.1", want: true},
		{addr: "192.0.2.1", want: false},
		{addr: "localhost", want: false},
		{addr: "", want: false},
	} {
		if got := isLocalAddress(tc.addr); got != tc.want {
			t.Errorf("isLocalAddress(%q) = %v, want %v", tc.addr, got, tc.want)
		}
	}
}

// brokenListener is a listener that bound and then stopped accepting, which
// is how a serve failure after the bind reaches the hub.
type brokenListener struct{ net.Listener }

func (brokenListener) Accept() (net.Conn, error) {
	return nil, errors.New("accept: too many open files")
}

// A listener that fails after its bind is a serve failure: it names the
// listener and its address, and the other listener stops with it rather
// than leaving a hub serving half of itself (#253).
func TestServeFailsWithEitherListener(t *testing.T) {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	healthy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	bound, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	api := &http.Server{}
	mcp := &http.Server{}

	done := make(chan error, 1)
	go func() {
		done <- serve(context.Background(), quiet,
			served{name: "api", srv: api, listener: healthy, addr: "127.0.0.1:8080"},
			served{name: "mcp", srv: mcp, listener: brokenListener{bound}, addr: "127.0.0.1:8081"})
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "mcp listener on 127.0.0.1:8081") {
			t.Fatalf("serve returned %v, want the mcp listener's failure naming its address", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve kept running after a listener failed")
	}
	// the api server may not have reached Serve yet when serve returns; it
	// closes the listener as it gets there, and finds itself shut down
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		conn, err := net.Dial("tcp", healthy.Addr().String())
		if err != nil {
			break
		}
		_ = conn.Close()
		if time.Now().After(deadline) {
			t.Fatal("the api listener still accepts after the mcp listener failed")
		}
	}
}

// A shutdown the operator asked for is not a failure.
func TestServeStopsCleanlyOnShutdown(t *testing.T) {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- serve(ctx, quiet, served{name: "api", srv: &http.Server{}, listener: listener, addr: "127.0.0.1:8080"})
	}()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve returned %v after a shutdown, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve kept running after its context ended")
	}
}

// With no --mcp-listen the hub binds where docker workstations reach it:
// the bridge gateway when it is an address of this machine, and loopback
// whenever it is not, docker cannot say, or the gateway will not bind
// (#475). A named address always wins.
func TestBindLoopListener(t *testing.T) {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.Background()
	gatewayAt := func(addr string) func(context.Context) (string, error) {
		return func(context.Context) (string, error) { return addr, nil }
	}
	noDocker := func(context.Context) (string, error) { return "", errors.New("docker: command not found") }
	bindHost := func(t *testing.T, flagAddr, port string, gateway func(context.Context) (string, error)) string {
		t.Helper()
		listener, addr, err := bindLoopListener(ctx, quiet, "127.0.0.1:1", flagAddr, port, gateway)
		if err != nil {
			t.Fatalf("bindLoopListener: %v", err)
		}
		_ = listener.Close()
		host, _, _ := net.SplitHostPort(addr)
		if bound := listener.Addr().(*net.TCPAddr).IP.String(); bound != host {
			t.Fatalf("bound %s, reported %s", bound, addr)
		}
		return host
	}

	if got := bindHost(t, "127.0.0.1:0", "0", gatewayAt("192.0.2.1")); got != "127.0.0.1" {
		t.Errorf("a named address bound %s", got)
	}
	if got := bindHost(t, "", "0", nil); got != "127.0.0.1" {
		t.Errorf("a hub of bare loops bound %s, want loopback", got)
	}
	if got := bindHost(t, "", "0", noDocker); got != "127.0.0.1" {
		t.Errorf("with no docker the listener went to %s, want loopback", got)
	}
	if got := bindHost(t, "", "0", gatewayAt("192.0.2.1")); got != "127.0.0.1" {
		t.Errorf("with a bridge in a VM the listener went to %s, want loopback", got)
	}

	local := ""
	addrs, _ := net.InterfaceAddrs()
	for _, ifaceAddr := range addrs {
		if ipNet, ok := ifaceAddr.(*net.IPNet); ok && ipNet.IP.To4() != nil && !ipNet.IP.IsLoopback() {
			local = ipNet.IP.String()
			break
		}
	}
	if local == "" {
		t.Skip("no non-loopback IPv4 address on this machine to stand in for the bridge")
	}
	if got := bindHost(t, "", "0", gatewayAt(local)); got != local {
		t.Errorf("with the bridge on this machine at %s the listener went to %s", local, got)
	}
	taken, err := net.Listen("tcp", net.JoinHostPort(local, "0"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = taken.Close() }()
	_, port, _ := net.SplitHostPort(taken.Addr().String())
	if got := bindHost(t, "", port, gatewayAt(local)); got != "127.0.0.1" {
		t.Errorf("with the bridge's port taken the listener went to %s, want loopback", got)
	}
}
