package eval

import (
	"regexp"
	"strings"
)

// Artifact is one thing a GitHub write publishes: a commit message, or an
// issue, PR, comment or review's title and body. Graders read these, not
// the command around them — the command also holds paths, scripts and the
// next command's body, and a grader reading those grades the wrong thing.
type Artifact struct {
	// Kind is "commit", or the gh noun and verb: "pr create",
	// "issue comment".
	Kind string
	// Text is every title, body and message the write gives inline or by a
	// file the turn wrote. Empty when the grader cannot see it.
	Text string
	// Authored marks a write that carries a new signed text: a commit, a
	// create, a comment or a review — not an edit, a close or an amend
	// that keeps its message.
	Authored bool
}

// writeStart finds where each write in a command begins.
var writeStart = regexp.MustCompile(`\bgit\s+(?:-C\s+\S+\s+)?commit\b|\bgh\s+(issue|pr)\s+(create|comment|edit|review|close)\b`)

// textFlag is a flag whose value is published text. (gh pr review's
// --comment is not one: it is the review's kind, and its text is --body.)
var textFlag = regexp.MustCompile(`(?:^|\s)(-m|--message|-b|--body|-t|--title)(?:\s+|=)`)

// fileFlag is a flag whose value names a file of published text.
var fileFlag = regexp.MustCompile(`(?:^|\s)(-F|--file|--body-file)(?:\s+|=)(\S+)`)

// Artifacts splits a write command into what it publishes.
func Artifacts(write GitHubWrite) []Artifact {
	command := write.Command
	starts := writeStart.FindAllStringSubmatchIndex(command, -1)
	var out []Artifact
	for i, start := range starts {
		end := len(command)
		if i+1 < len(starts) {
			end = starts[i+1][0]
		}
		span := command[start[1]:end]
		artifact := Artifact{Kind: "commit"}
		if start[2] >= 0 {
			artifact.Kind = command[start[2]:start[3]] + " " + command[start[4]:start[5]]
		}
		switch {
		case artifact.Kind == "commit":
			artifact.Authored = !strings.Contains(span, "--no-edit")
		case strings.HasSuffix(artifact.Kind, "edit"), strings.HasSuffix(artifact.Kind, "close"):
		default:
			artifact.Authored = true
		}

		var texts []string
		for _, flag := range textFlag.FindAllStringIndex(span, -1) {
			if value, ok := shellWord(span[flag[1]:]); ok {
				texts = append(texts, value)
			}
		}
		for _, flag := range fileFlag.FindAllStringSubmatch(span, -1) {
			path := strings.Trim(flag[2], `"'`)
			switch body, written := write.Files[path]; {
			case path == "-":
				texts = append(texts, heredoc(span))
			case written:
				texts = append(texts, body)
			default:
				// A file the same command filled first — cat > $f <<EOF,
				// then --body-file $f — is the last heredoc before the
				// write.
				texts = append(texts, lastHeredoc(command[:start[0]]))
			}
		}
		artifact.Text = strings.Join(texts, "\n")
		out = append(out, artifact)
	}
	return out
}

// shellWord reads one shell word — "…", '…', or bare — from the start of
// s. A "$(cat <<'EOF' … EOF)" word yields the heredoc's body, which is how
// a multi-line message is usually passed.
func shellWord(s string) (string, bool) {
	if s == "" {
		return "", false
	}
	switch s[0] {
	case '\'':
		end := strings.IndexByte(s[1:], '\'')
		if end < 0 {
			return "", false
		}
		return s[1 : 1+end], true
	case '"':
		if strings.HasPrefix(s, `"$(cat <<`) {
			return heredoc(s), true
		}
		var word strings.Builder
		for i := 1; i < len(s); i++ {
			switch s[i] {
			case '\\':
				// Inside double quotes a backslash escapes only these.
				if i+1 < len(s) && strings.IndexByte("$`\"\\\n", s[i+1]) >= 0 {
					i++
				}
				word.WriteByte(s[i])
			case '"':
				return word.String(), true
			default:
				word.WriteByte(s[i])
			}
		}
		return "", false
	default:
		end := strings.IndexAny(s, " \t\n;&|")
		if end < 0 {
			end = len(s)
		}
		return s[:end], true
	}
}

var heredocOpen = regexp.MustCompile(`<<-?\s*['"]?(\w+)['"]?[^\n]*\n`)

// heredoc returns the body of the first heredoc in s.
func heredoc(s string) string {
	open := heredocOpen.FindStringSubmatchIndex(s)
	if open == nil {
		return ""
	}
	return heredocAt(s, open)
}

// lastHeredoc returns the body of the last heredoc in s.
func lastHeredoc(s string) string {
	opens := heredocOpen.FindAllStringSubmatchIndex(s, -1)
	if len(opens) == 0 {
		return ""
	}
	return heredocAt(s, opens[len(opens)-1])
}

func heredocAt(s string, open []int) string {
	delimiter := s[open[2]:open[3]]
	var body []string
	for _, line := range strings.Split(s[open[1]:], "\n") {
		if strings.TrimSpace(line) == delimiter {
			break
		}
		body = append(body, line)
	}
	return strings.Join(body, "\n")
}
