package hook

import (
	"path/filepath"
	"strings"
)

// commands splits a shell script into its simple commands, each as the
// words a shell would hand the program: quotes removed, separators (; & |
// newlines, subshell parentheses, command substitutions, inside double
// quotes too) between commands, comments and here-document bodies dropped.
// It is a reader for the rules, not a shell: expansions are left as
// written, and a script that hides a command inside a variable or a file it
// runs is not seen. The hook catches slips, not adversaries (ADR-0042).
func commands(script string) [][]string {
	s := &scanner{runes: []rune(script)}
	var unwrapped [][]string
	for _, command := range s.scan(false) {
		unwrapped = append(unwrapped, unwrap(command)...)
	}
	return unwrapped
}

// scanner reads a script one rune at a time; i is the rune being read.
type scanner struct {
	runes []rune
	i     int
}

// heredoc is a here-document whose body starts at the next line.
type heredoc struct {
	delimiter string
	stripTabs bool // <<- strips leading tabs from the body and delimiter
}

// scan reads simple commands up to the end of the script or, in a command
// substitution, up to the parenthesis that closes it, where it stops.
func (s *scanner) scan(substitution bool) [][]string {
	var all [][]string
	var words []string
	var word strings.Builder
	var pending []heredoc
	inWord, depth := false, 0
	endWord := func() {
		if inWord {
			words = append(words, word.String())
			word.Reset()
			inWord = false
		}
	}
	endCommand := func() {
		endWord()
		if len(words) > 0 {
			all = append(all, words)
			words = nil
		}
	}
	runes := s.runes
	for ; s.i < len(runes); s.i++ {
		r := runes[s.i]
		switch {
		case r == '\'':
			inWord = true
			for s.i++; s.i < len(runes) && runes[s.i] != '\''; s.i++ {
				word.WriteRune(runes[s.i])
			}
		case r == '"':
			inWord = true
			for s.i++; s.i < len(runes) && runes[s.i] != '"'; s.i++ {
				switch {
				case runes[s.i] == '\\' && s.i+1 < len(runes) && strings.ContainsRune("\"\\$`", runes[s.i+1]):
					s.i++
					word.WriteRune(runes[s.i])
				case runes[s.i] == '$' && s.i+1 < len(runes) && runes[s.i+1] == '(' ||
					runes[s.i] == '`':
					// The substitution's text stays in the word as written;
					// its commands run all the same.
					from := s.i
					all = append(all, s.substitution()...)
					word.WriteString(string(runes[from:min(s.i+1, len(runes))]))
				default:
					word.WriteRune(runes[s.i])
				}
			}
		case r == '\\' && s.i+1 < len(runes):
			s.i++
			if runes[s.i] != '\n' {
				inWord = true
				word.WriteRune(runes[s.i])
			}
		case r == '#' && !inWord:
			for s.i+1 < len(runes) && runes[s.i+1] != '\n' {
				s.i++
			}
			endCommand()
		case r == '<' && strings.HasPrefix(string(runes[s.i:min(s.i+3, len(runes))]), "<<<"):
			s.i += 2 // a here-string: its word follows as an argument
			endWord()
		case r == '<' && s.i+1 < len(runes) && runes[s.i+1] == '<':
			endWord()
			pending = append(pending, s.heredocDelimiter())
		case r == '$' && s.i+1 < len(runes) && runes[s.i+1] == '(' || r == '`':
			endCommand()
			all = append(all, s.substitution()...)
		case r == '(':
			depth++
			endCommand()
		case r == ')' && substitution && depth == 0:
			endCommand()
			return all
		case r == ')':
			depth--
			endCommand()
		case r == '\n':
			endCommand()
			for _, doc := range pending {
				s.skipBody(doc)
			}
			pending = nil
		case strings.ContainsRune(";&|", r):
			endCommand()
		case r == ' ' || r == '\t':
			endWord()
		default:
			inWord = true
			word.WriteRune(r)
		}
	}
	endCommand()
	return all
}

// substitution reads the command substitution starting at s.i, $( or a
// backquote, and returns its commands, leaving s.i on its last rune.
func (s *scanner) substitution() [][]string {
	if s.runes[s.i] == '$' {
		s.i += 2
		return s.scan(true)
	}
	start := s.i + 1
	for s.i++; s.i < len(s.runes) && s.runes[s.i] != '`'; s.i++ {
		if s.runes[s.i] == '\\' {
			s.i++
		}
	}
	return (&scanner{runes: s.runes[start:min(s.i, len(s.runes))]}).scan(false)
}

