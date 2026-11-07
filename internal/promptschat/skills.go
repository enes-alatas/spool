package promptschat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
)

// A bot names the skills it was built to use, each with a source. When
// that source is a prompts.chat skill's page, the import fetches the
// skill and the draft carries its files, text only, for the operator to
// read before create installs them (#1364, ADR-0063). Every other source
// is named in the draft as not installed, as before (#1332).

const (
	// maxBotSkills bounds the skills a bot's import fetches. On
	// 2026-11-06, 17 of the 500 bots on prompts.chat named any skill.
	maxBotSkills = 8
	// maxSkillFiles bounds a skill's files, SKILL.md among them. The most
	// any of the 172 skills on prompts.chat held on 2026-11-07 was 18.
	maxSkillFiles = 24
	// maxSkillBytes bounds a skill's files together, after the split. The
	// largest text-only skill on 2026-11-07 was 78 KB.
	maxSkillBytes = 128 << 10
	// maxSkillPath is the longest file path in a skill the hub takes.
	maxSkillPath = 200
	// maxSkillDir is the longest skill folder name the hub makes.
	maxSkillDir = 64
	// skillMain is the file a skill's content opens with.
	skillMain = "SKILL.md"
)

// The reasons a skill a bot names isn't installed: fixed sentences, so a
// draft never echoes a skill's own text in one.
const (
	notPublished     = "prompts.chat publishes no files for it"
	notASkill        = "its link isn't a skill on prompts.chat"
	skillUnanswered  = "prompts.chat didn't answer for it"
	skillNotText     = "it carries files that aren't .md or .txt"
	skillBadPath     = "a file path in it is absolute, has a .. segment or repeats"
	skillTooBig      = "it is over 24 files or 128 KiB"
	skillTooMany     = "the bot links more than 8 skills"
	skillSameDir     = "another skill in this bot has the same name"
	fileSeparator    = "\x1fFILE:"
	fileHeaderEnd    = "\x1e"
	skillPromptsType = "SKILL"
)

// skillPage is a prompts.chat prompt's page path: /prompts/<id>_<slug>.
// The id is a cuid on every prompt read on 2026-11-07.
var skillPage = regexp.MustCompile(`^/prompts/([a-z0-9]{1,40})_[a-z0-9]+(?:-[a-z0-9]+)*$`)

// SkillFile is one file of a skill: its path under the skill's folder,
// relative and with /, and its text after the mission's text rules.
type SkillFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// Skill is a skill to install: the folder under .claude/skills/ the hub
// made for it, and its files as the draft carried them. Create takes these
// back from the operator, ticked, and installs them as given.
type Skill struct {
	Dir   string      `json:"dir"`
	Files []SkillFile `json:"files"`
}

// skillLink is the id of the prompts.chat prompt source links, and
// source rebuilt by the hub, or "" for any source that isn't exactly a
// prompt's page on prompts.chat over https: no other host, scheme, port,
// credentials, query or fragment.
func skillLink(source string) (id, page string) {
	parsed, err := url.Parse(strings.TrimSpace(source))
	if err != nil || parsed.Scheme != "https" || parsed.Host != Host || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || parsed.RawPath != "" {
		return "", ""
	}
	match := skillPage.FindStringSubmatch(parsed.Path)
	if match == nil {
		return "", ""
	}
	return match[1], "https://" + Host + parsed.Path
}

// prompt is the part of /api/prompts/<id> a skill import reads.
type prompt struct {
	Type    string `json:"type"`
	Slug    string `json:"slug"`
	Content string `json:"content"`
}

// skillHints are the skills a bot names, each with a name, in the bot's
// order. One whose source links a prompts.chat skill is fetched through
// fetcher, at most maxBotSkills of them, and carries its files when they
// pass every rule; every other is not installed, with the reason.
func skillHints(ctx context.Context, fetcher Fetcher, skills []skill) []SkillHint {
	hints := []SkillHint{}
	fetched := 0
	dirs := map[string]bool{}
	for _, item := range skills {
		name := singleLine(item.Name)
		if name == "" {
			continue
		}
		hint := SkillHint{Name: name, Description: singleLine(item.Description), Reason: notPublished, Files: []SkillFile{}}
		id, page := skillLink(item.Source)
		if id != "" {
			hint.Source = page
			if fetched++; fetched > maxBotSkills {
				hint.Reason = skillTooMany
			} else if made, reason := fetchSkill(ctx, fetcher, id); reason != "" {
				hint.Reason = reason
			} else if dirs[made.Dir] {
				hint.Reason = skillSameDir
			} else {
				dirs[made.Dir] = true
				hint.Installed, hint.Reason, hint.Dir, hint.Files = true, "", made.Dir, made.Files
			}
		}
		hints = append(hints, hint)
	}
	return hints
}

