// Package redact keeps the secret values Spool holds out of everything Spool
// writes down: log lines, stored transcripts and API responses.
//
// The rule "secrets never in code, logs, or API responses" used to be a thing
// every call site had to remember, and #146 showed what forgetting costs — a
// transport error string carried a bot token into the log. So redaction is
// one component the values pass through rather than a habit: a log handler
// (Handler), a store decorator (Store) and the API's JSON writer each ask the
// same Redactor, and a new call site inherits it by construction.
//
// What it catches is the secrets Spool itself holds — the operator's Claude
// token, each loop's bot token, hub MCP token and per-loop secrets. It cannot
// catch a credential a loop invents or reads from somewhere Spool has never
// seen (#30 is that problem), and it matches values literally, so a value
// that survives only in an escaped or re-encoded form goes through, beyond
// the few encodings it matches as well (base64, hex, percent-encoding).
// Both limits are the reason this is a floor, not a guarantee.
package redact

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Secret is one value to keep out of the record, and the name that stands in
// for it. Names need not be unique: two loops may both hold a GH_TOKEN, and
// the placeholder says which kind of thing was removed, not which row.
type Secret struct {
	Name  string
	Value string
}

// Source supplies every secret Spool currently holds. The redactor reads it
// rather than being told, because secrets change while the process runs and a
// redactor working from a value it was handed at startup silently stops
// covering the ones added since.
type Source interface {
	Secrets(ctx context.Context) ([]Secret, error)
}

// MinLength is the shortest value the redactor will act on. A one- or
// two-character secret would match inside ordinary words and turn every
// transcript into placeholders, which reads as a bug and gets the redactor
// switched off — a worse outcome than not covering a credential that short.
// Nothing Spool mints is anywhere near this bound.
const MinLength = 8

// Redactor replaces known secret values with <redacted:name>. It is safe for
// concurrent use, and its hot path — Text, on every log line — reads an
// immutable snapshot without locking.
type Redactor struct {
	src Source
	ttl time.Duration
	now func() time.Time

	snap atomic.Pointer[snapshot]

	// refreshing serializes reloads so a burst of callers makes one query.
	refreshing sync.Mutex
}

type snapshot struct {
	// replacer is built longest value first, so a secret that contains
	// another is replaced whole rather than left with a placeholder
	// embedded in it. nil when empty.
	replacer *strings.Replacer
	empty    bool
	loadedAt time.Time
}

// New returns a redactor over src. Until the first successful Refresh it
// redacts nothing, so callers load once during wiring, before the store is
// handed to anything that writes.
//
// ttl bounds how long a secret written while the process runs can stay
// uncovered: refresh is explicit at the write sites, and the ttl is what
// catches the site that forgets. Zero means never expire, for tests that
// drive Refresh themselves.
func New(src Source, ttl time.Duration) *Redactor {
	return &Redactor{src: src, ttl: ttl, now: time.Now}
}

// Refresh reloads the secrets. Call it after writing one, so the very next
// log line covers it rather than waiting out the ttl.
func (redactor *Redactor) Refresh(ctx context.Context) error {
	redactor.refreshing.Lock()
	defer redactor.refreshing.Unlock()
	return redactor.reload(ctx)
}

func (redactor *Redactor) reload(ctx context.Context) error {
	secrets, err := redactor.src.Secrets(ctx)
	if err != nil {
		// Keep the previous snapshot: a store hiccup must not quietly turn
		// redaction off. It goes stale, which the ttl cannot fix either —
		// staleness is recoverable, an empty replacer is a leak.
		return err
	}
	redactor.snap.Store(compile(secrets, redactor.now()))
	return nil
}

