// Package promptschat reads a prompts.chat bot into a loop draft
// (ADR-0063). The operator pastes a bot's URL; the hub fetches that bot's
// public `.json` from prompts.chat and nowhere else, and answers a draft
// of the loop it would make, which the operator edits before creating it.
//
// What prompts.chat serves is untrusted: it is text someone else wrote,
// read under a size bound, and it reaches a loop only through the mission
// the operator saw in the draft and chose to create.
package promptschat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"
)

// Host is the one host the hub fetches a bot from.
const Host = "prompts.chat"

const (
	// fetchTimeout bounds one fetch of a bot.
	fetchTimeout = 15 * time.Second
	// maxBot bounds the bytes read of a bot's .json; the largest bot on
	// prompts.chat on 2026-11-02 was under 10 KiB.
	maxBot = 1 << 20
	// maxName is the longest loop name the hub takes.
	maxName = 32
	// fallbackName is the loop name of a slug that leaves too little.
	fallbackName = "imported-bot"
	// maxMission bounds the mission a bot makes: it goes into every turn
	// of the loop, so a bloated or hostile bot is refused, not imported.
	maxMission = 64 << 10
)

// Refusal is a URL or a page the hub won't read as a bot. Reason is a
// sentence the control room shows.
type Refusal struct{ Reason string }

func (refusal *Refusal) Error() string { return refusal.Reason }

func refuse(format string, args ...any) error {
	return &Refusal{Reason: fmt.Sprintf(format, args...)}
}

// Fetcher gets the bytes at a path on prompts.chat, such as
// /bots/<bot>.json. HTTPFetcher is the hub's; a test hands in fixture
// bytes, so it never reaches the network.
type Fetcher interface {
	Fetch(ctx context.Context, path string) ([]byte, error)
}

// ErrNotFound is a URL prompts.chat answered 404 for.
var ErrNotFound = errors.New("promptschat: not found")

// HTTPFetcher fetches from Base with a plain GET: no cookie, no
// credential and no body. It follows no redirect, and takes only a path
// fetchPath matches.
type HTTPFetcher struct {
	// Base is where prompts.chat is reached; empty means
	// https://prompts.chat. A test points it at a stand-in server
	// (ADR-0063).
	Base string
	// Client is the HTTP client fetches go through; nil means one with
	// fetchTimeout and no redirects.
	Client *http.Client
}

var fetchClient = &http.Client{
	Timeout:       fetchTimeout,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// Fetch gets path from Base.
func (fetcher HTTPFetcher) Fetch(ctx context.Context, path string) ([]byte, error) {
	if !fetchPath.MatchString(path) {
		return nil, fmt.Errorf("promptschat: %q is no path a bot or a team is fetched from", path)
	}
	base := fetcher.Base
	if base == "" {
		base = "https://" + Host
	}
	parsed, err := url.Parse(strings.TrimSuffix(base, "/") + path)
	if err != nil {
		return nil, err
	}
	client := fetcher.Client
	if client == nil {
		client = fetchClient
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "spool")
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("promptschat: fetch %s: %w", parsed.Path, err)
	}
	defer func() { _ = response.Body.Close() }()
	switch {
	case response.StatusCode == http.StatusNotFound:
		return nil, ErrNotFound
	case response.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("promptschat: fetch %s: %s", parsed.Path, response.Status)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBot+1))
	if err != nil {
		return nil, fmt.Errorf("promptschat: read %s: %w", parsed.Path, err)
	}
	if len(body) > maxBot {
		return nil, fmt.Errorf("promptschat: %s is over %d bytes", parsed.Path, maxBot)
	}
	return body, nil
}

// fetchPath is every path a fetch may ask for: a bot's .json and a
// team's.
var fetchPath = regexp.MustCompile(`^/(bots/[A-Za-z0-9][A-Za-z0-9_-]*\.json|api/bots/teams/[A-Za-z0-9][A-Za-z0-9_-]*)$`)

// botPath is a bot's page, with .md or .json or neither.
var botPath = regexp.MustCompile(`^/bots/([A-Za-z0-9][A-Za-z0-9_-]*)(\.md|\.json)?/?$`)

// notBots are the pages under /bots/ that are no bot.
var notBots = map[string]bool{"teams": true, "new": true}

