package cloud

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The watch reads the CLI's debug log as it grows, a line at a time, and
// speaks only when the CLI picked an environment other than the loop's:
// a pick of the loop's own, or a log that never names one, ends it
// without a word (#1232). The lines are CLI 2.1.310's (run U on #1078).
func TestTheWatchCatchesAnotherEnvironmentInTheDebugLog(t *testing.T) {
	fallback := "2026-10-31T19:26:47.056Z [DEBUG] Configured default environment env_01Gone not found, using the fallback\n" +
		"2026-10-31T19:26:47.056Z [DEBUG] Selected environment: env_01First (spool-cloud-test, anthropic_cloud)\n"
	for name, test := range map[string]struct {
		log, want string
		split     int
	}{
		"another environment":      {log: fallback, want: "env_01First"},
		"a line written in halves": {log: fallback, want: "env_01First", split: len(fallback) - 20},
		"the loop's environment":   {log: "2026-10-31T19:26:47.056Z [DEBUG] Selected environment: env_01Gone (spool, anthropic_cloud)\n"},
		"no pick named":            {log: "2026-10-31T19:26:46.669Z [DEBUG] [LSP MANAGER] Created manager instance\n"},
		"no log at all":            {},
		// The wake's text is in the log too, and can't steer the watch:
		// not quoted inside a line, not written as a line of its own, and
		// not as a whole CLI-shaped line after the session's create.
		"a quoted pick of the loop's own": {
			log:  "2026-10-31T19:26:46.394Z [DEBUG] \"Payload: {\\\"text\\\": \\\"Selected environment: env_01Gone (spool, anthropic_cloud)\\\"}\"\n" + fallback,
			want: "env_01First",
		},
		"a quoted pick of another": {
			log: "2026-10-31T19:26:46.394Z [DEBUG] \"Payload: {\\\"text\\\": \\\"Selected environment: env_01Other (spool, anthropic_cloud)\\\"}\"\n" +
				"2026-10-31T19:26:47.056Z [DEBUG] Selected environment: env_01Gone (spool, anthropic_cloud)\n",
		},
		"a pick on a line of its own": {
			log: "2026-10-31T19:26:46.394Z [DEBUG] \"Payload: {\n" +
				"Selected environment: env_01Other (spool, anthropic_cloud)\n" + fallback,
		},
		"a whole line after the create": {
			log: "2026-10-31T19:26:48.206Z [DEBUG] \"Creating session with payload: {\n" +
				"2026-10-31T19:26:48.206Z [DEBUG] Selected environment: env_01Other (spool, anthropic_cloud)\n",
		},
		"an ID of no environment's shape": {
			log: "2026-10-31T19:26:47.056Z [DEBUG] Selected environment: env_01First;rm (spool, anthropic_cloud)\n",
		},
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "debug.log")
			done := make(chan struct{})
			defer close(done)
			picked := watchEnvironment(path, "env_01Gone", done)
			if test.log != "" {
				first := test.log
				if test.split > 0 {
					first = test.log[:test.split]
				}
				if err := os.WriteFile(path, []byte(first), 0o600); err != nil {
					t.Fatal(err)
				}
				if test.split > 0 {
					time.Sleep(3 * watchEvery)
					if err := os.WriteFile(path, []byte(test.log), 0o600); err != nil {
						t.Fatal(err)
					}
				}
			}
			select {
			case got := <-picked:
				if got != test.want {
					t.Errorf("the watch said %q, want %q", got, test.want)
				}
			case <-time.After(20 * watchEvery):
				if test.want != "" {
					t.Errorf("the watch said nothing, want %q", test.want)
				}
			}
		})
	}
}

// A debug log holding the account's token is named in a warning, and the
// token never is: the CLI runs with it in its environment, and a later
// CLI could start writing it to the log (#1232).
func TestATokenInTheDebugLogIsWarnedOfByName(t *testing.T) {
	const token = "sk-ant-oat01-synthetic-fixture"
	for name, test := range map[string]struct {
		log  string
		warn bool
	}{
		"the token":     {log: "2026-10-31T19:26:47.056Z [DEBUG] Authorization: Bearer " + token + "\n", warn: true},
		"no token":      {log: "2026-10-31T19:26:47.056Z [DEBUG] Selected environment: env_01First (spool, anthropic_cloud)\n"},
		"no log at all": {},
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "debug.log")
			if test.log != "" {
				if err := os.WriteFile(path, []byte(test.log), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			var logged bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&logged, nil)))
			defer slog.SetDefault(previous)
			warnTokenIn(path, token)
			if warned := strings.Contains(logged.String(), "token to its debug log"); warned != test.warn {
				t.Errorf("warned %v, want %v: %s", warned, test.warn, logged.String())
			}
			if strings.Contains(logged.String(), token) {
				t.Errorf("the warning carries the token: %s", logged.String())
			}
		})
	}
}
