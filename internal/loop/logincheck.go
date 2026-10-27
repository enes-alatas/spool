package loop

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/enes-alatas/spool/internal/runtime"
	"github.com/enes-alatas/spool/internal/store"
)

// loginCheckTimeout bounds one check: a cold container start, the proxy, and
// one short haiku answer.
const loginCheckTimeout = 90 * time.Second

// LoginChecker runs the hub's login check (ADR-0044) on the default
// runtime, when the hub's Claude token changes and when the operator asks,
// and keeps the last outcome in the store for the onboarding read. Never
// on a timer: each check spends a haiku answer from the operator's plan.
type LoginChecker struct {
	store   store.Store
	runtime runtime.Runtime // the default runtime's, which new loops get
	log     *slog.Logger
	// Contained checks a Claude login other than the default: one serves
	// only contained loops (ADR-0060), so it is checked as one of them
	// would run. Nil on a hub with no docker runtime, where such a check
	// ends inconclusive.
	Contained runtime.Runtime

	mu      sync.Mutex
	started int64 // the newest check's At; an older one's outcome is not kept
	// startedFor is started for each login but the default
	startedFor map[string]int64
}

// NewLoginChecker returns the checker for a hub whose default runtime is rt.
func NewLoginChecker(st store.Store, rt runtime.Runtime, log *slog.Logger) *LoginChecker {
	return &LoginChecker{store: st, runtime: rt, log: log, startedFor: map[string]int64{}}
}

// Start records a pending check and runs it in the background. A check
// started while another runs supersedes it: the login may have changed in
// between, so only the newest outcome is kept. The pending record is stored
// before Start returns, so a read right after it never shows an outcome the
// new check has not had.
func (checker *LoginChecker) Start(ctx context.Context) error {
	checker.mu.Lock()
	at := max(time.Now().UnixMilli(), checker.started+1)
	checker.started = at
	checker.mu.Unlock()
	if err := checker.keep(ctx, at, store.LoginCheckRecord{Status: store.LoginCheckPending, At: at}); err != nil {
		return err
	}
	go checker.run(at)
	return nil
}

// Clear forgets the last check, for a hub left with no Claude token: there
// is no login left for its outcome to be about.
func (checker *LoginChecker) Clear(ctx context.Context) error {
	checker.mu.Lock()
	checker.started = max(time.Now().UnixMilli(), checker.started+1)
	checker.mu.Unlock()
	return checker.store.Settings().Set(ctx, store.SettingLoginCheck, "")
}

// Last returns the last check's record, the zero record when none ran.
func (checker *LoginChecker) Last(ctx context.Context) (store.LoginCheckRecord, error) {
	return LastLoginCheck(ctx, checker.store.Settings())
}

// LastLoginCheck reads the stored record, the zero record when there is none.
// A check still pending past loginCheckTimeout reads inconclusive: its hub
// stopped before it finished, and the record outlives the hub, so nothing
// else would ever end it. A live check that overran ends inconclusive too.
func LastLoginCheck(ctx context.Context, settings store.SettingsStore) (store.LoginCheckRecord, error) {
	var record store.LoginCheckRecord
	raw, err := settings.Get(ctx, store.SettingLoginCheck)
	if errors.Is(err, store.ErrNotFound) || (err == nil && raw == "") {
		return record, nil
	}
	if err != nil {
		return record, err
	}
	if err := json.Unmarshal([]byte(raw), &record); err != nil {
		return record, err
	}
	if record.Status == store.LoginCheckPending && time.Since(time.UnixMilli(record.At)) > loginCheckTimeout {
		record.Status = store.LoginCheckInconclusive
	}
	return record, nil
}

func (checker *LoginChecker) run(at int64) {
	ctx, cancel := context.WithTimeout(context.Background(), loginCheckTimeout)
	defer cancel()
	token := ""
	if needsClaudeToken(checker.runtime) {
		var err error
		if token, err = HubClaudeToken(ctx, checker.store); err != nil || token == "" {
			checker.log.Warn("login check: no Claude token to check", "err", err)
			checker.finish(at, store.LoginCheckRecord{Status: store.LoginCheckInconclusive, At: at})
			return
		}
	}
	result, err := checker.runtime.CheckLogin(ctx, token)
	switch {
	case err != nil:
		// the reason stays in the log: a run's stderr is no place to read
		// back to a browser
		checker.log.Warn("login check proved nothing either way", "err", err)
		checker.finish(at, store.LoginCheckRecord{Status: store.LoginCheckInconclusive, At: at})
	case result.OK:
		checker.log.Info("login check: the API accepted the login")
		checker.finish(at, store.LoginCheckRecord{Status: store.LoginCheckOK, At: at})
	default:
		checker.log.Warn("login check: the API refused the login", "refusal", result.Refusal)
		checker.finish(at, store.LoginCheckRecord{Status: store.LoginCheckRefused, Refusal: result.Refusal, At: at})
	}
}

