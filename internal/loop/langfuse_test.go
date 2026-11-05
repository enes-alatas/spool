package loop

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestLangfuseAuthorization(t *testing.T) {
	got := LangfuseAuthorization("pk-lf-a:sk-lf-b")
	encoded, found := strings.CutPrefix(got, "Basic ")
	if !found {
		t.Fatalf("authorization = %q, want Basic", got)
	}
	if decoded, err := base64.StdEncoding.DecodeString(encoded); err != nil || string(decoded) != "pk-lf-a:sk-lf-b" {
		t.Errorf("authorization decodes to %q (%v)", decoded, err)
	}
	if LangfuseAuthorization("") != "" {
		t.Error("no key must build no header")
	}
}

func TestValidLangfuseKey(t *testing.T) {
	for key, valid := range map[string]bool{
		"pk-lf-a:sk-lf-b":   true,
		"pk-lf-a":           false,
		":sk-lf-b":          false,
		"pk-lf-a:":          false,
		"pk-lf-a:sk:lf":     false,
		"pk-lf-a:sk-lf b":   false,
		"pk-lf-a:sk-lf\r\n": false,
	} {
		if err := ValidLangfuseKey(key); (err == nil) != valid {
			t.Errorf("ValidLangfuseKey(%q) = %v, want valid %v", key, err, valid)
		}
	}
}

func TestValidLangfuseReadAddress(t *testing.T) {
	for address, valid := range map[string]bool{
		"https://langfuse.example":       true,
		"http://127.0.0.1:3000":          true,
		"http://localhost:3000":          true,
		"http://[::1]:3000":              true,
		"http://langfuse.example":        false,
		"http://langfuse-web":            false,
		"http://0.0.0.0:3000":            false,
		"https://u:p@langfuse.example":   false,
		"https://langfuse.example/?a=b":  false,
		"https://langfuse.example/#frag": false,
		"langfuse.example":               false,
	} {
		if err := ValidLangfuseReadAddress(address); (err == nil) != valid {
			t.Errorf("ValidLangfuseReadAddress(%q) = %v, want valid %v", address, err, valid)
		}
	}
}
