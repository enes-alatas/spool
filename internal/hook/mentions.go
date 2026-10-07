package hook

import (
	"io"
	"os"
	"path/filepath"
	"strings"
)

// The mention rule (#628): the fleet's loops share one GitHub identity and
// their names, like the people's chat handles, are not theirs on GitHub, so
// an @-mention of one in a gh body pings whoever holds that GitHub account.
// The hook refuses a body that mentions one of the names the hub hands it.

// ghBodied are the gh commands whose body GitHub renders, mentions and all.
var ghBodied = map[string]map[string]bool{
	"issue": {"create": true, "comment": true, "edit": true},
	"pr":    {"create": true, "comment": true, "edit": true, "review": true},
}

// ghValued are gh's own options that take the next word as a value.
var ghValued = map[string]bool{"-R": true, "--repo": true, "--hostname": true}

// maxBodyFile is as much of a body file as the rule reads.
const maxBodyFile = 1 << 20

// checkMentions refuses a gh command whose body @-mentions one of names.
// A body given on the command line is read as given. One read from a file
// or from stdin is read from the file, when it is there yet, and from the
// whole script besides: the script may write the file first, or feed stdin
// from a here-document or a pipe. So is one given as a shell variable or
// a command substitution: the script may set the variable, and a
// substitution may read any file the script names.
func checkMentions(words []string, script, cwd string, names []string) string {
	if len(names) == 0 || program(words[0]) != "gh" {
		return ""
	}
	texts := ghBodies(words[1:], script, cwd)
	handles := mentionable(names)
	for _, text := range texts {
		if handle := mentioned(text, handles); handle != "" {
			return "this gh body @-mentions " + handle + ", which on GitHub pings whoever owns that account there, not the loop or person here; write the name without the @."
		}
	}
	return ""
}

// ghBodies returns the texts a gh invocation sends as a body, or none for
// a command that sends no body.
func ghBodies(args []string, script, cwd string) []string {
	var positional []string
	i := 0
	for ; i < len(args) && len(positional) < 2; i++ {
		switch {
		case ghValued[args[i]]:
			i++
		case strings.HasPrefix(args[i], "-"):
		case len(positional) == 0 && args[i] == "api":
			// gh api takes an endpoint, not a verb, and its fields after it
			return apiBodies(args[i+1:], script, cwd)
		default:
			positional = append(positional, args[i])
		}
	}
	if len(positional) < 2 || !ghBodied[positional[0]][positional[1]] {
		return nil
	}
	rest := args[i:]
	var texts []string
	given := func(value string) {
		texts = append(texts, bodyGiven(value, script, cwd)...)
	}
	from := func(source string) {
		texts = append(texts, bodyFrom(source, script, cwd)...)
	}
	for i := 0; i < len(rest); i++ {
		arg := rest[i]
		switch {
		case arg == "--body" || arg == "-b":
			if i+1 < len(rest) {
				i++
				given(rest[i])
			}
		case strings.HasPrefix(arg, "--body="):
			given(strings.TrimPrefix(arg, "--body="))
		case strings.HasPrefix(arg, "-b") && !strings.HasPrefix(arg, "--"):
			given(strings.TrimPrefix(strings.TrimPrefix(arg, "-b"), "="))
		case arg == "--body-file" || arg == "-F":
			if i+1 < len(rest) {
				i++
				from(rest[i])
			}
		case strings.HasPrefix(arg, "--body-file="):
			from(strings.TrimPrefix(arg, "--body-file="))
		case strings.HasPrefix(arg, "-F") && !strings.HasPrefix(arg, "--"):
			from(strings.TrimPrefix(strings.TrimPrefix(arg, "-F"), "="))
		}
	}
	return texts
}

// apiBodies returns the field values and input a gh api call sends: any of
// them may be a body, a comment's or a GraphQL mutation's.
func apiBodies(args []string, script, cwd string) []string {
	var texts []string
	field := func(value string, typed bool) {
		_, value, _ = strings.Cut(value, "=")
		if typed && strings.HasPrefix(value, "@") {
			texts = append(texts, bodyFrom(strings.TrimPrefix(value, "@"), script, cwd)...)
			return
		}
		texts = append(texts, bodyGiven(value, script, cwd)...)
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-f" || arg == "--raw-field" || arg == "-F" || arg == "--field":
			if i+1 < len(args) {
				i++
				field(args[i], arg == "-F" || arg == "--field")
			}
		case strings.HasPrefix(arg, "--raw-field="):
			field(strings.TrimPrefix(arg, "--raw-field="), false)
		case strings.HasPrefix(arg, "--field="):
			field(strings.TrimPrefix(arg, "--field="), true)
		case strings.HasPrefix(arg, "-f") && !strings.HasPrefix(arg, "--"):
			field(strings.TrimPrefix(arg, "-f"), false)
		case strings.HasPrefix(arg, "-F") && !strings.HasPrefix(arg, "--"):
			field(strings.TrimPrefix(arg, "-F"), true)
		case arg == "--input":
			if i+1 < len(args) {
				i++
				texts = append(texts, bodyFrom(args[i], script, cwd)...)
			}
		case strings.HasPrefix(arg, "--input="):
			texts = append(texts, bodyFrom(strings.TrimPrefix(arg, "--input="), script, cwd)...)
		}
	}
	return texts
}

