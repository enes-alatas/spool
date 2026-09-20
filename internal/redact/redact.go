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
// that survives only in an escaped or re-encoded form goes through. Both
// limits are the reason this is a floor, not a guarantee.
package redact

import (
	"context"
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
func (r *Redactor) Refresh(ctx context.Context) error {
	r.refreshing.Lock()
	defer r.refreshing.Unlock()
	return r.reload(ctx)
}

func (r *Redactor) reload(ctx context.Context) error {
	secrets, err := r.src.Secrets(ctx)
	if err != nil {
		// Keep the previous snapshot: a store hiccup must not quietly turn
		// redaction off. It goes stale, which the ttl cannot fix either —
		// staleness is recoverable, an empty replacer is a leak.
		return err
	}
	r.snap.Store(compile(secrets, r.now()))
	return nil
}

func compile(secrets []Secret, at time.Time) *snapshot {
	usable := make([]Secret, 0, len(secrets))
	for _, s := range secrets {
		if len(s.Value) >= MinLength {
			usable = append(usable, s)
		}
	}
	if len(usable) == 0 {
		return &snapshot{empty: true, loadedAt: at}
	}
	sort.SliceStable(usable, func(i, j int) bool {
		return len(usable[i].Value) > len(usable[j].Value)
	})
	pairs := make([]string, 0, 2*len(usable))
	for _, s := range usable {
		pairs = append(pairs, s.Value, "<redacted:"+s.Name+">")
	}
	return &snapshot{replacer: strings.NewReplacer(pairs...), loadedAt: at}
}

// Text returns s with every known secret value replaced. It never blocks on
// the store: a stale snapshot redacts what it knows, and the ttl decides when
// the next caller reloads.
func (r *Redactor) Text(s string) string {
	if s == "" {
		return s
	}
	snap := r.current()
	if snap == nil || snap.empty {
		return s
	}
	return snap.replacer.Replace(s)
}

// Redacted reports whether s contains a known secret value.
func (r *Redactor) Redacted(s string) bool { return r.Text(s) != s }

// current returns the snapshot to redact against, reloading first if the ttl
// has passed. The caller that finds the snapshot expired pays for the reload,
// which makes the staleness bound a real bound rather than a scheduling hope
// — but only if it can take the lock uncontended. It must not wait: the store
// query behind a reload logs on failure, and a log line waiting on the
// reload it is part of would deadlock. A caller that cannot take the lock
// redacts against the snapshot it has, which is the safe direction.
func (r *Redactor) current() *snapshot {
	snap := r.snap.Load()
	if snap == nil {
		return nil
	}
	if r.ttl <= 0 || r.now().Sub(snap.loadedAt) < r.ttl {
		return snap
	}
	if !r.refreshing.TryLock() {
		return snap
	}
	defer r.refreshing.Unlock()
	// A reload may have landed between the load above and the lock.
	if snap := r.snap.Load(); r.now().Sub(snap.loadedAt) < r.ttl {
		return snap
	}
	if err := r.reload(context.Background()); err != nil {
		return snap
	}
	return r.snap.Load()
}
