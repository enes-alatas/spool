package operator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enes-alatas/spool/internal/datadir"
)

func TestLoadMintsOncePrivate(t *testing.T) {
	dir := t.TempDir()

	token, minted, err := Load(dir)
	if err != nil {
		t.Fatalf("first load: %v", err)
	}
	if !minted {
		t.Error("the first load must report that it minted the token — it is the hub's cue to print it")
	}
	if len(token) < 32 || strings.ContainsAny(token, " \n") {
		t.Errorf("token = %q, want a long opaque value on one line", token)
	}

	info, err := os.Stat(Path(dir))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != datadir.FileMode {
		t.Errorf("token file mode = %o, want %o — another local account must not read it", got, datadir.FileMode)
	}

	// The same fleet gets the same credential back, and is not told it was
	// minted a second time: a hub that reprinted the token on every restart
	// would scatter it through the operator's scrollback.
	again, minted, err := Load(dir)
	if err != nil {
		t.Fatalf("second load: %v", err)
	}
	if again != token {
		t.Errorf("second load = %q, want the stored %q", again, token)
	}
	if minted {
		t.Error("a token that already existed was reported as minted")
	}
}

// A data directory restored from a backup, or written under a wider umask,
// can hold a token every account on the machine can read. Loading it narrows
// it, on the same reasoning as the rest of the directory (#149).
func TestLoadNarrowsAWidenedToken(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := Load(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(Path(dir), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Load(dir); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(Path(dir))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != datadir.FileMode {
		t.Errorf("token file mode = %o after reload, want %o", got, datadir.FileMode)
	}
}

// An empty token file must not become an empty token: Matches would then be
// asked to compare "" against "", and every unauthenticated request would be
// the operator's.
func TestLoadRefusesAnEmptyTokenFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, TokenFile), []byte("\n"), datadir.FileMode); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Load(dir); err == nil {
		t.Fatal("an empty token file must be an error, not an empty credential")
	}
}

func TestMatches(t *testing.T) {
	const token = "fixture-operator-token-aaaa"
	cases := []struct {
		name string
		want string
		got  string
		ok   bool
	}{
		{name: "the same token", want: token, got: token, ok: true},
		{name: "a different token", want: token, got: "not-it", ok: false},
		{name: "a prefix of it", want: token, got: token[:8], ok: false},
		{name: "nothing presented", want: token, got: "", ok: false},
		// the case that matters most: a hub with no token configured must
		// not accept a caller who also presents nothing
		{name: "nothing against nothing", want: "", got: "", ok: false},
		{name: "anything against no token", want: "", got: token, ok: false},
	}
	for _, tc := range cases {
		if got := Matches(tc.want, tc.got); got != tc.ok {
			t.Errorf("%s: Matches(%q, %q) = %v, want %v", tc.name, tc.want, tc.got, got, tc.ok)
		}
	}
}
