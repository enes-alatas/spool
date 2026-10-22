//go:build integration

package itest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/enes-alatas/spool/internal/store"
	"github.com/enes-alatas/spool/internal/store/sqlite"
)

// egressSettings is GET /api/settings/egress, as much as these tests read.
type egressSettings struct {
	Enforced  bool      `json:"enforced"`
	Extra     []string  `json:"extra"`
	Flag      *[]string `json:"flag"`
	ChangedAt int64     `json:"changed_at"`
	AppliedAt int64     `json:"applied_at"`
}

// --egress-allow seeds the stored list on the first start, and the stored
// list wins from then on: a restart with another flag keeps what the
// operator edited, shows the flag beside it, and warns at the terminal,
// where an operator narrowing the flag expects it to cut a host. Adding a
// host already listed, or removing one that isn't, changes nothing (#542).
func TestEgressHostsSeedFromTheFlagOnce(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// No egress image: this hub filters nothing, so it writes into no proxy
	// another fleet on this daemon runs, and the list is still stored.
	s := startServerArgs(t, dir, "--runtime", "bare", "--egress-image", "",
		"--egress-allow", "PKG.example.dev,tracker.example:8443,pkg.example.dev")

	var seeded egressSettings
	s.mustJSON("GET", "/api/settings/egress", nil, &seeded)
	if want := []string{"pkg.example.dev", "tracker.example:8443"}; !reflect.DeepEqual(seeded.Extra, want) || seeded.Flag != nil {
		t.Fatalf("seeded view = %+v, want extra %q and no flag beside it", seeded, want)
	}
	if seeded.Enforced || seeded.AppliedAt != 0 {
		t.Fatalf("a hub without an egress image = %+v, want not enforced, never applied", seeded)
	}

	var same egressSettings
	s.mustJSON("POST", "/api/settings/egress/hosts", map[string]string{"host": "pkg.example.dev."}, &same)
	s.mustJSON("DELETE", "/api/settings/egress/hosts/"+url.PathEscape("absent.example"), nil, &same)
	if !reflect.DeepEqual(same, seeded) {
		t.Fatalf("after a duplicate add and an absent remove = %+v, want unchanged %+v", same, seeded)
	}

	var edited egressSettings
	s.mustJSON("DELETE", "/api/settings/egress/hosts/"+url.PathEscape("tracker.example:8443"), nil, &edited)
	s.mustJSON("POST", "/api/settings/egress/hosts", map[string]string{"host": ".internal.example"}, &edited)
	if want := []string{"pkg.example.dev", ".internal.example"}; !reflect.DeepEqual(edited.Extra, want) {
		t.Fatalf("edited extra = %q, want %q in the order added", edited.Extra, want)
	}
	if want := []string{"pkg.example.dev", "tracker.example:8443"}; edited.Flag == nil || !reflect.DeepEqual(*edited.Flag, want) {
		t.Fatalf("flag beside an edited list = %v, want %q", edited.Flag, want)
	}
	if strings.Contains(s.log(), "differs from the stored extra egress hosts") {
		t.Fatal("the hub that seeded the list warned that the flag differs from it")
	}
	s.stop()

	s = startServerArgs(t, dir, "--runtime", "bare", "--egress-image", "", "--egress-allow", "other.example")
	var restarted egressSettings
	s.mustJSON("GET", "/api/settings/egress", nil, &restarted)
	if !reflect.DeepEqual(restarted.Extra, edited.Extra) || restarted.ChangedAt != edited.ChangedAt {
		t.Fatalf("after a restart with another flag = %+v, want the stored %q kept", restarted, edited.Extra)
	}
	if want := []string{"other.example"}; restarted.Flag == nil || !reflect.DeepEqual(*restarted.Flag, want) {
		t.Fatalf("flag after the restart = %v, want %q", restarted.Flag, want)
	}
	if !strings.Contains(s.log(), "--egress-allow differs from the stored extra egress hosts; the stored list is in force") {
		t.Fatalf("a restart whose flag differs from the stored list must warn at the terminal; log:\n%s", s.log())
	}
	s.stop()

	// An empty flag is a flag: it shows as [] beside the stored list, and
	// warns. No flag at all shows none and says nothing.
	s = startServerArgs(t, dir, "--runtime", "bare", "--egress-image", "", "--egress-allow", "")
	var emptied egressSettings
	s.mustJSON("GET", "/api/settings/egress", nil, &emptied)
	if emptied.Flag == nil || len(*emptied.Flag) != 0 {
		t.Fatalf("flag for a hub started with an empty one = %v, want []", emptied.Flag)
	}
	if !strings.Contains(s.log(), "differs from the stored extra egress hosts") {
		t.Fatal("an empty flag beside a stored list must warn")
	}
	s.stop()

	s = startServerArgs(t, dir, "--runtime", "bare", "--egress-image", "")
	var flagless egressSettings
	s.mustJSON("GET", "/api/settings/egress", nil, &flagless)
	if flagless.Flag != nil || !reflect.DeepEqual(flagless.Extra, edited.Extra) {
		t.Fatalf("a hub started without the flag = %+v, want the stored list and no flag", flagless)
	}
	if strings.Contains(s.log(), "differs from the stored extra egress hosts") {
		t.Fatal("a hub started without the flag warned about it")
	}
}