// heredocDelimiter reads the redirection starting at s.i, << or <<-, and
// its delimiter word, quotes removed, leaving s.i on the word's last rune.
func (s *scanner) heredocDelimiter() heredoc {
	var doc heredoc
	s.i += 2
	if s.i < len(s.runes) && s.runes[s.i] == '-' {
		doc.stripTabs = true
		s.i++
	}
	for s.i < len(s.runes) && (s.runes[s.i] == ' ' || s.runes[s.i] == '\t') {
		s.i++
	}
	var delimiter strings.Builder
	for ; s.i < len(s.runes) && !strings.ContainsRune(" \t\n;&|()<>", s.runes[s.i]); s.i++ {
		switch r := s.runes[s.i]; r {
		case '\'', '"':
			for s.i++; s.i < len(s.runes) && s.runes[s.i] != r; s.i++ {
				delimiter.WriteRune(s.runes[s.i])
			}
		case '\\':
			if s.i+1 < len(s.runes) {
				s.i++
				delimiter.WriteRune(s.runes[s.i])
			}
		default:
			delimiter.WriteRune(r)
		}
	}
	s.i--
	doc.delimiter = delimiter.String()
	return doc
}

// skipBody moves s.i from the newline that ends a here-document's command
// line to the newline that ends its delimiter line, or the end of the
// script: a body is text, not commands.
func (s *scanner) skipBody(doc heredoc) {
	for s.i < len(s.runes) {
		start := s.i + 1
		end := start
		for end < len(s.runes) && s.runes[end] != '\n' {
			end++
		}
		s.i = end
		line := string(s.runes[min(start, end):end])
		if doc.stripTabs {
			line = strings.TrimLeft(line, "\t")
		}
		if line == doc.delimiter {
			return
		}
	}
}

// wrappers are the commands that run another command named in their
// arguments, with the options of each that take a value of their own.
var wrappers = map[string]map[string]bool{
	"sudo":    {"-u": true, "-g": true, "-C": true, "-D": true, "-h": true, "-p": true, "-r": true, "-t": true, "-U": true},
	"env":     {"-u": true, "-C": true, "-S": true},
	"timeout": {"-s": true, "-k": true},
	"nice":    {"-n": true},
	"ionice":  {"-c": true, "-n": true, "-p": true},
	"stdbuf":  {"-i": true, "-o": true, "-e": true},
	"xargs":   {"-a": true, "-d": true, "-E": true, "-I": true, "-L": true, "-n": true, "-P": true, "-s": true},
	"nohup":   {},
	"setsid":  {},
	"time":    {},
	"command": {},
	"exec":    {},
	"builtin": {},
}

// keywords are the shell's own words that can stand ahead of a command.
var keywords = map[string]bool{"{": true, "}": true, "!": true, "if": true, "then": true, "elif": true,
	"else": true, "while": true, "until": true, "do": true}

// shells are the programs whose -c argument is a script of its own.
var shells = map[string]bool{"sh": true, "bash": true, "dash": true, "zsh": true, "ksh": true}

// unwrap returns the command a simple command really runs: past leading
// variable assignments and wrappers such as sudo or timeout, and, for a
// shell's -c, the commands of the script it is handed.
func unwrap(words []string) [][]string {
	for len(words) > 0 && (isAssignment(words[0]) || keywords[words[0]]) {
		words = words[1:]
	}
	if len(words) == 0 {
		return nil
	}
	name := program(words[0])
	if valued, ok := wrappers[name]; ok {
		rest := words[1:]
		for len(rest) > 0 && (strings.HasPrefix(rest[0], "-") || (name == "env" && isAssignment(rest[0]))) {
			if name == "command" && rest[0] != "--" && strings.ContainsAny(rest[0], "vV") {
				return [][]string{words} // it says how a name resolves; it runs nothing
			}
			if rest[0] == "--" {
				rest = rest[1:]
				break
			}
			if valued[rest[0]] && len(rest) > 1 {
				rest = rest[1:]
			}
			rest = rest[1:]
		}
		if name == "timeout" && len(rest) > 0 {
			rest = rest[1:] // the duration
		}
		return unwrap(rest)
	}
	if shells[name] {
		for i, word := range words[1:] {
			if strings.HasPrefix(word, "-") && !strings.HasPrefix(word, "--") && strings.Contains(word, "c") {
				if i+2 < len(words) {
					return commands(words[i+2])
				}
				return nil
			}
		}
	}
	return [][]string{words}
}

// program is a command's name without the directory it was run from.
func program(word string) string { return filepath.Base(word) }

// isAssignment reports whether word is a NAME=value prefix of a command.
func isAssignment(word string) bool {
	name, _, found := strings.Cut(word, "=")
	if !found || name == "" {
		return false
	}
	for i, r := range name {
		letter := r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z'
		digit := i > 0 && r >= '0' && r <= '9'
		if !letter && !digit {
			return false
		}
	}
	return true
}
