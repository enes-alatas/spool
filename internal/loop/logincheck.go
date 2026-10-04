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
// runtime, when a setup-token is saved and when the operator asks, and keeps
// the last outcome in the store for the onboarding read. Never on a timer:
// each check spends a haiku answer from the operator's plan.
type LoginChecker struct {
	store   store.Store
	runtime runtime.Runtime // the default runtime's, which new loops get
	log     *slog.Logger

	mu      sync.Mutex
	started int64 // the newest check's At; an older one's outcome is not kept
}

// NewLoginChecker returns the checker for a hub whose default runtime is rt.
func NewLoginChecker(st store.Store, rt runtime.Runtime, log *slog.Logger) *LoginChecker {
	return &LoginChecker{store: st, runtime: rt, log: log}
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

// Clear forgets the last check, for a hub whose setup-token was removed:
// there is no login left for its outcome to be about.
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
		if token, err = claudeTokenFrom(ctx, checker.store.Settings()); err != nil || token == "" {
			checker.log.Warn("login check: no setup-token to check", "err", err)
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

// claudeTokenFrom reads the stored setup-token, an unset key as empty.
func claudeTokenFrom(ctx context.Context, settings store.SettingsStore) (string, error) {
	token, err := settings.Get(ctx, store.SettingClaudeOAuthToken)
	if errors.Is(err, store.ErrNotFound) {
		return "", nil
	}
	return token, err
}
