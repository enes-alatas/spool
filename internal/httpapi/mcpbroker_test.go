package httpapi

import (
	"net/url"
	"testing"
)

// A brokered request goes to the server's URL exactly as stored when the
// client adds nothing, and keeps whatever path and query it does add after
// the connection's name (#622).
func TestBrokeredURL(t *testing.T) {
	for _, tc := range []struct {
		target, rest, query, want string
	}{
		{"https://mcp.example.test/mcp", "", "", "https://mcp.example.test/mcp"},
		{"https://mcp.example.test/", "", "", "https://mcp.example.test/"},
		{"https://mcp.example.test", "", "", "https://mcp.example.test"},
		{"https://mcp.example.test/mcp", "sse", "", "https://mcp.example.test/mcp/sse"},
		{"https://mcp.example.test/mcp", "", "session=1", "https://mcp.example.test/mcp?session=1"},
		{"https://mcp.example.test/mcp?team=a", "", "session=1", "https://mcp.example.test/mcp?team=a&session=1"},
		{"https://mcp.example.test/mcp?team=a", "", "", "https://mcp.example.test/mcp?team=a"},
	} {
		target, err := url.Parse(tc.target)
		if err != nil {
			t.Fatal(err)
		}
		if got := brokeredURL(target, tc.rest, tc.query).String(); got != tc.want {
			t.Errorf("brokeredURL(%q, %q, %q) = %q, want %q", tc.target, tc.rest, tc.query, got, tc.want)
		}
	}
}
