package hook

import (
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// credentialShape is one of the shapes scripts/secret-rules.awk knows a
// credential by. The pattern is written for lowercase text, as the awk
// one is, and matched against a folded copy.
type credentialShape struct {
	name    string
	pattern *regexp.Regexp
}

// credentialShapes are the credential shapes of scripts/secret-rules.awk,
// copied pattern for pattern. That file's fleet identifiers are left out:
// they are not credentials, and its own scan reads them only in bodies.
// TestCredentialShapesMatchTheSecretRules keeps the two lists one list.
var credentialShapes = []credentialShape{
	{"telegram-bot-token", regexp.MustCompile(`[0-9]{8,}:[a-z0-9_-]{30,}`)},
	{"anthropic-key", regexp.MustCompile(`sk-ant-[a-z0-9_-]{16,}`)},
	{"github-token", regexp.MustCompile(`gh[pousr]_[a-z0-9]{20,}`)},
	{"bearer-header", regexp.MustCompile(`bearer[ \t]+[a-z0-9._-]{24,}`)},
	{"assigned-secret", regexp.MustCompile(`(token|secret|password|passwd|api[_-]?key)[ \t]*[:=]{1,2}[ \t]*["']?[a-z0-9._-]{24,}`)},
}

// carriedCredential returns the name of the first credential shape in text
// whose value is not obviously synthetic, or "" when there is none.
func carriedCredential(text string) string {
	folded := foldASCII(text)
	for _, shape := range credentialShapes {
		for _, at := range shape.pattern.FindAllStringIndex(folded, -1) {
			if !synthetic(text[at[0]:at[1]]) {
				return shape.name
			}
		}
	}
	return ""
}

// synthetic reports whether a matched value reads like a hand-written
// fixture: a word that says so, one character four times over, or six
// characters running up or down the alphabet. It is secret-rules.awk's
// allowance, so a value the scan lets into a test the hook lets into a call.
func synthetic(value string) bool {
	if fixtureWord.MatchString(strings.ToLower(value)) {
		return true
	}
	run, sequence, step := 1, 1, 0
	for i := 1; i < len(value); i++ {
		here, previous := ordinal(value[i]), ordinal(value[i-1])
		if value[i] == value[i-1] {
			if run++; run >= 4 {
				return true
			}
		} else {
			run = 1
		}
		switch difference := here - previous; {
		case here == 0 || previous == 0 || (difference != 1 && difference != -1):
			sequence, step = 1, 0
		case difference == step:
			if sequence++; sequence >= 6 {
				return true
			}
		default:
			sequence, step = 2, difference
		}
	}
	return false
}

var fixtureWord = regexp.MustCompile(`fixture|synthetic|fake|dummy|example|placeholder|redacted|not-a-real`)

// ordinal is a character's place in secret-rules.awk's alphabet of digits,
// then lowercase, then uppercase letters, counting from 1; 0 for any other.
func ordinal(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c-'0') + 1
	case c >= 'a' && c <= 'z':
		return int(c-'a') + 11
	case c >= 'A' && c <= 'Z':
		return int(c-'A') + 37
	}
	return 0
}

// carriesToGitHub reports whether a simple command is a gh or git call,
// the two that publish what they are handed.
func carriesToGitHub(words []string) bool {
	name := program(words[0])
	return name == "gh" || name == "git"
}

// checkCredentials refuses a Bash call that hands gh or git a credential.
// The whole script is read, here-documents included, since a value reaches
// the call as an argument, through a pipe, or in a body written a line
// before it; and so is any body, notes or gist file the call names.
func checkCredentials(script, cwd string, calls [][]string) string {
	text := script
	for _, words := range calls {
		for _, path := range bodyFiles(words) {
			if !filepath.IsAbs(path) && cwd != "" {
				path = filepath.Join(cwd, path)
			}
			text += "\n" + readBodyFile(path)
		}
	}
	shape := carriedCredential(text)
	if shape == "" {
		return ""
	}
	return "this call hands gh or git what reads as a " + shape + ", and what they are handed can be published; " +
		"quote a credential as at most eight characters and an ellipsis, or let the command read it from the environment."
}

// maxBodyFile bounds what is read of one body file. A body is text a person
// reads; anything longer is not one, and the hook must stay quick.
const maxBodyFile = 1 << 20

// readBodyFile is the start of the file at path, or "" when it cannot be
// read: one written by the same call does not exist yet, and that call's
// script is read instead.
func readBodyFile(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	body, _ := io.ReadAll(io.LimitReader(file, maxBodyFile))
	return string(body)
}

// bodyFiles are the text files a gh or git call names to publish: gh's
// --body-file, -F and --notes-file, a gh api field whose value is @<file>
// and its --input, the files gh gist creates or edits from, and git's -F
// and --file. Standard input, "-", is in the script already.
func bodyFiles(words []string) []string {
	var flags map[string]bool
	fields := false
	switch program(words[0]) {
	case "gh":
		flags = map[string]bool{"--body-file": true, "--notes-file": true}
		switch {
		case len(words) > 1 && words[1] == "api":
			flags["--input"] = true
			fields = true // gh api's -F is a field, and a field's value can be a file
		case len(words) > 2 && words[1] == "gist" && (words[2] == "create" || words[2] == "edit"):
			return gistFiles(words[3:])
		default:
			flags["-F"] = true
		}
	case "git":
		flags = map[string]bool{"-F": true, "--file": true}
	default:
		return nil
	}
	var files []string
	for i, word := range words {
		name, value, joined := strings.Cut(word, "=")
		switch {
		case fields && (word == "-F" || word == "--field") && i+1 < len(words):
			value = fieldFile(words[i+1])
		case fields && (name == "--field" || strings.HasPrefix(word, "-F") && len(word) > 2):
			value = fieldFile(strings.TrimPrefix(strings.TrimPrefix(word, "--field="), "-F"))
		case joined && strings.HasPrefix(name, "--") && flags[name]:
		case flags[word] && i+1 < len(words):
			value = words[i+1]
		default:
			continue
		}
		if value != "-" && value != "" {
			files = append(files, value)
		}
	}
	return files
}

// fieldFile is the file a gh api field reads its value from, key=@<file>,
// or "" for a field whose value is written out.
func fieldFile(field string) string {
	_, value, _ := strings.Cut(field, "=")
	if path, isFile := strings.CutPrefix(value, "@"); isFile {
		return path
	}
	return ""
}

// gistValued are the gh gist options that take the next word as a value.
var gistValued = map[string]bool{"-d": true, "--desc": true, "-f": true, "--filename": true, "-a": true, "--add": true, "-r": true, "--remove": true}

// gistFiles are the files gh gist create or edit publishes: its
// arguments, and the file edit --add names. The gist edit names comes
// along with them, and names no file the call can read.
func gistFiles(args []string) []string {
	var files []string
	for i := 0; i < len(args); i++ {
		switch arg := args[i]; {
		case (arg == "-a" || arg == "--add") && i+1 < len(args):
			files = append(files, args[i+1])
			i++
		case strings.HasPrefix(arg, "--add="):
			files = append(files, strings.TrimPrefix(arg, "--add="))
		case gistValued[arg]:
			i++
		case !strings.HasPrefix(arg, "-"):
			files = append(files, arg)
		}
	}
	return files
}

// foldASCII lowercases the ASCII letters of s and leaves every other byte
// where it is, so an index into the folded copy is an index into s.
func foldASCII(s string) string {
	folded := []byte(s)
	for i, c := range folded {
		if c >= 'A' && c <= 'Z' {
			folded[i] = c + 'a' - 'A'
		}
	}
	return string(folded)
}