// fetchSkill fetches the prompt id and answers it as a skill, or the
// reason it isn't installed.
func fetchSkill(ctx context.Context, fetcher Fetcher, id string) (Skill, string) {
	body, err := fetcher.Fetch(ctx, "/api/prompts/"+id)
	if errors.Is(err, ErrNotFound) {
		return Skill{}, notASkill
	}
	if err != nil {
		return Skill{}, skillUnanswered
	}
	var read prompt
	if err := json.Unmarshal(body, &read); err != nil || read.Type != skillPromptsType {
		return Skill{}, notASkill
	}
	dir := skillDir(read.Slug)
	if dir == "" {
		return Skill{}, notASkill
	}
	files, reason := splitSkill(read.Content)
	if reason != "" {
		return Skill{}, reason
	}
	return Skill{Dir: dir, Files: files}, ""
}

// skillDir makes slug a skill's folder name the way loopName makes a
// loop's, at most maxSkillDir long, or "" when too little is left.
func skillDir(slug string) string {
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
	made := name.String()
	if len(made) > maxSkillDir {
		made = made[:maxSkillDir]
	}
	made = strings.Trim(made, "-")
	if len(made) < 2 {
		return ""
	}
	return made
}

// splitSkill splits a skill's content into its files: SKILL.md first,
// then each file the content's FILE: separators name, in order. The
// split comes before the text rules, which drop the separators. A skill
// that breaks a rule is refused whole, with the reason, and never cleaned
// up into one that doesn't.
func splitSkill(content string) ([]SkillFile, string) {
	parts := strings.Split(content, fileSeparator)
	files := []SkillFile{{Path: skillMain, Content: parts[0]}}
	for _, part := range parts[1:] {
		path, text, found := strings.Cut(part, fileHeaderEnd)
		if !found {
			return nil, skillBadPath
		}
		files = append(files, SkillFile{Path: path, Content: strings.TrimPrefix(text, "\n")})
	}
	for index := range files {
		files[index].Content = plain(files[index].Content)
	}
	if files[0].Content == "" {
		return nil, notASkill
	}
	if err := checkFiles(files); err != nil {
		return nil, err.reason
	}
	return files, ""
}

// skillRefusal is a skill that breaks a rule: reason is the draft's
// sentence for it.
type skillRefusal struct{ reason string }

// checkFiles holds a skill's files to every rule, in the order the draft
// names them: the caps, then the paths, then text only. Content is
// already plain.
func checkFiles(files []SkillFile) *skillRefusal {
	total := 0
	for _, file := range files {
		total += len(file.Content)
	}
	if len(files) > maxSkillFiles || total > maxSkillBytes {
		return &skillRefusal{skillTooBig}
	}
	seen := map[string]bool{}
	for index, file := range files {
		if !skillPath(file.Path) || seen[file.Path] || (index == 0) != (file.Path == skillMain) {
			return &skillRefusal{skillBadPath}
		}
		seen[file.Path] = true
	}
	for _, file := range files {
		if !strings.HasSuffix(file.Path, ".md") && !strings.HasSuffix(file.Path, ".txt") {
			return &skillRefusal{skillNotText}
		}
	}
	return nil
}

// skillPath reports whether path is one the hub writes under a skill's
// folder as given: relative, with / between segments, none of them empty,
// "." or "..", and no backslash, NUL or character the text rules drop.
func skillPath(path string) bool {
	if path == "" || len(path) > maxSkillPath || !utf8.ValidString(path) ||
		strings.ContainsAny(path, "\\\x00") || plain(path) != path || strings.TrimSpace(path) != path {
		return false
	}
	for _, segment := range strings.Split(path, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

// CheckSkills holds the skills a create was sent to every rule a draft's
// were held to, since create installs what it is given without a fetch
// (#1364): at most maxBotSkills, each folder a name the hub would make and
// named once, SKILL.md first, every path relative with no .. and no
// repeat, text files only, within the caps, and every file's content
// already through the text rules. The error names the rule, and the folder
// it broke on, never a file's text.
func CheckSkills(skills []Skill) error {
	if len(skills) > maxBotSkills {
		return fmt.Errorf("a loop takes at most %d skills", maxBotSkills)
	}
	dirs := map[string]bool{}
	for _, item := range skills {
		if item.Dir == "" || skillDir(item.Dir) != item.Dir {
			return errors.New("a skill's dir is no folder name the hub makes")
		}
		if dirs[item.Dir] {
			return fmt.Errorf("skill %s is named twice", item.Dir)
		}
		dirs[item.Dir] = true
		if len(item.Files) == 0 || item.Files[0].Content == "" {
			return fmt.Errorf("skill %s has no %s", item.Dir, skillMain)
		}
		for _, file := range item.Files {
			if plain(file.Content) != file.Content {
				return fmt.Errorf("skill %s: a file holds characters the text rules drop", item.Dir)
			}
		}
		if refusal := checkFiles(item.Files); refusal != nil {
			return fmt.Errorf("skill %s: %s", item.Dir, refusal.reason)
		}
	}
	return nil
}
