package surface

import (
	"errors"
	"fmt"
	"testing"
)

// A refusal says which token to fix, through any wrapping; an error that is
// not a refusal says nothing about the credential (#230).
func TestRejectedPart(t *testing.T) {
	reason := errors.New("invalid_auth")
	refused := fmt.Errorf("validating: %w", &RejectedError{Part: PartAppToken, Err: reason})
	if got := RejectedPart(refused); got != PartAppToken {
		t.Errorf("RejectedPart(wrapped refusal) = %q, want %q", got, PartAppToken)
	}
	if !errors.Is(refused, reason) {
		t.Error("a refusal does not unwrap to the platform's reason")
	}
	if got := refused.Error(); got != "validating: invalid_auth" {
		t.Errorf("message = %q: the part is for the caller to key on, not to print", got)
	}
	if got := RejectedPart(errors.New("dial tcp: connection refused")); got != "" {
		t.Errorf("RejectedPart(unreachable) = %q, want \"\"", got)
	}
}
