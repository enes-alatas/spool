package egress

import (
	"slices"
	"testing"
)

// A host stays as it is, a domain rule is its domain and everything under
// it, and an entry with a port of its own, a stored * or a stored domain of
// one label has no form in a cloud environment, so it is left off and
// named.
func TestEnvironmentHostsTranslateTheHubsEntries(t *testing.T) {
	hosts, notApplied := EnvironmentHosts([]string{
		"pypi.org", ".example.test", "registry.example.test:8443", "example.test", "pypi.org", "registry.example.test:8443", "*", "*.stored.test", ".stored-tld",
	})
	if want := []string{"pypi.org", "*.example.test", "example.test"}; !slices.Equal(hosts, want) {
		t.Errorf("hosts = %q, want %q", hosts, want)
	}
	if want := []string{"registry.example.test:8443", "*", "*.stored.test", ".stored-tld"}; !slices.Equal(notApplied, want) {
		t.Errorf("not applied = %q, want %q", notApplied, want)
	}
}