// BotPage is the page of the bot raw names by its page, its .md or its
// .json. A URL on any other host, or no bot's page on prompts.chat, is
// refused. The bot's .json is its page's path plus .json.
func BotPage(raw string) (string, error) {
	parsed, err := onPromptsChat(raw, "bot")
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(parsed.Path, "/bots/teams/") || strings.HasPrefix(parsed.Path, "/api/bots/teams/") {
		return "", refuse("That is a team on %s. Import its bots one at a time for now.", Host)
	}
	match := botPath.FindStringSubmatch(parsed.Path)
	if match == nil || notBots[match[1]] {
		return "", refuse("That page isn't a bot on %s. A bot's address starts with https://%s/bots/.", Host, Host)
	}
	return "https://" + Host + "/bots/" + match[1], nil
}

// onPromptsChat is raw parsed, refused unless it is an http or https URL
// on prompts.chat by exact name, with no port or credentials. what names
// the page it should be, for the refusal.
func onPromptsChat(raw, what string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" {
		return nil, refuse("That isn't a URL. Paste a %s's address on %s.", what, Host)
	}
	if !strings.EqualFold(parsed.Hostname(), Host) || parsed.Port() != "" || parsed.User != nil {
		return nil, refuse("Spool reads %ss from %s only, not from %s.", what, Host, parsed.Host)
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return nil, refuse("Paste the %s's https address on %s.", what, Host)
	}
	return parsed, nil
}

// Draft is the loop a bot would make, for the operator to edit before
// creating it. Runtime is "cloud" when the bot runs only in the cloud and
// empty otherwise, for the hub's default. Egress is the hosts of the
// bot's connectors the hub knows, in the order the bot names them.
type Draft struct {
	Name    string       `json:"name"`
	Mission string       `json:"mission"`
	Runtime string       `json:"runtime"`
	Egress  []EgressHint `json:"egress"`
	// Source is the bot's page on prompts.chat.
	Source string `json:"source"`
}

// EgressHint is a host a bot's connector is on, with that connector as
// the bot spells it, so the operator sees why it is suggested.
type EgressHint struct {
	Host      string `json:"host"`
	Connector string `json:"connector"`
}

// skill is a skill a bot names.
type skill struct {
	Name string `json:"name"`
}

// bot is the part of a bot's .json a draft reads.
type bot struct {
	Slug                 string   `json:"slug"`
	Name                 string   `json:"name"`
	Instructions         string   `json:"instructions"`
	ComputerRequirements []string `json:"computerRequirements"`
	Routines             []struct {
		Name         string `json:"name"`
		Trigger      string `json:"trigger"`
		TriggerLabel string `json:"triggerLabel"`
		Prompt       string `json:"prompt"`
	} `json:"routines"`
	Skills     []skill  `json:"skills"`
	Connectors []string `json:"connectors"`
	URLs       struct {
		Page string `json:"page"`
	} `json:"urls"`
}

// Import fetches the bot raw names through fetcher and answers its draft.
// A URL or page that is no bot is a *Refusal; any other error is the
// fetch failing.
func Import(ctx context.Context, fetcher Fetcher, raw string) (Draft, error) {
	page, err := BotPage(raw)
	if err != nil {
		return Draft{}, err
	}
	return draftAt(ctx, fetcher, page, "%s has no bot at that address.")
}

// draftAt is the draft of the bot whose page is page, fetched through
// fetcher. missing is the refusal when prompts.chat has no such bot, with
// a %s for its host.
func draftAt(ctx context.Context, fetcher Fetcher, page, missing string) (Draft, error) {
	body, err := fetcher.Fetch(ctx, strings.TrimPrefix(page, "https://"+Host)+".json")
	if errors.Is(err, ErrNotFound) {
		return Draft{}, refuse(missing, Host)
	}
	if err != nil {
		return Draft{}, err
	}
	var read bot
	if err := json.Unmarshal(body, &read); err != nil || strings.TrimSpace(read.Slug) == "" || strings.TrimSpace(read.Instructions) == "" {
		return Draft{}, refuse("That page on %s isn't a bot: it has no instructions.", Host)
	}
	made := mission(read, page)
	if len(made) > maxMission {
		return Draft{}, refuse("That bot is too long to import: its mission would be over %d KiB. Trim it on %s.", maxMission>>10, Host)
	}
	return Draft{
		Name:    loopName(read.Slug),
		Mission: made,
		Runtime: runtimeHint(read.ComputerRequirements),
		Egress:  egressHint(read.Connectors),
		Source:  page,
	}, nil
}

