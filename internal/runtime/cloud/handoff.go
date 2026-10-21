package cloud

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"regexp"
	"slices"
	"strings"
)

// A cloud loop's env-var connections reach its session as the session
// starts (ADR-0059): the hub answers the loop home's SessionStart post with
// them as `export` lines, which the hook writes to CLAUDE_ENV_FILE, so the
// session's shell commands see them as a contained loop's do. The answer
// comes only to the post a start is waiting on.

// sourceStartup is the one SessionStart source the hub answers, a
// session's first start. Run I on #976 found the env file kept through a
// compaction and on a VM Anthropic reclaimed, so the loop home's matcher
// posts nothing else.
const sourceStartup = "startup"

// envName is ADR-0043's rule for an env-var connection's variable, checked
// again at hand-off: the name is written into a line the VM's shell reads.
var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// exportLines is the hand-off of env: one `export NAME='value'` line per
// variable, by name, each value single-quoted as shellQuote quotes it.
// A variable whose name breaks ADR-0043's rule, or whose value holds a NUL,
// is left out and logged by name.
func exportLines(env map[string]string) []byte {
	names := make([]string, 0, len(env))
	for name := range env {
		names = append(names, name)
	}
	slices.Sort(names)
	var lines bytes.Buffer
	for _, name := range names {
		value := env[name]
		switch {
		case !envName.MatchString(name):
			slog.Default().Warn("cloud: a connection's variable name can't be handed to a session; left out", "name", name)
			continue
		case strings.ContainsRune(value, 0):
			slog.Default().Warn("cloud: a connection's value holds a NUL and can't be handed to a session; left out", "name", name)
			continue
		}
		lines.WriteString("export " + name + "=" + shellQuote(value) + "\n")
	}
	return lines.Bytes()
}

// hookSource is a SessionStart hook's source, "" when its input has none.
func hookSource(hook []byte) string {
	var input struct {
		Source string `json:"source"`
	}
	_ = json.Unmarshal(hook, &input)
	return input.Source
}

// errNoHandOff refuses a SessionStart post nothing is waiting for.
var errNoHandOff = errors.New("cloud: no start of this loop is pending")
