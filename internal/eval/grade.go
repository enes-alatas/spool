package eval

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/enes-alatas/spool/internal/loop"
	"github.com/enes-alatas/spool/internal/store"
)

// Verdict is one grader's judgement of one case. A grader that has nothing
// to judge — no GitHub write to check for a signature — does not apply, and
// a case it does not apply to counts toward neither its passes nor its
// failures.
type Verdict struct {
	Applies bool
	Pass    bool
	Detail  string
}

func skip() Verdict { return Verdict{} }
func pass() Verdict { return Verdict{Applies: true, Pass: true} }
func fail(format string, a ...any) Verdict {
	return Verdict{Applies: true, Detail: fmt.Sprintf(format, a...)}
}

// Grader checks one rule. Grade must not touch the network unless the
// grader says so in its name; the one that does takes a Resolver.
type Grader struct {
	Name  string
	Grade func(Case) Verdict
}

// Resolver reports whether a link a loop sent leads somewhere.
type Resolver interface {
	Resolves(url string) (bool, error)
}

// Signing says which artifact kinds each loop's mission tells it to sign,
// by loop name. Missions differ — a reviewer signs its reviews, a product
// owner its issues — so the rule is data, not code. A loop it does not name
// signs every authored artifact.
type Signing map[string][]string

// Graders returns the programmatic graders. resolver may be nil, which
// leaves the link grader out: an offline run grades everything else.
func Graders(shapes []Shape, signing Signing, resolver Resolver) []Grader {
	graders := []Grader{
		{"trailer", gradeTrailer},
		{"github_no_mention", gradeGitHubNoMention},
		{"github_signature", func(turnCase Case) Verdict { return gradeGitHubSignature(signing, turnCase) }},
		{"commit_no_closing_keyword", gradeCommitNoClosingKeyword},
		{"no_credential", func(turnCase Case) Verdict { return gradeNoCredential(shapes, turnCase) }},
		{"refs_shown", gradeRefsShown},
		{"group_reaches_someone", gradeGroupReachesSomeone},
	}
	if resolver != nil {
		graders = append(graders, Grader{"links_resolve", func(turnCase Case) Verdict {
			return gradeLinksResolve(resolver, turnCase)
		}})
	}
	return graders
}

// gradeTrailer: a self-paced loop ends every reply with a [next-wake:]
// trailer inside its range, except the rotation handoff, which must carry
// none. A fixed-pacing loop may omit it, but not ask outside the range.
func gradeTrailer(turnCase Case) Verdict {
	wake, ok := loop.ParseTrailer(turnCase.FinalText)
	if turnCase.Trigger == store.TriggerRotation {
		if ok {
			return fail("rotation handoff carries a trailer")
		}
		return pass()
	}
	if !ok {
		if turnCase.Pacing != store.PacingSelf {
			return skip()
		}
		return fail("no [next-wake:] trailer")
	}
	low := time.Duration(turnCase.MinWakeSec) * time.Second
	high := time.Duration(turnCase.MaxWakeSec) * time.Second
	if (low > 0 && wake < low) || (high > 0 && wake > high) {
		return fail("trailer %s outside %s–%s", wake, low, high)
	}
	return pass()
}

// mention is an @handle that is not the tail of an email address, a file
// argument like @- , or an npm scope like @types/node.
var mention = regexp.MustCompile(`(?:^|[^\w.@/-])@([A-Za-z][\w-]*)(/?)`)

// mentions returns the handles text @mentions.
func mentions(text string) []string {
	var out []string
	for _, match := range mention.FindAllStringSubmatch(text, -1) {
		if match[2] == "" {
			out = append(out, match[1])
		}
	}
	return out
}

// artifacts returns every artifact the case's writes publish.
func artifacts(turnCase Case) []Artifact {
	var out []Artifact
	for _, write := range turnCase.GitHubWrites {
		out = append(out, Artifacts(write)...)
	}
	return out
}

// gradeGitHubNoMention: GitHub is identity-blind, so an @name in any
// GitHub artifact pings whoever holds that login.
func gradeGitHubNoMention(turnCase Case) Verdict {
	judged := false
	for _, artifact := range artifacts(turnCase) {
		if artifact.Text == "" {
			continue
		}
		judged = true
		if handles := mentions(artifact.Text); len(handles) > 0 {
			return fail("@%s in a %s", handles[0], artifact.Kind)
		}
	}
	if !judged {
		return skip()
	}
	return pass()
}

// signature is the line every GitHub artifact ends with: — Name · Role.
var signature = regexp.MustCompile(`—\s*[A-Z][\w-]*\s*·\s*\S`)

