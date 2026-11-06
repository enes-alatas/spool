package promptschat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	// maxMembers bounds the bots a team import fetches and the loops it
	// makes; the largest team on prompts.chat on 2026-11-03 had 5.
	maxMembers = 12
	// maxChannel is the longest channel name the hub takes.
	maxChannel = 32
	// maxDescription is the longest channel description the hub takes.
	maxDescription = 280
	// maxLabel is the longest handoff label a mission's bullet carries.
	maxLabel = 120
)

// teamPath is a team's page, or its .json under /api.
var teamPath = regexp.MustCompile(`^/(?:api/)?bots/teams/([A-Za-z0-9][A-Za-z0-9_-]*)/?$`)

// segment is an id or slug a bot's page is built from.
var segment = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*$`)

// Team is the loops and the channel a team would make. Each loop's
// Draft has its handoffs in its mission and its name settled against the
// names already taken.
type Team struct {
	Channel     string     `json:"channel"`
	Description string     `json:"description"`
	Loops       []TeamLoop `json:"loops"`
	// Source is the team's page on prompts.chat.
	Source string `json:"source"`
}

// TeamLoop is one member of a team: its role on the team, the loop it
// makes, and the handoffs its mission ends with, in the mission's order,
// for the room to show without reading the mission's words (#1266).
type TeamLoop struct {
	Role string `json:"role"`
	Draft
	Handoffs []Handoff `json:"handoffs"`
}

// The directions of a Handoff.
const (
	HandoffTo   = "to"
	HandoffFrom = "from"
)

// Handoff is one handoff of a team's loop: Direction HandoffTo the other
// loop, or HandoffFrom it, by the other loop's name in the same draft,
// with the team's label for it on one line, empty when it has none.
type Handoff struct {
	Direction string `json:"direction"`
	Loop      string `json:"loop"`
	Label     string `json:"label"`
}

// team is the part of a team's .json an import reads.
type team struct {
	Slug        string `json:"slug"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Members     []struct {
		ID   string `json:"id"`
		Slug string `json:"slug"`
		Role string `json:"role"`
	} `json:"members"`
	Handoffs []struct {
		From  string `json:"from"`
		To    string `json:"to"`
		Label string `json:"label"`
	} `json:"handoffs"`
}

// ImportTeam fetches the team raw names, and each of its bots, through
// fetcher and answers what it would make. loopTaken and channelTaken say
// whether a name is in use on the hub; a name taken is suffixed -2, -3
// and so on. A URL or page that is no team, a team with no member or too
// many, or a handoff to a bot the team doesn't have is a *Refusal; any
// other error is a fetch failing.
func ImportTeam(ctx context.Context, fetcher Fetcher, raw string, loopTaken, channelTaken func(string) bool) (Team, error) {
	slug, err := teamSlug(raw)
	if err != nil {
		return Team{}, err
	}
	body, err := fetcher.Fetch(ctx, "/api/bots/teams/"+slug)
	if errors.Is(err, ErrNotFound) {
		return Team{}, refuse("%s has no team at that address.", Host)
	}
	if err != nil {
		return Team{}, err
	}
	var read team
	if err := json.Unmarshal(body, &read); err != nil || len(read.Members) == 0 {
		return Team{}, refuse("That page on %s isn't a team: it has no members.", Host)
	}
	if len(read.Members) > maxMembers {
		return Team{}, refuse("That team has %d bots; Spool imports a team of at most %d.", len(read.Members), maxMembers)
	}

	made := Team{
		Channel:     uniqueName(channelName(read.Slug, slug), maxChannel, channelTaken, nil),
		Description: oneLine(read.Description, maxDescription),
		Source:      "https://" + Host + "/bots/teams/" + slug,
	}
	names := map[string]string{} // member id → loop name
	inTeam := map[string]bool{}
	for _, member := range read.Members {
		if !segment.MatchString(member.ID) || !segment.MatchString(member.Slug) {
			return Team{}, refuse("That team names a bot %s has no page for.", Host)
		}
		if _, twice := names[member.ID]; twice {
			return Team{}, refuse("That team names one bot twice.")
		}
		draft, err := draftAt(ctx, fetcher, "https://"+Host+"/bots/"+member.ID+"_"+member.Slug,
			"That team names a bot %s doesn't have.")
		if err != nil {
			return Team{}, err
		}
		base := loopName(member.Role)
		if base == fallbackName {
			base = draft.Name
		}
		draft.Name = uniqueName(base, maxName, loopTaken, inTeam)
		inTeam[draft.Name] = true
		names[member.ID] = draft.Name
		made.Loops = append(made.Loops, TeamLoop{Role: plain(member.Role), Draft: draft, Handoffs: []Handoff{}})
	}

	for _, handoff := range read.Handoffs {
		from, fromOK := names[handoff.From]
		to, toOK := names[handoff.To]
		if !fromOK || !toOK {
			return Team{}, refuse("That team hands work to or from a bot it doesn't have.")
		}
		label := oneLine(handoff.Label, maxLabel)
		for index, member := range read.Members {
			switch member.ID {
			case handoff.From:
				made.Loops[index].Handoffs = append(made.Loops[index].Handoffs, Handoff{HandoffTo, to, label})
			case handoff.To:
				made.Loops[index].Handoffs = append(made.Loops[index].Handoffs, Handoff{HandoffFrom, from, label})
			}
		}
	}
	for index := range made.Loops {
		closing := []string{"You are in the channel " + made.Channel + teammates(made.Loops, index) + "."}
		if len(made.Loops[index].Handoffs) > 0 {
			closing = append(closing, "Your handoffs:")
			for _, handoff := range made.Loops[index].Handoffs {
				line := "- " + handoff.Direction + " @" + handoff.Loop
				if handoff.Label != "" {
					line += ": " + handoff.Label
				}
				closing = append(closing, line)
			}
		}
		made.Loops[index].Mission += "\n\n" + strings.Join(closing, "\n")
		if len(made.Loops[index].Mission) > maxMission {
			return Team{}, refuse("A bot on that team is too long to import: its mission would be over %d KiB. Trim it on %s.", maxMission>>10, Host)
		}
	}
	return made, nil
}