// bodyGiven returns what a body given on the command line may hold: the
// value as written and, when it still holds an expansion the shell makes,
// the whole script, which may set the variable or hold the substituted
// command's text. A command substitution may also read a file already
// there, so it adds every file a word of the script names.
func bodyGiven(value, script, cwd string) []string {
	texts := []string{value}
	if !strings.ContainsAny(value, "$`") {
		return texts
	}
	texts = append(texts, script)
	if !strings.Contains(value, "$(") && !strings.Contains(value, "`") {
		return texts
	}
	read := map[string]bool{}
	for _, words := range commands(script) {
		for _, word := range words {
			// $(<file) reads the file without a command
			path := strings.TrimPrefix(word, "<")
			if path == "" || read[path] {
				continue
			}
			read[path] = true
			if data, ok := readBody(path, cwd); ok {
				texts = append(texts, data)
			}
		}
	}
	return texts
}

// bodyFrom returns what a body read from source may hold: "-" is stdin,
// anything else a file. Either way the script is one of them.
func bodyFrom(source, script, cwd string) []string {
	texts := []string{script}
	if source == "-" || source == "" {
		return texts
	}
	if data, ok := readBody(source, cwd); ok {
		texts = append(texts, data)
	}
	return texts
}

// readBody returns the start of the regular file at path, relative to cwd,
// and whether there is one to read.
func readBody(path, cwd string) (string, bool) {
	if !filepath.IsAbs(path) && cwd != "" {
		path = filepath.Join(cwd, path)
	}
	file, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer func() { _ = file.Close() }()
	if info, err := file.Stat(); err != nil || !info.Mode().IsRegular() {
		return "", false
	}
	data, err := io.ReadAll(io.LimitReader(file, maxBodyFile))
	if err != nil {
		return "", false
	}
	return string(data), true
}

// mentionable are the handles GitHub would read in an @-mention of each
// name: lowercased, and cut where a GitHub login can't go on, so a bot's
// terra_spool_bot is the terra it would ping.
func mentionable(names []string) map[string]bool {
	handles := map[string]bool{}
	for _, name := range names {
		if handle := strings.ToLower(name[:handleLength(name)]); handle != "" {
			handles[handle] = true
		}
	}
	return handles
}

// handleLength is how many bytes of s a GitHub login can take: letters,
// digits and hyphens.
func handleLength(s string) int {
	for i, r := range s {
		if !loginRune(r) {
			return i
		}
	}
	return len(s)
}

// loginRune reports whether r can be part of a GitHub login.
func loginRune(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-'
}

// mentioned returns the first handle text @-mentions, or "". A mention
// follows the start of the text or a character that can't end a word, so
// an email address is none. Code spans and fenced blocks are skipped:
// GitHub renders a mention in them as text.
func mentioned(text string, handles map[string]bool) string {
	text = withoutCode(text)
	for i := 0; i < len(text); i++ {
		if text[i] != '@' {
			continue
		}
		if i > 0 {
			prev := text[i-1]
			if prev == '_' || prev >= 'a' && prev <= 'z' || prev >= 'A' && prev <= 'Z' || prev >= '0' && prev <= '9' {
				continue
			}
		}
		rest := text[i+1:]
		handle := strings.ToLower(rest[:handleLength(rest)])
		if handles[handle] {
			return handle
		}
	}
	return ""
}

// withoutCode blanks out text's code spans and fenced blocks, keeping the
// rest where it was.
func withoutCode(text string) string {
	var out strings.Builder
	for len(text) > 0 {
		start := strings.IndexByte(text, '`')
		if start < 0 {
			out.WriteString(text)
			break
		}
		out.WriteString(text[:start])
		run := len(text[start:]) - len(strings.TrimLeft(text[start:], "`"))
		fence := text[start : start+run]
		end := strings.Index(text[start+run:], fence)
		if end < 0 {
			// an unclosed span is no span: the rest is text
			out.WriteString(text[start:])
			break
		}
		out.WriteByte(' ')
		text = text[start+run+end+run:]
	}
	return out.String()
}
