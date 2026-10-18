//go:build integration

package itest

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// cloudSettings is GET /api/settings/cloud, as far as these tests read it.
type cloudSettings struct {
	Listener struct {
		On       bool   `json:"on"`
		Addr     string `json:"addr"`
		FromFlag bool   `json:"from_flag"`
	} `json:"listener"`
	Address string `json:"address"`
	Warning string `json:"warning"`
	Check   *struct {
		State  string `json:"state"`
		Stage  string `json:"stage"`
		Detail string `json:"detail"`
	} `json:"check"`
	Account struct {
		Set   bool `json:"set"`
		CLI   bool `json:"cli"`
		Check *struct {
			State string `json:"state"`
			Stage string `json:"stage"`
		} `json:"check"`
	} `json:"account"`
}

// loopbackAddress lets these hubs take a plain-HTTP loopback public
// address: the harness's stand-in for a tunnel is the listener itself.
var loopbackAddress = []string{"--runtime", "bare", "--cloud-loopback-address"}

// Settings → Cloud opens the cloud listener and checks the public address
// in front of it (ADR-0056). The check passes only when a
// request through the address reaches this hub's listener, names the stage
// a failed one stopped at, and the listener the page opened is open again
// after a restart. Only an admin switches or sets anything.
func TestSettingsCloudOpensTheListenerAndChecksTheAddress(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	s := startServerArgs(t, dataDir, loopbackAddress...)

	// Each answer is read into a fresh value, so a field it leaves out
	// reads as unset rather than as the last answer's.
	cloud := func(method, path string, body any) (view cloudSettings) {
		s.mustJSON(method, path, body, &view)
		return view
	}
	view := cloud("GET", "/api/settings/cloud", nil)
	if view.Listener.On || view.Address != "" || view.Check != nil || view.Account.Set {
		t.Fatalf("a fresh hub's cloud settings = %+v, want nothing set", view)
	}

	member := s.signedInAs("mia", "member")
	if resp, body := s.asCookie("GET", "/api/settings/cloud", member, nil); resp.StatusCode != http.StatusOK {
		t.Errorf("a member's read = %d %s", resp.StatusCode, body)
	}
	for _, call := range []struct{ method, path string }{
		{"PUT", "/api/settings/cloud/listener"}, {"PUT", "/api/settings/cloud/address"},
		{"DELETE", "/api/settings/cloud/address"}, {"POST", "/api/settings/cloud/check"},
	} {
		if resp, body := s.asCookie(call.method, call.path, member, map[string]any{"on": true}); resp.StatusCode != http.StatusForbidden {
			t.Errorf("a member's %s %s = %d %s, want 403", call.method, call.path, resp.StatusCode, body)
		}
	}

	s.wantRefusal("PUT", "/api/settings/cloud/address", map[string]string{"address": "http://hub.example.test"}, 400, "cloud_address_invalid")

	view = cloud("PUT", "/api/settings/cloud/listener", map[string]any{"on": true, "listen": "127.0.0.1:0"})
	if !view.Listener.On || !strings.HasPrefix(view.Listener.Addr, "127.0.0.1:") || view.Listener.FromFlag {
		t.Fatalf("after switching it on: %+v, want open on loopback, by the page", view.Listener)
	}
	s.wantRefusal("PUT", "/api/settings/cloud/listener", map[string]any{"on": true, "listen": "127.0.0.1:1"}, 409, "cloud_listener")
	// The harness's stand-in for a tunnel is the listener itself, on loopback.
	public := "http://" + view.Listener.Addr
	view = cloud("PUT", "/api/settings/cloud/address", map[string]string{"address": public + "/"})
	if view.Address != public || view.Check == nil || view.Check.State != "ok" {
		t.Fatalf("after setting the address: %+v %+v, want it stored and checked ok", view, view.Check)
	}

	// The API answers the check's path too, with no value of its own.
	view = cloud("PUT", "/api/settings/cloud/address", map[string]string{"address": s.baseURL})
	if view.Check == nil || view.Check.Stage != "other_server" {
		t.Fatalf("an address reaching another server: %+v, want other_server", view.Check)
	}
	var seen cloudSettings
	if resp, body := s.asCookie("GET", "/api/settings/cloud", member, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("a member's read = %d %s", resp.StatusCode, body)
	} else if err := json.Unmarshal(body, &seen); err != nil {
		t.Fatal(err)
	}
	if seen.Check == nil || seen.Check.Stage != "other_server" || seen.Check.Detail != "" || view.Check.Detail == "" {
		t.Errorf("a member reads %+v, the admin %+v; want the stage alone for the member", seen.Check, view.Check)
	}

	s.stop()
	s = startServerArgs(t, dataDir, loopbackAddress...)
	view = cloud("GET", "/api/settings/cloud", nil)
	if !view.Listener.On || view.Listener.FromFlag {
		t.Fatalf("after a restart: %+v, want the listener the page opened open again", view.Listener)
	}

	view = cloud("PUT", "/api/settings/cloud/listener", map[string]any{"on": false})
	if view.Listener.On || view.Check == nil || view.Check.Stage != "listener_off" {
		t.Fatalf("after switching it off: %+v %+v, want off, and the check saying so", view.Listener, view.Check)
	}
	view = cloud("DELETE", "/api/settings/cloud/address", nil)
	if view.Address != "" || view.Check != nil {
		t.Fatalf("after clearing the address: %+v, want none and no check", view)
	}

	s.stop()
	s = startServerArgs(t, dataDir, loopbackAddress...)
	view = cloud("GET", "/api/settings/cloud", nil)
	if view.Listener.On {
		t.Fatalf("after a restart with the listener switched off: %+v, want it off", view.Listener)
	}
}

// A cloud listener --cloud-listen opened is reported, and only a restart
// without the flag closes it. A real hub refuses a loopback address.
func TestAFlagOpenedCloudListenerStaysOpen(t *testing.T) {
	t.Parallel()
	s := startServerArgs(t, t.TempDir(), "--runtime", "bare", "--cloud-listen", "0")
	var view cloudSettings
	s.mustJSON("GET", "/api/settings/cloud", nil, &view)
	if !view.Listener.On || !view.Listener.FromFlag || "http://"+view.Listener.Addr != s.cloudURL {
		t.Fatalf("listener = %+v, want the flag's, on %s", view.Listener, s.cloudURL)
	}
	s.wantRefusal("PUT", "/api/settings/cloud/listener", map[string]any{"on": false}, 409, "cloud_listener")
	// Without the tests' opt-in, as on any real hub, the listener's own
	// loopback address is refused: the hub would check it green from
	// itself, though no cloud session can reach it.
	s.wantRefusal("PUT", "/api/settings/cloud/address", map[string]string{"address": s.cloudURL}, 400, "cloud_address_invalid")
}