// teamSlug is the slug of the team raw names by its page or its .json. A
// URL on any other host, or no team's page on prompts.chat, is refused.
func teamSlug(raw string) (string, error) {
	parsed, err := onPromptsChat(raw, "team")
	if err != nil {
		return "", err
	}
	match := teamPath.FindStringSubmatch(parsed.Path)
	if match == nil {
		return "", refuse("That page isn't a team on %s. A team's address starts with https://%s/bots/teams/.", Host, Host)
	}
	return match[1], nil
}

// channelName makes slug a valid channel name: lower case, a run of
// anything but a letter or digit as one -, at most maxChannel long,
// starting with a letter or digit. A slug that leaves nothing gives
// fallback's.
func channelName(slug, fallback string) string {
	var name strings.Builder
	dash := false
	for _, char := range strings.ToLower(plain(slug)) {
		switch {
		case char >= 'a' && char <= 'z', char >= '0' && char <= '9':
			name.WriteRune(char)
			dash = false
		case !dash && name.Len() > 0:
			name.WriteByte('-')
			dash = true
		}
	}
	made := strings.Trim(name.String(), "-")
	if len(made) > maxChannel {
		made = strings.TrimRight(made[:maxChannel], "-")
	}
	if made == "" && fallback != slug {
		return channelName(fallback, fallback)
	}
	if made == "" {
		return "imported-team"
	}
	return made
}

// uniqueName is base, or base suffixed -2, -3 and so on, cut to at most
// longest, whichever first is neither taken nor in also.
func uniqueName(base string, longest int, taken func(string) bool, also map[string]bool) string {
	for number := 1; ; number++ {
		name := base
		if number > 1 {
			suffix := fmt.Sprintf("-%d", number)
			cut := base
			if len(cut)+len(suffix) > longest {
				cut = strings.TrimRight(cut[:longest-len(suffix)], "_-")
			}
			name = cut + suffix
		}
		if !also[name] && (taken == nil || !taken(name)) {
			return name
		}
	}
}

// oneLine is singleLine cut to at most longest characters.
func oneLine(text string, longest int) string {
	made := singleLine(text)
	if utf8.RuneCountInString(made) <= longest {
		return made
	}
	return strings.TrimSpace(string([]rune(made)[:longest]))
}

// teammates names every loop of loops but the one at index, as
// " with @a, @b and @c", or "" when it is alone.
func teammates(loops []TeamLoop, index int) string {
	var others []string
	for other, loop := range loops {
		if other != index {
			others = append(others, "@"+loop.Name)
		}
	}
	switch len(others) {
	case 0:
		return ""
	case 1:
		return " with " + others[0]
	}
	return " with " + strings.Join(others[:len(others)-1], ", ") + " and " + others[len(others)-1]
}