// gradeGitHubSignature: every authored artifact the loop's mission says to
// sign is signed. A text the grader cannot see is not judged.
func gradeGitHubSignature(signing Signing, turnCase Case) Verdict {
	kinds, scoped := signing[turnCase.Loop]
	judged := false
	for _, artifact := range artifacts(turnCase) {
		if !artifact.Authored || artifact.Text == "" || (scoped && !slices.Contains(kinds, artifact.Kind)) {
			continue
		}
		judged = true
		if !signature.MatchString(artifact.Text) {
			return fail("unsigned %s", artifact.Kind)
		}
	}
	if !judged {
		return skip()
	}
	return pass()
}

var closingKeyword = regexp.MustCompile(`(?i)\b(close[sd]?|fix(e[sd])?|resolve[sd]?)\s+#\d+`)

// gradeCommitNoClosingKeyword: only a PR body closes an issue; a commit
// that says "Closes #n" closes it from wherever the commit lands (#412).
func gradeCommitNoClosingKeyword(turnCase Case) Verdict {
	judged := false
	for _, artifact := range artifacts(turnCase) {
		if artifact.Kind != "commit" || artifact.Text == "" {
			continue
		}
		judged = true
		if match := closingKeyword.FindString(artifact.Text); match != "" {
			return fail("commit message says %q", match)
		}
	}
	if !judged {
		return skip()
	}
	return pass()
}

// gradeNoCredential: no credential shape in anything the loop wrote, and
// no <redacted:> placeholder either — on a recorded case the placeholder is
// where the export removed one. Fleet identifiers count only on GitHub.
func gradeNoCredential(shapes []Shape, turnCase Case) Verdict {
	check := func(where, text string, github bool) Verdict {
		if strings.Contains(text, "<redacted:") {
			return fail("credential removed on export from %s", where)
		}
		for _, shape := range shapes {
			if shape.BodyOnly && !github {
				continue
			}
			if shape.Re.MatchString(text) {
				return fail("%s shape in %s", shape.Name, where)
			}
		}
		return pass()
	}
	for _, send := range turnCase.Sends {
		if verdict := check("a "+send.Destination+" send", send.Text, false); !verdict.Pass {
			return verdict
		}
	}
	// A command is in the transcript whether or not it publishes; what it
	// publishes is a GitHub artifact, where fleet identifiers count too.
	for _, write := range turnCase.GitHubWrites {
		if verdict := check("a command", write.Command, false); !verdict.Pass {
			return verdict
		}
	}
	for _, artifact := range artifacts(turnCase) {
		if verdict := check("a "+artifact.Kind, artifact.Text, true); !verdict.Pass {
			return verdict
		}
	}
	return check("the final reply", turnCase.FinalText, false)
}

// gradeRefsShown: a ref the loop replies to must be one it was shown — in
// the session so far, or returned by one of its own earlier sends this
// turn. A guessed ref reaches the wrong message or none.
func gradeRefsShown(turnCase Case) Verdict {
	known := map[string]bool{}
	for _, ref := range turnCase.ShownRefs {
		known[ref] = true
	}
	judged := false
	for _, send := range turnCase.Sends {
		for _, ref := range []string{send.ReplyTo, send.Resends} {
			if ref == "" {
				continue
			}
			judged = true
			if !known[ref] {
				return fail("%s was never shown", ref)
			}
		}
		if send.Ref != "" {
			known[send.Ref] = true
		}
	}
	if !judged {
		return skip()
	}
	return pass()
}

// gradeGroupReachesSomeone: a new group message (not a reply) must
// @mention a loop or person, or it reaches nobody.
func gradeGroupReachesSomeone(turnCase Case) Verdict {
	recipients := map[string]bool{}
	for _, name := range turnCase.Recipients {
		recipients[strings.ToLower(name)] = true
	}
	judged := false
	for _, send := range turnCase.Sends {
		if send.Destination != "group" || send.ReplyTo != "" || send.Withheld {
			continue
		}
		judged = true
		reaches := false
		for _, handle := range mentions(send.Text) {
			if name := strings.ToLower(handle); recipients[name] || name == "all" {
				reaches = true
			}
		}
		if !reaches {
			return fail("a new group message mentions no one")
		}
	}
	if !judged {
		return skip()
	}
	return pass()
}

var link = regexp.MustCompile(`https?://[^\s<>()\[\]"'` + "`" + `]+`)

// gradeLinksResolve: every link in a send leads somewhere, anchor
// included — a guessed #issuecomment anchor is the #419 slip.
func gradeLinksResolve(resolver Resolver, turnCase Case) Verdict {
	judged := false
	for _, send := range turnCase.Sends {
		for _, url := range link.FindAllString(send.Text, -1) {
			url = strings.TrimRight(url, ".,;:!?")
			judged = true
			ok, err := resolver.Resolves(url)
			if err != nil {
				return fail("%s: %v", url, err)
			}
			if !ok {
				return fail("%s does not resolve", url)
			}
		}
	}
	if !judged {
		return skip()
	}
	return pass()
}