// loopName makes slug a valid loop name: lower case, a run of anything
// but a letter, digit, - or _ as one -, at most maxName long, starting
// with a letter or digit.
func loopName(slug string) string {
	var name strings.Builder
	dash := false
	for _, char := range strings.ToLower(plain(slug)) {
		switch {
		case char >= 'a' && char <= 'z', char >= '0' && char <= '9', char == '_', char == '-':
			name.WriteRune(char)
			dash = false
		case !dash && name.Len() > 0:
			name.WriteByte('-')
			dash = true
		}
	}
	made := strings.TrimLeft(name.String(), "_-")
	if len(made) > maxName {
		made = made[:maxName]
	}
	made = strings.TrimRight(made, "_-")
	if len(made) < 2 {
		return fallbackName
	}
	return made
}

// mission is the bot's instructions, then a line naming page, then a
// sentence per routine, then the skills and connectors it names.
func mission(read bot, page string) string {
	parts := []string{plain(read.Instructions), "Imported from " + page + "."}
	var routines []string
	for _, routine := range read.Routines {
		when := plain(routine.TriggerLabel)
		if when == "" {
			when = plain(routine.Trigger)
		}
		sentence := plain(routine.Name)
		if when != "" {
			sentence += " (" + when + ")"
		}
		if prompt := plain(routine.Prompt); prompt != "" {
			sentence += ": " + prompt
		}
		if sentence != "" {
			routines = append(routines, "- "+sentence)
		}
	}
	if len(routines) > 0 {
		parts = append(parts, "Routines:\n"+strings.Join(routines, "\n"))
	}
	var named []string
	if skills := names(read.Skills, func(item skill) string { return item.Name }); skills != "" {
		named = append(named, "Skills: "+skills+".")
	}
	if connectors := names(read.Connectors, func(connector string) string { return connector }); connectors != "" {
		named = append(named, "Connectors: "+connectors+".")
	}
	if len(named) > 0 {
		parts = append(parts, strings.Join(named, "\n"))
	}
	return strings.Join(parts, "\n\n")
}

// names joins the non-empty names of items.
func names[Item any](items []Item, name func(Item) string) string {
	var kept []string
	for _, item := range items {
		if text := plain(name(item)); text != "" {
			kept = append(kept, text)
		}
	}
	return strings.Join(kept, ", ")
}

// plain is text with its ends trimmed and these dropped: every control
// character but a newline or tab, every format character (bidi overrides,
// zero-width characters, Unicode tags) and both variation-selector
// blocks. A model reads them; the operator reviewing the draft sees
// nothing, and tags and the supplement's selectors can spell whole text.
func plain(text string) string {
	return strings.TrimSpace(strings.Map(func(char rune) rune {
		if unicode.Is(unicode.Cc, char) && char != '\n' && char != '\t' || unicode.Is(unicode.Cf, char) || variationSelector(char) {
			return -1
		}
		return char
	}, text))
}

// variationSelector is a rune of Variation Selectors (U+FE00–U+FE0F) or
// its supplement (U+E0100–U+E01EF).
func variationSelector(char rune) bool {
	return char >= 0xFE00 && char <= 0xFE0F || char >= 0xE0100 && char <= 0xE01EF
}

// runtimeHint is "cloud" for a bot that needs the cloud alone, and empty,
// for the hub's default, for any other.
func runtimeHint(requirements []string) string {
	if len(requirements) == 1 && strings.EqualFold(requirements[0], "CLOUD") {
		return "cloud"
	}
	return ""
}

// connectorHosts are the hosts a connector's API is on, for the
// connectors whose host doesn't depend on the account. A connector not
// here is named in the mission only.
var connectorHosts = map[string][]string{
	"github":          {"github.com", "api.github.com"},
	"slack":           {"slack.com"},
	"gmail":           {"gmail.googleapis.com"},
	"google drive":    {"www.googleapis.com"},
	"google calendar": {"www.googleapis.com"},
	"notion":          {"api.notion.com"},
	"linear":          {"api.linear.app"},
	"vercel":          {"api.vercel.com"},
	"telegram":        {"api.telegram.org"},
}

// egressHint is the hosts of connectors, each once with the first
// connector that names it, in the order the bot names its connectors.
func egressHint(connectors []string) []EgressHint {
	hints := []EgressHint{}
	seen := map[string]bool{}
	for _, connector := range connectors {
		named := plain(connector)
		for _, host := range connectorHosts[strings.ToLower(named)] {
			if !seen[host] {
				seen[host] = true
				hints = append(hints, EgressHint{Host: host, Connector: named})
			}
		}
	}
	return hints
}
