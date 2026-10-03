// Command spool-eval exports recorded loop turns as eval cases and grades
// cases against the fleet rules that can be checked mechanically (#450).
//
//	spool-eval export -out eval/cases         # redacted cases from ~/.spool/spool.db
//	spool-eval grade eval/cases eval/synthetic
//	spool-eval grade -online eval/cases       # also resolve every sent link
//
// Export reads a snapshot of the store, never the live file: the hub may be
// running, and this binary's migrations must not touch its database.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/enes-alatas/spool/internal/eval"
	"github.com/enes-alatas/spool/internal/redact"
	"github.com/enes-alatas/spool/internal/store"
	"github.com/enes-alatas/spool/internal/store/sqlite"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "export":
		err = export(os.Args[2:])
	case "grade":
		err = grade(os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "spool-eval:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: spool-eval export [flags] | spool-eval grade [flags] dir...")
	os.Exit(2)
}

func export(args []string) error {
	flags := flag.NewFlagSet("export", flag.ExitOnError)
	home, _ := os.UserHomeDir()
	dbPath := flags.String("db", filepath.Join(home, ".spool", "spool.db"), "the store to export from")
	out := flags.String("out", "eval/cases", "directory the cases are written to")
	rules := flags.String("rules", "scripts/secret-rules.awk", "the credential shapes")
	perLoop := flags.Int("per-loop", 15, "most recent cases kept per loop")
	since := flags.Duration("since", 7*24*time.Hour, "how far back to read")
	only := flags.String("loops", "", "comma-separated loop names to export; empty is every active loop")
	_ = flags.Parse(args)

	ctx := context.Background()
	shapes, err := eval.LoadShapes(*rules)
	if err != nil {
		return err
	}
	snapshot, err := snapshotStore(*dbPath)
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(filepath.Dir(snapshot)) }()
	db, err := sqlite.Open(snapshot)
	if err != nil {
		return err
	}
	defer db.Close()

	// The store's own redactor removes every secret Spool holds, by value;
	// the shapes then catch one it never held.
	redactor := redact.New(redact.StoreSource{Store: db}, 0)
	if err := redactor.Refresh(ctx); err != nil {
		return err
	}
	clean := func(text string) string { return eval.Scrub(shapes, redactor.Text(text)) }

	loops, err := db.Loops().List(ctx)
	if err != nil {
		return err
	}
	senders, err := db.TGSenders().List(ctx)
	if err != nil {
		return err
	}
	var people []string
	for _, sender := range senders {
		if sender.Status == "allowed" && sender.Username != "" {
			people = append(people, sender.Username)
		}
	}

	if err := os.MkdirAll(*out, 0o700); err != nil {
		return err
	}
	cutoff := time.Now().Add(-*since).Unix()
	written := 0
	for _, loopRecord := range loops {
		if loopRecord.Status == store.StatusArchived || !wanted(*only, loopRecord.Name) {
			continue
		}
		events, err := eventsSince(ctx, db, loopRecord.ID, cutoff)
		if err != nil {
			return err
		}
		recipients := append([]string(nil), people...)
		for _, other := range loops {
			if other.ID != loopRecord.ID && other.Status != store.StatusArchived {
				recipients = append(recipients, other.Name)
			}
		}
		cases := eval.Extract(events, eval.LoopInfo{
			Name:       loopRecord.Name,
			Pacing:     loopRecord.Pacing,
			MinWakeSec: loopRecord.MinWakeSec,
			MaxWakeSec: loopRecord.MaxWakeSec,
			Recipients: recipients,
		}, clean)
		if len(cases) > *perLoop {
			cases = cases[len(cases)-*perLoop:]
		}
		for _, turnCase := range cases {
			if err := writeCase(*out, turnCase); err != nil {
				return err
			}
			written++
		}
	}
	fmt.Printf("wrote %d cases to %s\n", written, *out)
	return nil
}

// wanted reports whether name is in the comma-separated list, an empty
// list wanting every loop.
func wanted(list, name string) bool {
	if list == "" {
		return true
	}
	for _, entry := range strings.Split(list, ",") {
		if strings.TrimSpace(entry) == name {
			return true
		}
	}
	return false
}

// snapshotStore copies the store with VACUUM INTO, which reads a consistent
// view of a live WAL database without writing to it, and returns the copy's
// path in a fresh temporary directory.
func snapshotStore(path string) (string, error) {
	if _, err := os.Stat(path); err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp("", "spool-eval-")
	if err != nil {
		return "", err
	}
	snapshot := filepath.Join(dir, "spool.db")
	live, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return "", err
	}
	defer live.Close()
	if _, err := live.Exec(`VACUUM INTO ?`, snapshot); err != nil {
		_ = os.RemoveAll(dir)
		return "", fmt.Errorf("snapshot %s: %w", path, err)
	}
	return snapshot, nil
}

