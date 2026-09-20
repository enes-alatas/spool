// Package egress holds the host allowlist a workstation's outbound traffic is
// measured against (ADR-0028). It is shared: the hub decides which hosts a
// workstation may reach, and the proxy binary that enforces it (cmd/spool-egress)
// links the same list, so there is exactly one definition of the default.
package egress

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// DefaultHosts is what a loop needs to do its job, and nothing else. Every
// entry earns its place; adding one is a PR with a reason.
//
// A leading dot means "this domain and anything under it"; everything else is
// an exact host. Names only — matching happens before the name is resolved
// (ADR-0028), so an address is never what is compared.
var DefaultHosts = []string{
	// Claude: the API the loop's own turns run against, plus the hosts the
	// CLI itself needs to authenticate and to check its version.
	"api.anthropic.com",
	"statsig.anthropic.com",
	"console.anthropic.com",
	"downloads.claude.ai",

	// GitHub: the work itself — gh, git over HTTPS, release and archive
	// downloads.
	"github.com",
	"api.github.com",
	"codeload.github.com",
	".githubusercontent.com",

	// Toolchains a loop installs into its persistent home.
	"proxy.golang.org",
	"sum.golang.org",
	"registry.npmjs.org",
	"deb.debian.org",
	"security.debian.org",
	"cli.github.com",
}

// DefaultPorts are the ports an entry without one of its own permits: the two
// the web is served on. An allowlisted host is not a blanket tunnel to every
// port it listens on — the gateway entry in particular names the hub's port
// and nothing else, so a loop cannot CONNECT to ssh or a database on the
// operator's own machine (ADR-0028).
var DefaultPorts = []string{"80", "443"}

// Allowlist decides whether a host may be reached, and on which port.
type Allowlist struct {
	exact   map[string]map[string]bool // host -> permitted ports
	domains []domainRule
}

type domainRule struct {
	suffix string // with its leading dot
	ports  map[string]bool
}

// New builds an allowlist from entries. An entry is a host, optionally with
// one port of its own ("host.docker.internal:8081", the hub's loop-facing
// listener — never its API port, #238); without a port it
// permits DefaultPorts. Entries are normalized (lowercase, trailing dot
// dropped), and anything Validate rejects — an empty entry from a stray
// comma, a pasted URL, a scheme where a port belongs — is left out.
func New(entries []string) *Allowlist {
	a := &Allowlist{exact: map[string]map[string]bool{}}
	for _, entry := range entries {
		// An entry that cannot be acted on is left out rather than kept as a
		// rule matching nothing: whoever accepted it reports it (Validate),
		// and the allowlist itself holds only rules that can fire.
		host, ports, err := parseEntry(entry)
		if err != nil {
			continue
		}
		switch {
		case host == "" || host == ".":
		case strings.HasPrefix(host, "."):
			a.domains = append(a.domains, domainRule{suffix: host, ports: ports})
		default:
			if a.exact[host] == nil {
				a.exact[host] = map[string]bool{}
			}
			for port := range ports {
				a.exact[host][port] = true
			}
		}
	}
	sort.Slice(a.domains, func(i, j int) bool { return a.domains[i].suffix < a.domains[j].suffix })
	return a
}

// Allows reports whether host may be reached on port. Both are the values the
// client asked for, already split apart by the caller — Allows does no parsing
// of its own, so handing it "example.com:443" as the host refuses.
func (a *Allowlist) Allows(host, port string) bool {
	host = normalize(host)
	if host == "" || port == "" {
		return false
	}
	if ports, ok := a.exact[host]; ok && ports[port] {
		return true
	}
	for _, d := range a.domains {
		// ".example.com" covers "api.example.com" and "example.com" itself,
		// which is what an operator writing the dotted form means.
		if strings.HasSuffix(host, d.suffix) || host == d.suffix[1:] {
			if d.ports[port] {
				return true
			}
		}
	}
	return false
}

