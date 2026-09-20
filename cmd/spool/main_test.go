package main

import "testing"

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

// The docker default and the --mcp-listen default cannot both be taken: a
// workstation comes in over the bridge, which has no route to loopback. The
// hub warns rather than refuses, so a fleet running only bare loops on a
// docker-equipped machine still starts.
func TestIsLoopback(t *testing.T) {
	for _, tc := range []struct {
		host string
		want bool
	}{
		{host: "127.0.0.1", want: true},
		{host: "localhost", want: true},
		{host: "::1", want: true},
		{host: "0.0.0.0", want: false},
		{host: "", want: false},
		{host: "::", want: false},
		{host: "no-such-host.invalid", want: false},
	} {
		if got := isLoopback(tc.host); got != tc.want {
			t.Errorf("isLoopback(%q) = %v, want %v", tc.host, got, tc.want)
		}
	}
}
