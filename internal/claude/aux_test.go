package claude

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The preflight's --version is an auxiliary run too (ADR-0033): the hub's
// credentials do not reach it.
func TestPreflightHoldsNoCredential(t *testing.T) {
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "fixture-oauth-token")
	t.Setenv("ANTHROPIC_API_KEY", "fixture-api-key")
	stub := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\nenv\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := Preflight(context.Background(), stub)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "fixture") {
		t.Fatalf("a hub credential reached --version:\n%s", out)
	}
	if strings.Contains(out, "ANTHROPIC_BASE_URL") {
		t.Fatalf("--version was told of an API it has no use for:\n%s", out)
	}
}
