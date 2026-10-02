package slack

import (
	_ "embed"
	"strings"
)

//go:generate go run ./emojigen -o emoji_names.txt

// Slack reports and sets a reaction by name ("+1", "+1::skin-tone-3"); the
// hub keeps the Unicode it draws, so a reaction reads the same on every
// surface (ADR-0040). The names are Slack's own, from the table it draws
// them from. A name the table does not know is a custom emoji of the
// workspace, kept as :name:.

//go:embed emoji_names.txt
var emojiNames string

var (
	emojiByName = map[string]string{}
	// nameByEmoji is keyed without presentation selectors, which a surface
	// may or may not send for the same emoji.
	nameByEmoji = map[string]string{}
)

func init() {
	for _, line := range strings.Split(emojiNames, "\n") {
		name, emoji, ok := strings.Cut(line, "\t")
		if !ok || strings.HasPrefix(line, "#") {
			continue
		}
		emojiByName[name] = emoji
		if key := bare(emoji); nameByEmoji[key] == "" {
			nameByEmoji[key] = name // the primary name comes first
		}
	}
}

// skinTones are Slack's suffixes for the five modifiers, lightest first.
var skinTones = []string{"skin-tone-2", "skin-tone-3", "skin-tone-4", "skin-tone-5", "skin-tone-6"}

const firstTone = 0x1F3FB

// emojiOf is the emoji a Slack reaction name draws: Unicode for a name
// Slack defines, :name: for a custom one.
func emojiOf(name string) string {
	if emoji, ok := emojiByName[name]; ok {
		return emoji
	}
	base, suffix, toned := strings.Cut(name, "::")
	if emoji, ok := emojiByName[base]; ok && toned {
		for i, tone := range skinTones {
			if suffix == tone {
				return withTone(emoji, firstTone+rune(i))
			}
		}
	}
	return ":" + name + ":"
}

// slackName is the name Slack sets an emoji by, "" when it has none: a
// :name: is the workspace's custom emoji of that name.
func slackName(emoji string) string {
	if custom, ok := strings.CutPrefix(emoji, ":"); ok {
		return strings.TrimSuffix(custom, ":")
	}
	if name := nameByEmoji[bare(emoji)]; name != "" {
		return name
	}
	// a skin tone follows the first code point (withTone)
	runes := []rune(emoji)
	if len(runes) > 1 && runes[1] >= firstTone && runes[1] < firstTone+5 {
		base := string(runes[0]) + string(runes[2:])
		if name := nameByEmoji[bare(base)]; name != "" {
			return name + "::" + skinTones[runes[1]-firstTone]
		}
	}
	return ""
}

// withTone puts a skin tone on an emoji the way Unicode's sequences do:
// after the first code point, in place of a presentation selector there.
// emojigen checks the table against this rule and writes out every name it
// does not draw.
func withTone(emoji string, tone rune) string {
	runes := []rune(emoji)
	rest := runes[1:]
	if len(rest) > 0 && rest[0] == 0xFE0F {
		rest = rest[1:]
	}
	return string(runes[0]) + string(tone) + string(rest)
}

// bare is an emoji without presentation selectors.
func bare(emoji string) string { return strings.ReplaceAll(emoji, "\uFE0F", "") }
