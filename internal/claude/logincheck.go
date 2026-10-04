package claude

import (
	"context"
	"errors"
	"fmt"
)

// A login check is the one CLI run that uses the operator's login outside a
// loop's turns (ADR-0044): a single haiku answer to a one-word prompt, run
// when a setup-token is saved and when the operator asks, to learn whether
// the API accepts the credential before any loop depends on it. It is not an
// auxiliary run in ADR-0033's sense, which holds no credential.

// LoginCheckModel is the model a check asks: the cheapest, since only the
// API's acceptance of the credential matters, never the answer.
const LoginCheckModel = "haiku"

// loginCheckPrompt is the whole of what a check sends.
const loginCheckPrompt = "hi"

// LoginCheckArgs is a check's argument list: one print-mode turn on haiku
// that can use no tool and no MCP server and leaves no session behind. No
// permission mode is given, so a tool the model asked for anyway would be
// refused rather than run.
func LoginCheckArgs() []string {
	return []string{
		"-p", "--verbose",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--model", LoginCheckModel,
		"--tools", "",
		"--strict-mcp-config",
		"--no-session-persistence",
	}
}

// LoginCheck is what a check found: the API accepted the login, or refused
// it with the sentence the CLI gave the operator.
type LoginCheck struct {
	OK      bool
	Refusal string
}

// ErrLoginCheckInconclusive is a check that ended without saying either way:
// the run failed for a reason other than the login, so it proves nothing.
var ErrLoginCheckInconclusive = errors.New("claude: login check inconclusive")

// CheckLogin sends a check's prompt and reads the run to its result. A
// refusal is the CLI's own authentication_failed message (IsLoginRejected);
// a result without an error is a login the API accepted. Any other end, a
// result in error or the run exiting first, is inconclusive.
func CheckLogin(ctx context.Context, stream *Stream) (LoginCheck, error) {
	if err := stream.Send(loginCheckPrompt); err != nil {
		return LoginCheck{}, fmt.Errorf("claude: login check: %w", err)
	}
	refusal := ""
	for {
		select {
		case <-ctx.Done():
			return LoginCheck{}, fmt.Errorf("%w: %w", ErrLoginCheckInconclusive, ctx.Err())
		case ev, ok := <-stream.Events():
			switch {
			case !ok:
				return LoginCheck{}, fmt.Errorf("%w: exited without a result", ErrLoginCheckInconclusive)
			case ev.Assistant != nil && IsLoginRejected(ev.Assistant):
				refusal = ev.Assistant.Text
			case ev.Result == nil:
			case refusal != "":
				return LoginCheck{Refusal: refusal}, nil
			case !ev.Result.IsError:
				return LoginCheck{OK: true}, nil
			default:
				return LoginCheck{}, fmt.Errorf("%w: the run ended in error", ErrLoginCheckInconclusive)
			}
		}
	}
}
