package docker

import (
	"context"
	"strconv"
	"strings"
)

// EngineVersion is the version the daemon reports for itself, as
// `docker version --format '{{.Server.Version}}'` prints it.
func (rt *Runtime) EngineVersion(ctx context.Context) (string, error) {
	out, err := rt.command(ctx, queryTimeout, "version", "--format", "{{.Server.Version}}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// ForwardsOutsideNames reports whether an engine at version answers a
// workstation's query for an outside name by asking the host's resolvers,
// past the egress allowlist (CVE-2024-29018, #899). Moby fixed it in
// 26.0.0, 25.0.5 and 23.0.11; no 24.x has the fix. A version it cannot read
// is not reported: the hub cannot tell, and a distribution that backported
// the fix reports the older number anyway, so the answer is a warning and
// never a refusal.
func ForwardsOutsideNames(version string) bool {
	release, ok := parseRelease(version)
	if !ok {
		return false
	}
	switch release[0] {
	case 23:
		return olderThan(release, [3]int{23, 0, 11})
	case 25:
		return olderThan(release, [3]int{25, 0, 5})
	}
	return release[0] < 26
}

// parseRelease reads the major, minor and patch numbers at the front of a
// version such as 26.1.4, 20.10.24+dfsg1 or 25.0.0-rc.1. A missing minor or
// patch reads as 0.
func parseRelease(version string) (release [3]int, ok bool) {
	numeric := version
	if end := strings.IndexFunc(version, func(char rune) bool { return char != '.' && (char < '0' || char > '9') }); end >= 0 {
		numeric = version[:end]
	}
	parts := strings.SplitN(numeric, ".", 3)
	for index, part := range parts {
		number, err := strconv.Atoi(part)
		if err != nil {
			return release, index > 0
		}
		release[index] = number
	}
	return release, true
}

func olderThan(release, fixed [3]int) bool {
	for index := range release {
		if release[index] != fixed[index] {
			return release[index] < fixed[index]
		}
	}
	return false
}
