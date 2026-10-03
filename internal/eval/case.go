// Package eval grades loop turns against the fleet rules that can be checked
// mechanically: the wake trailer, no @name on GitHub, the signature line, no
// credential value, links that resolve, refs that were shown, a group message
// that reaches someone.
//
// A Case is one turn: what the loop was shown and what it did. Cases come
// from two places — recorded turns exported from the store, and a model
// replaying a recorded turn's input — and the graders cannot tell them apart,
// which is the point: the same grader scores the fleet's own record and a
// candidate prompt (#450).
package eval

// Case is one turn, reduced to what the graders read.
type Case struct {
	ID      string `json:"id"`
	Loop    string `json:"loop"`
	Trigger string `json:"trigger"`
	// Slip names a known mistake this case reproduces, for synthetic cases
	// and for recorded ones picked out by hand. Empty for the rest.
	Slip string `json:"slip,omitempty"`

	// Input is the envelope text of the turn, in order.
	Input []string `json:"input"`
	// ShownRefs is every ref the loop had been shown when the turn began:
	// the refs in this turn's envelopes and in every earlier envelope and
	// send result of the same session.
	ShownRefs []string `json:"shown_refs"`
	// Recipients is every name a group message may @mention to reach
	// someone: the other loops and the people, without the @.
	Recipients []string `json:"recipients"`
	// Pacing is the loop's: "self" loops must end every reply with a
	// trailer, "fixed" ones may.
	Pacing string `json:"pacing"`
	// MinWakeSec and MaxWakeSec bound the trailer the loop may ask for.
	MinWakeSec int `json:"min_wake_sec"`
	MaxWakeSec int `json:"max_wake_sec"`

	Sends        []Send        `json:"sends"`
	GitHubWrites []GitHubWrite `json:"github_writes"`
	// FinalText is the turn's reply: the private status note, where the
	// trailer lives.
	FinalText string `json:"final_text"`
}

// Send is one send_message call.
type Send struct {
	Destination string `json:"destination"`
	Text        string `json:"text"`
	ReplyTo     string `json:"reply_to,omitempty"`
	Resends     string `json:"resends,omitempty"`
	// Ref is the reference the send reported, when it succeeded. A later
	// send in the same turn may reply to it.
	Ref string `json:"ref,omitempty"`
	// Withheld marks a send whose text was dropped on export: an owner DM
	// never leaves the store. The graders skip what they cannot read.
	Withheld bool `json:"withheld,omitempty"`
}

// GitHubWrite is one shell command that writes to GitHub or into a commit:
// a gh create/comment/edit/review/close, or a git commit. The command is
// kept whole and split into Artifacts when graded, so a better splitter
// regrades old cases without a new export.
type GitHubWrite struct {
	Command string `json:"command"`
	// Files is what the turn wrote to each file the command names, so a
	// body passed by file is graded like an inline one.
	Files map[string]string `json:"files,omitempty"`
}
