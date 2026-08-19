package httpapi

import (
	"strings"
	"testing"
)

func TestValidateSecretName(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		wantErr bool
	}{
		{"simple upper", "GH_TOKEN", false},
		{"leading underscore", "_HIDDEN", false},
		{"digits after letter", "KEY2", false},
		{"lowercase allowed", "api_key", false},
		{"empty", "", true},
		{"leading digit", "1KEY", true},
		{"dash", "GH-TOKEN", true},
		{"dot", "gh.token", true},
		{"space", "GH TOKEN", true},
		{"too long", strings.Repeat("A", maxSecretNameLen+1), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateSecretName(tc.in)
			if tc.wantErr && err == nil {
				t.Fatalf("name %q: want error, got nil", tc.in)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("name %q: unexpected error: %v", tc.in, err)
			}
		})
	}
}