// eventsSince reads a loop's events from cutoff on, oldest first.
func eventsSince(ctx context.Context, db store.Store, loopID string, cutoff int64) ([]*store.Event, error) {
	var out []*store.Event
	var after int64
	for {
		page, err := db.Events().ListByLoop(ctx, loopID, after, 1000)
		if err != nil {
			return nil, err
		}
		if len(page) == 0 {
			return out, nil
		}
		for _, event := range page {
			if event.TS >= cutoff {
				out = append(out, event)
			}
		}
		after = page[len(page)-1].ID
	}
}

func writeCase(dir string, turnCase eval.Case) error {
	raw, err := json.MarshalIndent(turnCase, "", "  ")
	if err != nil {
		return err
	}
	id := turnCase.ID
	if len(id) > 8 {
		id = id[:8]
	}
	return os.WriteFile(filepath.Join(dir, turnCase.Loop+"-"+id+".json"), append(raw, '\n'), 0o600)
}

func grade(args []string) error {
	flags := flag.NewFlagSet("grade", flag.ExitOnError)
	rules := flags.String("rules", "scripts/secret-rules.awk", "the credential shapes")
	online := flags.Bool("online", false, "resolve every sent link (GitHub through gh)")
	signingPath := flags.String("signing", "eval/signing.json", "which artifacts each loop signs")
	_ = flags.Parse(args)
	if flags.NArg() == 0 {
		usage()
	}

	shapes, err := eval.LoadShapes(*rules)
	if err != nil {
		return err
	}
	var signing eval.Signing
	raw, err := os.ReadFile(*signingPath)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, &signing); err != nil {
		return fmt.Errorf("%s: %w", *signingPath, err)
	}
	var cases []eval.Case
	for _, dir := range flags.Args() {
		loaded, err := loadCases(dir)
		if err != nil {
			return err
		}
		cases = append(cases, loaded...)
	}
	var resolver eval.Resolver
	if *online {
		resolver = &linkResolver{seen: map[string]bool{}, client: &http.Client{Timeout: 10 * time.Second}}
	}
	eval.Run(cases, eval.Graders(shapes, signing, resolver)).Write(os.Stdout)
	return nil
}

func loadCases(dir string) ([]eval.Case, error) {
	var cases []eval.Case
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || filepath.Ext(path) != ".json" {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var turnCase eval.Case
		if err := json.Unmarshal(raw, &turnCase); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		cases = append(cases, turnCase)
		return nil
	})
	return cases, err
}

// githubLink splits a link to an issue or PR, and the anchor on it.
var githubLink = regexp.MustCompile(`^https://github\.com/([^/]+/[^/]+)/(issues|pull)/(\d+)(?:/files)?(?:#(issuecomment|pullrequestreview|discussion_r)-?(\d+))?$`)

// linkResolver checks GitHub links through the API, anchor included —
// github.com answers 200 for any anchor, which is how a guessed one slips
// through — and every other link with a HEAD.
type linkResolver struct {
	seen   map[string]bool
	client *http.Client
}

func (resolver *linkResolver) Resolves(url string) (bool, error) {
	if ok, cached := resolver.seen[url]; cached {
		return ok, nil
	}
	ok, err := resolver.resolve(url)
	if err == nil {
		resolver.seen[url] = ok
	}
	return ok, err
}

func (resolver *linkResolver) resolve(url string) (bool, error) {
	if match := githubLink.FindStringSubmatch(url); match != nil {
		repo, number, anchor, id := match[1], match[3], match[4], match[5]
		path := "repos/" + repo + "/issues/" + number
		switch anchor {
		case "issuecomment":
			path = "repos/" + repo + "/issues/comments/" + id
		case "pullrequestreview":
			path = "repos/" + repo + "/pulls/" + number + "/reviews/" + id
		case "discussion_r":
			path = "repos/" + repo + "/pulls/comments/" + id
		}
		output, err := exec.Command("gh", "api", path, "--silent").CombinedOutput()
		if err != nil {
			var exit *exec.ExitError
			if errors.As(err, &exit) && strings.Contains(string(output), "404") {
				return false, nil
			}
			return false, fmt.Errorf("gh api %s: %s", path, strings.TrimSpace(string(output)))
		}
		return true, nil
	}
	response, err := resolver.client.Head(url)
	if err != nil {
		return false, err
	}
	response.Body.Close()
	return response.StatusCode < 400, nil
}