// finish keeps a check's outcome unless a newer check has started since.
func (checker *LoginChecker) finish(at int64, record store.LoginCheckRecord) {
	if err := checker.keep(context.Background(), at, record); err != nil {
		checker.log.Error("store login check", "err", err)
	}
}

func (checker *LoginChecker) keep(ctx context.Context, at int64, record store.LoginCheckRecord) error {
	checker.mu.Lock()
	defer checker.mu.Unlock()
	if at != checker.started {
		return nil
	}
	raw, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return checker.store.Settings().Set(ctx, store.SettingLoginCheck, string(raw))
}

// HubClaudeToken reads the hub's Claude token, the one contained loops
// run on: the default Claude login's access token, else the stored
// setup-token, else "" (ADR-0058, ADR-0060). A login that can't be read
// is an error rather than a fall back to the setup-token, which the
// sign-in superseded.
func HubClaudeToken(ctx context.Context, st store.Store) (string, error) {
	account, signedIn, err := store.SignedInAccount(ctx, st.ClaudeLogins(), store.DefaultClaudeLogin)
	if err != nil {
		return "", err
	}
	if signedIn {
		return account.Token, nil
	}
	token, err := st.Settings().Get(ctx, store.SettingClaudeOAuthToken)
	if errors.Is(err, store.ErrNotFound) {
		return "", nil
	}
	return token, err
}

// ClaudeTokenFor reads the token of the Claude login a contained loop
// names, login "" for the default: the default's is HubClaudeToken's, and
// another's is its access token, or "" while it is signed out or gone
// (ADR-0060). A loop on another login never falls back to the default's:
// it would spend a subscription its loop wasn't put on.
func ClaudeTokenFor(ctx context.Context, st store.Store, login string) (string, error) {
	if login == "" || login == store.DefaultClaudeLogin {
		return HubClaudeToken(ctx, st)
	}
	account, _, err := store.SignedInAccount(ctx, st.ClaudeLogins(), login)
	return account.Token, err
}

// StartLogin checks the Claude login called name, as Start checks the
// hub's token, and keeps the outcome on the login (ADR-0060). The default
// login's check is Start's, kept where onboarding reads it.
func (checker *LoginChecker) StartLogin(ctx context.Context, name string) error {
	if name == store.DefaultClaudeLogin {
		return checker.Start(ctx)
	}
	checker.mu.Lock()
	at := max(time.Now().UnixMilli(), checker.startedFor[name]+1)
	checker.startedFor[name] = at
	checker.mu.Unlock()
	if err := checker.keepFor(ctx, name, at, store.LoginCheckRecord{Status: store.LoginCheckPending, At: at}); err != nil {
		return err
	}
	go checker.runLogin(name, at)
	return nil
}

func (checker *LoginChecker) runLogin(name string, at int64) {
	ctx, cancel := context.WithTimeout(context.Background(), loginCheckTimeout)
	defer cancel()
	record := store.LoginCheckRecord{Status: store.LoginCheckInconclusive, At: at}
	account, signedIn, err := store.SignedInAccount(ctx, checker.store.ClaudeLogins(), name)
	switch {
	case err != nil || !signedIn:
		checker.log.Warn("login check: the Claude login holds no sign-in to check", "login", name, "err", err)
	case checker.Contained == nil:
		checker.log.Warn("login check: no docker runtime to check the Claude login with", "login", name)
	default:
		result, err := checker.Contained.CheckLogin(ctx, account.Token)
		switch {
		case err != nil:
			checker.log.Warn("login check proved nothing either way", "login", name, "err", err)
		case result.OK:
			checker.log.Info("login check: the API accepted the login", "login", name)
			record.Status = store.LoginCheckOK
		default:
			checker.log.Warn("login check: the API refused the login", "login", name, "refusal", result.Refusal)
			record.Status, record.Refusal = store.LoginCheckRefused, result.Refusal
		}
	}
	if err := checker.keepFor(context.Background(), name, at, record); err != nil && !errors.Is(err, store.ErrNotFound) {
		checker.log.Error("store login check", "login", name, "err", err)
	}
}

// keepFor keeps a check of the login called name unless a newer one has
// started since.
func (checker *LoginChecker) keepFor(ctx context.Context, name string, at int64, record store.LoginCheckRecord) error {
	checker.mu.Lock()
	defer checker.mu.Unlock()
	if at != checker.startedFor[name] {
		return nil
	}
	return checker.store.ClaudeLogins().SetCheck(ctx, name, &record)
}

// SettledCheck is record as it reads now: a check still pending past
// loginCheckTimeout reads inconclusive, as LastLoginCheck reads the
// default's.
func SettledCheck(record *store.LoginCheckRecord) *store.LoginCheckRecord {
	if record == nil || record.Status != store.LoginCheckPending || time.Since(time.UnixMilli(record.At)) <= loginCheckTimeout {
		return record
	}
	settled := *record
	settled.Status = store.LoginCheckInconclusive
	return &settled
}
