package httpapi

import (
	"strings"
	"testing"
)

func TestValidateClaudeToken(t *testing.T) {
	valid := "sk-ant-oat01-" + strings.Repeat("A0", 30) // sk-ant-, long, no spaces

	cases := []struct {
		name    string
		in      string
		want    string // returned token when no error
		wantErr bool
	}{
		{"well-formed", valid, valid, false},
		{"surrounding whitespace trimmed", "  " + valid + "\n", valid, false},
		{"empty", "", "", true},
		{"whitespace only", "   \t\n", "", true},
		{"internal space", "sk-ant-oat01-aaaa aaaabbbbbbbbbbbb", "", true},
		{"internal newline", "sk-ant-oat01-aaaa\naaaabbbbbbbbbbbb", "", true},
		{"too short", "sk-ant-oat", "", true},
		{"wrong prefix", "nope-" + strings.Repeat("x", 40), "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := validateClaudeToken(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want error, got token %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("token = %q, want %q", got, tc.want)
			}
		})
	}
}
