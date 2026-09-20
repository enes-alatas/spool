// Package version reports which Spool this is: the build's git description,
// its commit, when it was built and which Go compiled it.
//
// The value comes from the build, not from a constant anyone has to remember
// to bump — `make server` passes `git describe` through -ldflags. A build
// that skipped the Makefile still says something true rather than nothing:
// Go records the revision it built from in the binary, so `go build ./cmd/...`
// and `go install` both report their commit, and only a build from outside a
// git checkout falls all the way through to "dev".
package version

import (
	"runtime"
	"runtime/debug"
	"strings"
)

// Dev is what a build reports when nothing knows where it came from.
const Dev = "dev"

// Info is one build's identity, as the API serves it and --version prints it.
type Info struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	BuiltAt string `json:"built_at"`
	Go      string `json:"go"`
}

// Resolve fills in whatever the linker did not. Each field falls back on its
// own: a build with -X main.commit set but no version is odd, and answering
// it with a version derived from that commit beats answering "dev".
func Resolve(version, commit, builtAt string) Info {
	bi, ok := debug.ReadBuildInfo()
	return resolve(version, commit, builtAt, bi, ok)
}

func resolve(version, commit, builtAt string, bi *debug.BuildInfo, ok bool) Info {
	info := Info{
		Version: strings.TrimSpace(version),
		Commit:  strings.TrimSpace(commit),
		BuiltAt: strings.TrimSpace(builtAt),
		Go:      runtime.Version(),
	}
	var revision, vcsTime string
	var dirty bool
	if ok && bi != nil {
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				revision = s.Value
			case "vcs.time":
				vcsTime = s.Value
			case "vcs.modified":
				dirty = s.Value == "true"
			}
		}
	}
	if info.Commit == "" {
		info.Commit = shortCommit(revision)
	}
	if info.BuiltAt == "" {
		info.BuiltAt = vcsTime
	}
	if info.Version == "" {
		// No tag to describe, so the revision stands in for one and carries
		// the same -dirty mark `git describe --dirty` would have added.
		info.Version = shortCommit(revision)
		if info.Version != "" && dirty {
			info.Version += "-dirty"
		}
	}
	if info.Version == "" {
		info.Version = Dev
	}
	return info
}

// String is the --version line: everything on one line, in the order a human
// reads it — which build, from what, when, with which Go.
func (i Info) String() string {
	out := "spool " + i.Version
	var parts []string
	if i.Commit != "" {
		parts = append(parts, i.Commit)
	}
	if i.BuiltAt != "" {
		parts = append(parts, "built "+i.BuiltAt)
	}
	if i.Go != "" {
		parts = append(parts, i.Go)
	}
	if len(parts) > 0 {
		out += " (" + strings.Join(parts, ", ") + ")"
	}
	return out
}

// shortCommit trims a full revision to the seven characters git and GitHub
// both show, and leaves anything already short alone.
func shortCommit(revision string) string {
	if len(revision) > 7 {
		return revision[:7]
	}
	return revision
}
