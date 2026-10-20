package cloud

import (
	"strings"

	"github.com/enes-alatas/spool/internal/egress"
)

// Allowlist translates entries of the hub's egress allowlist (ADR-0028)
// into a cloud environment's allowed hosts, in order and without repeats.
// A host stays as it is. A domain rule, .example.com, is the domain and
// everything under it, which a cloud environment spells *.example.com and
// example.com (#861). An entry with a port of its own has no form there:
// it is left off and returned in notApplied, as is one holding a *, which
// the hub no longer stores (a leading *. comes in as the dotted form, #972)
// but may have stored before: the wall matches it as a name nothing has, an
// environment as a wildcard. So the list is never
// wider than the hub's (ADR-0056).
func Allowlist(entries []string) (hosts, notApplied []string) {
	seen := map[string]bool{}
	add := func(host string) {
		if !seen[host] {
			seen[host] = true
			hosts = append(hosts, host)
		}
	}
	for _, entry := range entries {
		switch {
		case egress.HasPort(entry), strings.Contains(entry, "*"):
			if !seen[entry] {
				seen[entry] = true
				notApplied = append(notApplied, entry)
			}
		case strings.HasPrefix(entry, "."):
			add("*" + entry)
			add(entry[1:])
		default:
			add(entry)
		}
	}
	return hosts, notApplied
}
