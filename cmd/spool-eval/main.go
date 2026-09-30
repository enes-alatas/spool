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
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"time"

	_ "modernc.org/sqlite"

	"github.com/enes-alatas/spool/internal/eval"
	"github.com/enes-alatas/spool/internal/operator"
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
	keyPath := flags.String("key", "", "the hub key the store is sealed under; empty is "+sqlite.KeyFile+" beside -db")
	tokenPath := flags.String("operator-token", "", "the hub's operator token, masked in every case; empty is "+operator.TokenFile+" beside -db")
	only := flags.String("loops", "", "comma-separated loop names to export; empty is every active loop")
	_ = flags.Parse(args)

	ctx := context.Background()
	shapes, err := eval.LoadShapes(*rules)
	if err != nil {
		return err
	}
	if *keyPath == "" {
		*keyPath = filepath.Join(filepath.Dir(*dbPath), sqlite.KeyFile)
	}
	if *tokenPath == "" {
		*tokenPath = filepath.Join(filepath.Dir(*dbPath), operator.TokenFile)
	}
	snapshot, err := snapshotStore(*dbPath, *keyPath)
	if err != nil {
		return err
	}
	// The snapshot holds every secret the hub does, sealed, so it goes
	// when the export does: at the end, or on an interrupt. Only a kill
	// leaves it behind, which is why the README says to sweep after one.
	removeSnapshot := func() { _ = os.RemoveAll(filepath.Dir(snapshot)) }
	defer removeSnapshot()
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(interrupts)
	go func() {
		if _, ok := <-interrupts; ok {
			removeSnapshot()
			os.Exit(1)
		}
	}()
	db, err := sqlite.Open(snapshot)
	if err != nil {
		return fmt.Errorf("open the snapshot with the key at %s (-key): %w", *keyPath, err)
	}
	defer db.Close()

	// The redactor removes what the hub's does (cmd/spool): every secret
	// the store holds, the hub key as bytes and as the hex its file holds,
	// and the operator token, both files a bare loop can read (#643). The
	// shapes then catch one Spool never held. The github-app tokens the
	// hub mints live only in its memory; they were masked as each turn was
	// stored.
	hubKey := db.HubKey()
	fixed := redact.Fixed{
		{Name: "hub_key", Value: string(hubKey)},
		{Name: "hub_key", Value: hex.EncodeToString(hubKey)},
	}
	token, err := readOperatorToken(*tokenPath)
	if err != nil {
		return err
	}
	if token != "" {
		fixed = append(fixed, redact.Secret{Name: "operator_token", Value: token})
	}
	redactor := redact.New(redact.Sources{redact.StoreSource{Store: db}, fixed}, 0)
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

	channels, err := db.Channels().List(ctx)
	if err != nil {
		return err
	}
	names := map[string]string{}
	for _, loopRecord := range loops {
		if loopRecord.Status != store.StatusArchived {
			names[loopRecord.ID] = loopRecord.Name
		}
	}

	if err := os.MkdirAll(*out, 0o700); err != nil {
		return err
	}
	cutoff := time.Now().Add(-*since).UnixMilli()
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
			Name:         loopRecord.Name,
			Pacing:       loopRecord.Pacing,
			MinWakeSec:   loopRecord.MinWakeSec,
			MaxWakeSec:   loopRecord.MaxWakeSec,
			Recipients:   recipients,
			ChannelLoops: channelLoops(channels, names, loopRecord.ID),
			People:       people,
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
func snapshotStore(path, keyPath string) (string, error) {
	if _, err := os.Stat(path); err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp("", "spool-eval-")
	if err != nil {
		return "", err
	}
	snapshot := filepath.Join(dir, "spool.db")
	// The store reads its key beside the database, so the snapshot gets a
	// link to the live key rather than a copy of it. A store with no key
	// yet seals nothing, and opens without one.
	if key, err := filepath.Abs(keyPath); err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	} else if _, err := os.Stat(key); err == nil {
		if err := os.Symlink(key, filepath.Join(dir, sqlite.KeyFile)); err != nil {
			_ = os.RemoveAll(dir)
			return "", err
		}
	}
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

// readOperatorToken reads the operator token's file, "" when there is
// none. It never mints one, as the hub would: the export writes nothing
// beside the live store.
func readOperatorToken(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("operator token: %w", err)
	}
	return strings.TrimSpace(string(raw)), nil
}

// channelLoops is, for each channel loopID is in, the names of the other
// loops in it.
func channelLoops(channels []*store.Channel, names map[string]string, loopID string) map[string][]string {
	out := map[string][]string{}
	for _, channel := range channels {
		if !slices.Contains(channel.LoopIDs, loopID) {
			continue
		}
		others := []string{}
		for _, id := range channel.LoopIDs {
			if name, ok := names[id]; ok && id != loopID {
				others = append(others, name)
			}
		}
		out[channel.Name] = others
	}
	return out
}

// eventsSince reads a loop's events from cutoff on (Unix milliseconds,
// as events are stamped), oldest first.
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