// *.example.com is another spelling of the domain form: it is stored, shown
// and removed as .example.com, so no * is kept. An entry with a * stored
// before the hub refused one is still removed as written, without taking
// the domain rule beside it (#972).
func TestEgressHostsReadTheStarSpellingAsTheDomainForm(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := startServerArgs(t, dir, "--runtime", "bare", "--egress-image", "", "--egress-allow", "*.Flag.example")

	var added egressSettings
	s.mustJSON("POST", "/api/settings/egress/hosts", map[string]string{"host": "*.Example.dev"}, &added)
	s.mustJSON("POST", "/api/settings/egress/hosts", map[string]string{"host": ".example.dev"}, &added)
	if want := []string{".flag.example", ".example.dev"}; !reflect.DeepEqual(added.Extra, want) {
		t.Fatalf("extra after adding both spellings = %q, want %q", added.Extra, want)
	}
	if resp, body := s.do("POST", "/api/settings/egress/hosts", map[string]string{"host": "a*b.example.dev"}); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("a * inside a label = %d %s, want 400", resp.StatusCode, body)
	}
	var removed egressSettings
	s.mustJSON("DELETE", "/api/settings/egress/hosts/"+url.PathEscape("*.example.dev"), nil, &removed)
	if want := []string{".flag.example"}; !reflect.DeepEqual(removed.Extra, want) {
		t.Fatalf("extra after removing *.example.dev = %q, want %q", removed.Extra, want)
	}
	s.stop()

	db, err := sqlite.Open(filepath.Join(dir, "spool.db"))
	if err != nil {
		t.Fatal(err)
	}
	legacy, _ := json.Marshal(store.EgressHosts{Hosts: []string{"*.legacy.example", ".legacy.example"}, ChangedAt: 1})
	if err := db.Settings().Set(context.Background(), store.SettingEgressHosts, string(legacy)); err != nil {
		t.Fatal(err)
	}
	db.Close()

	s = startServerArgs(t, dir, "--runtime", "bare", "--egress-image", "")
	s.mustJSON("DELETE", "/api/settings/egress/hosts/"+url.PathEscape("*.legacy.example"), nil, &removed)
	if want := []string{".legacy.example"}; !reflect.DeepEqual(removed.Extra, want) {
		t.Fatalf("extra after removing the stored *.legacy.example = %q, want %q", removed.Extra, want)
	}
}

// An IPv4 literal is refused as an extra host in every spelling a resolver
// reads as one, as an IPv6 literal already was. One stored before the hub
// refused it permits nothing: the hub warns at boot, naming it, and it is
// removed as written (#901).
func TestEgressHostsRefuseAnIPv4Literal(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	hub := startServerArgs(t, dir, "--runtime", "bare", "--egress-image", "")
	for _, literal := range []string{"169.254.169.254", "10.0.0.1", "127.1", "2130706433", "0x7f000001", ".10.0.0.1"} {
		resp, body := hub.do("POST", "/api/settings/egress/hosts", map[string]string{"host": literal})
		if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), "IP literal") {
			t.Errorf("adding %s = %d %s, want 400 naming an IP literal", literal, resp.StatusCode, body)
		}
	}
	hub.stop()

	db, err := sqlite.Open(filepath.Join(dir, "spool.db"))
	if err != nil {
		t.Fatal(err)
	}
	legacy, _ := json.Marshal(store.EgressHosts{Hosts: []string{"169.254.169.254", "example.com"}, ChangedAt: 1})
	if err := db.Settings().Set(context.Background(), store.SettingEgressHosts, string(legacy)); err != nil {
		t.Fatal(err)
	}
	db.Close()

	hub = startServerArgs(t, dir, "--runtime", "bare", "--egress-image", "")
	// one warning, for the literal and not the name beside it
	var warnings []string
	for _, line := range strings.Split(hub.log(), "\n") {
		if strings.Contains(line, "a stored extra egress host is refused") {
			warnings = append(warnings, line)
		}
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "169.254.169.254") {
		t.Fatalf("boot warnings = %q, want one naming the stored IP literal", warnings)
	}
	var removed egressSettings
	hub.mustJSON("DELETE", "/api/settings/egress/hosts/"+url.PathEscape("169.254.169.254"), nil, &removed)
	if want := []string{"example.com"}; !reflect.DeepEqual(removed.Extra, want) {
		t.Fatalf("extra after removing the stored literal = %q, want %q", removed.Extra, want)
	}
}