// Entries returns what this allowlist permits, sorted, host:port per line —
// for logging what a proxy is enforcing.
func (a *Allowlist) Entries() []string {
	var entries []string
	for host, ports := range a.exact {
		entries = append(entries, render(host, ports)...)
	}
	for _, d := range a.domains {
		entries = append(entries, render(d.suffix, d.ports)...)
	}
	sort.Strings(entries)
	return entries
}

func render(host string, ports map[string]bool) []string {
	rendered := make([]string, 0, len(ports))
	for port := range ports {
		rendered = append(rendered, host+":"+port)
	}
	return rendered
}

// Validate reports what is wrong with an entry, or nil when it is one this
// allowlist can act on. An unusable entry permits nothing — it fails closed,
// which is safe but silent — so whoever accepts entries from an operator
// (`--egress-allow`, `--allow`) says so at startup rather than leaving a host
// mysteriously unreachable.
func Validate(entry string) error {
	_, _, err := parseEntry(entry)
	return err
}

// parseEntry reads one entry into the host and the ports it permits. It is
// the single definition of what an entry means: New keeps what it accepts,
// Validate reports what it rejects, and neither can drift from the other.
//
// The ports come back in the spelling a client will ask with — decimal, no
// sign, no leading zeros — because matching is on the string. "example.com:0443"
// is an ordinary typo with an unambiguous intent, so it becomes 443 rather
// than an entry that is live in the list and dead in practice.
func parseEntry(entry string) (host string, ports map[string]bool, err error) {
	rawHost, rawPort := cutPort(strings.TrimSpace(entry))
	if err := validHost(entry, rawHost); err != nil {
		return "", nil, err
	}
	permitted := map[string]bool{}
	if rawPort == "" {
		for _, port := range DefaultPorts {
			permitted[port] = true
		}
		return normalize(rawHost), permitted, nil
	}
	number, err := strconv.Atoi(rawPort)
	if err != nil {
		return "", nil, fmt.Errorf("%q: %q is not a port number — the allowlist names ports, not schemes", entry, rawPort)
	}
	if number < 1 || number > 65535 {
		return "", nil, fmt.Errorf("%q: port %d is outside 1-65535", entry, number)
	}
	permitted[strconv.Itoa(number)] = true
	return normalize(rawHost), permitted, nil
}

// validHost holds an entry's host to what a client can actually ask for: a
// name. A leading dot (the domain form) is the only punctuation with meaning
// here, and a trailing one is normalized away.
func validHost(entry, host string) error {
	switch {
	case strings.TrimSpace(host) == "":
		return fmt.Errorf("%q: no host", entry)
	case strings.Contains(host, "/"):
		return fmt.Errorf("%q: a host, not a URL — drop the scheme and path", entry)
	case strings.ContainsAny(host, ":[]"):
		// cutPort split at the last colon, so a colon still here means an
		// IPv6 literal. The allowlist is names only: matching happens before
		// resolution, so an address has nothing to match (ADR-0028).
		return fmt.Errorf("%q: host names only — an IP literal cannot be allowlisted", entry)
	case strings.ContainsAny(host, " \t\r\n@?#"):
		return fmt.Errorf("%q: %q is not a host name", entry, host)
	}
	for _, label := range strings.Split(strings.Trim(normalize(host), "."), ".") {
		if label == "" {
			return fmt.Errorf("%q: %q is not a host name", entry, host)
		}
	}
	return nil
}

// cutPort splits an entry at its last colon. Both halves come back raw: what
// counts as a port is parseEntry's judgement, and its complaint needs the text
// the operator actually wrote.
func cutPort(entry string) (host, port string) {
	colon := strings.LastIndex(entry, ":")
	if colon < 0 {
		return entry, ""
	}
	return entry[:colon], strings.TrimSpace(entry[colon+1:])
}

func normalize(host string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
}
