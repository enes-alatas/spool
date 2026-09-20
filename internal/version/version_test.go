package version

import (
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
)

func buildInfo(settings map[string]string) *debug.BuildInfo {
	bi := &debug.BuildInfo{}
	for k, v := range settings {
		bi.Settings = append(bi.Settings, debug.BuildSetting{Key: k, Value: v})
	}
	return bi
}

// TestResolveFallbackOrder pins the order the issue fixes: what the linker
// was told wins, what Go recorded at build comes next, and "dev" is only for
// a build that came from neither.
func TestResolveFallbackOrder(t *testing.T) {
	recorded := buildInfo(map[string]string{
		"vcs.revision": "abcdef1234567890",
		"vcs.time":     "2026-09-20T10:00:00Z",
		"vcs.modified": "false",
	})

	for _, tc := range []struct {
		name                       string
		version, commit, builtAt   string
		bi                         *debug.BuildInfo
		ok                         bool
		wantVer, wantCom, wantTime string
	}{
		{
			name:    "ldflags win over what Go recorded",
			version: "v0.2.0", commit: "1111111", builtAt: "2026-09-20T12:00:00Z",
			bi: recorded, ok: true,
			wantVer: "v0.2.0", wantCom: "1111111", wantTime: "2026-09-20T12:00:00Z",
		},
		{
			name: "a go build in a clean checkout reports its revision",
			bi:   recorded, ok: true,
			wantVer: "abcdef1", wantCom: "abcdef1", wantTime: "2026-09-20T10:00:00Z",
		},
		{
			name: "outside git, and outside a build that recorded anything",
			bi:   buildInfo(nil), ok: true,
			wantVer: Dev,
		},
		{
			name:    "no build info at all",
			ok:      false,
			wantVer: Dev,
		},
		{
			name:   "each field falls back on its own",
			commit: "2222222",
			bi:     recorded, ok: true,
			wantVer: "abcdef1", wantCom: "2222222", wantTime: "2026-09-20T10:00:00Z",
		},
		{
			name:    "whitespace from a shell that produced nothing is not a version",
			version: "  ", commit: "\n",
			bi: buildInfo(nil), ok: true,
			wantVer: Dev,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := resolve(tc.version, tc.commit, tc.builtAt, tc.bi, tc.ok)
			if got.Version != tc.wantVer {
				t.Errorf("version = %q, want %q", got.Version, tc.wantVer)
			}
			if got.Commit != tc.wantCom {
				t.Errorf("commit = %q, want %q", got.Commit, tc.wantCom)
			}
			if got.BuiltAt != tc.wantTime {
				t.Errorf("built_at = %q, want %q", got.BuiltAt, tc.wantTime)
			}
			if got.Go != runtime.Version() {
				t.Errorf("go = %q, want the compiler that built this", got.Go)
			}
		})
	}
}

// TestDirtyRevisionSaysSo: a build from a modified checkout must not claim to
// be the commit it was built from — `git describe --dirty` would not, and the
// fallback is standing in for it.
func TestDirtyRevisionSaysSo(t *testing.T) {
	got := resolve("", "", "", buildInfo(map[string]string{
		"vcs.revision": "abcdef1234567890",
		"vcs.modified": "true",
	}), true)
	if got.Version != "abcdef1-dirty" {
		t.Errorf("version = %q, want the revision marked dirty", got.Version)
	}
	// The commit itself is the commit; only the version carries the mark.
	if got.Commit != "abcdef1" {
		t.Errorf("commit = %q, want the plain revision", got.Commit)
	}
}

func TestStringIsOneReadableLine(t *testing.T) {
	line := Info{Version: "v0.2.0", Commit: "abcdef1", BuiltAt: "2026-09-20T12:00:00Z", Go: "go1.25.0"}.String()
	want := "spool v0.2.0 (abcdef1, built 2026-09-20T12:00:00Z, go1.25.0)"
	if line != want {
		t.Errorf("--version prints\n%q\nwant\n%q", line, want)
	}
	if strings.Contains(Info{Version: Dev, Go: "go1.25.0"}.String(), ", ,") {
		t.Error("a build with nothing but a version prints empty slots")
	}
	if got := (Info{Version: Dev, Go: "go1.25.0"}).String(); got != "spool dev (go1.25.0)" {
		t.Errorf("dev build prints %q", got)
	}
}