func compile(secrets []Secret, at time.Time) *snapshot {
	usable := make([]Secret, 0, len(secrets))
	for _, secret := range secrets {
		if len(secret.Value) >= MinLength {
			usable = append(usable, secret)
		}
	}
	if len(usable) == 0 {
		return &snapshot{empty: true, loadedAt: at}
	}
	// Each secret stands for its encoded forms too, so they are matched
	// alongside it: a value a loop base64s or hex-dumps is the same
	// credential, and it is the first thing a steered loop tries (#30).
	var forms []Secret
	for _, secret := range usable {
		for _, form := range encodings(secret.Value) {
			forms = append(forms, Secret{Name: secret.Name, Value: form})
		}
	}
	sort.SliceStable(forms, func(i, j int) bool {
		return len(forms[i].Value) > len(forms[j].Value)
	})
	pairs := make([]string, 0, 2*len(forms))
	for _, form := range forms {
		pairs = append(pairs, form.Value, "<redacted:"+form.Name+">")
	}
	return &snapshot{replacer: strings.NewReplacer(pairs...), loadedAt: at}
}

// encodings is value and the forms it takes when a loop encodes it the
// usual ways: base64 (standard and URL alphabets), hex in either case, and
// percent-encoding. Base64 is matched at each of its three alignments, by
// the characters that depend on value's bytes alone, so the value is caught
// inside a longer encoded text too, as in an HTTP Basic header. A form
// shorter than MinLength is left out, as a short value is.
func encodings(value string) []string {
	hexed := hex.EncodeToString([]byte(value))
	forms := []string{value, hexed, strings.ToUpper(hexed), url.QueryEscape(value), url.PathEscape(value)}
	for offset := range 3 {
		core := base64Core(value, offset)
		forms = append(forms, core, strings.NewReplacer("+", "-", "/", "_").Replace(core))
	}
	seen := make(map[string]bool, len(forms))
	unique := forms[:0]
	for _, form := range forms {
		if len(form) >= MinLength && !seen[form] {
			seen[form] = true
			unique = append(unique, form)
		}
	}
	return unique
}

// base64Core is the run of base64 characters that encode value's bytes and
// nothing else, when value starts offset bytes into the encoded text. A
// character carries six bits, so those at the edges also carry a neighbour's
// bits and are dropped.
func base64Core(value string, offset int) string {
	encoded := base64.StdEncoding.EncodeToString(append(make([]byte, offset), value...))
	first := (8*offset + 5) / 6             // the first character whose six bits start inside value
	last := (8 * (offset + len(value))) / 6 // one past the last whose six bits end inside it
	if first >= last || last > len(encoded) {
		return ""
	}
	return encoded[first:last]
}

// Text returns text with every known secret value replaced. It never blocks
// on the store: a stale snapshot redacts what it knows, and the ttl decides
// when the next caller reloads.
func (redactor *Redactor) Text(text string) string {
	if text == "" {
		return text
	}
	snap := redactor.current()
	if snap == nil || snap.empty {
		return text
	}
	return snap.replacer.Replace(text)
}

// Redacted reports whether text contains a known secret value.
func (redactor *Redactor) Redacted(text string) bool { return redactor.Text(text) != text }

// current returns the snapshot to redact against, reloading first if the ttl
// has passed. The caller that finds the snapshot expired pays for the reload,
// which makes the staleness bound a real bound rather than a scheduling hope
// — but only if it can take the lock uncontended. It must not wait: the store
// query behind a reload logs on failure, and a log line waiting on the
// reload it is part of would deadlock. A caller that cannot take the lock
// redacts against the snapshot it has, which is the safe direction.
func (redactor *Redactor) current() *snapshot {
	snap := redactor.snap.Load()
	if snap == nil {
		return nil
	}
	if redactor.ttl <= 0 || redactor.now().Sub(snap.loadedAt) < redactor.ttl {
		return snap
	}
	if !redactor.refreshing.TryLock() {
		return snap
	}
	defer redactor.refreshing.Unlock()
	// A reload may have landed between the load above and the lock.
	if snap := redactor.snap.Load(); redactor.now().Sub(snap.loadedAt) < redactor.ttl {
		return snap
	}
	if err := redactor.reload(context.Background()); err != nil {
		return snap
	}
	return redactor.snap.Load()
}
