package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/enes-alatas/spool/internal/store"
)

// claudeLogins keeps each Claude login's sign-in as a store.CloudAccount
// in JSON, sealed under the hub key (ADR-0046), and "" while it is signed
// out (ADR-0060).
type claudeLogins struct {
	db     *sql.DB
	sealer *box
}

const claudeLoginColumns = `name, account, owner_user_id, created_at, check_record, plan_usage, plan_cap_resumed_until`

func (table claudeLogins) List(ctx context.Context) ([]*store.ClaudeLogin, error) {
	rows, err := table.db.QueryContext(ctx, `SELECT `+claudeLoginColumns+` FROM claude_logins ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*store.ClaudeLogin
	for rows.Next() {
		login, err := table.scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, login)
	}
	return out, rows.Err()
}

func (table claudeLogins) Get(ctx context.Context, name string) (*store.ClaudeLogin, error) {
	login, err := table.scan(table.db.QueryRowContext(ctx, `SELECT `+claudeLoginColumns+` FROM claude_logins WHERE name=?`, name))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	return login, err
}

func (table claudeLogins) Create(ctx context.Context, login *store.ClaudeLogin) error {
	account, err := table.sealAccount(login.Account)
	if err != nil {
		return err
	}
	check, err := checkJSON(login.Check)
	if err != nil {
		return err
	}
	usage, err := planUsageJSON(login.PlanUsage)
	if err != nil {
		return err
	}
	_, err = table.db.ExecContext(ctx, `INSERT INTO claude_logins (`+claudeLoginColumns+`) VALUES (?,?,?,?,?,?,?)`,
		login.Name, account, login.OwnerUserID, login.CreatedAt, check, usage, login.PlanCapResumedUntil)
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return store.ErrDuplicate
	}
	return err
}

func (table claudeLogins) SetAccount(ctx context.Context, name string, account store.CloudAccount) error {
	sealed, err := table.sealAccount(account)
	if err != nil {
		return err
	}
	return table.update(ctx, `UPDATE claude_logins SET account=? WHERE name=?`, sealed, name)
}

func (table claudeLogins) SetCheck(ctx context.Context, name string, check *store.LoginCheckRecord) error {
	raw, err := checkJSON(check)
	if err != nil {
		return err
	}
	return table.update(ctx, `UPDATE claude_logins SET check_record=? WHERE name=?`, raw, name)
}

func (table claudeLogins) SetPlanUsage(ctx context.Context, name string, usage store.PlanUsageRecord) error {
	raw, err := planUsageJSON(usage)
	if err != nil {
		return err
	}
	return table.update(ctx, `UPDATE claude_logins SET plan_usage=? WHERE name=?`, raw, name)
}

func (table claudeLogins) SetPlanCapResumedUntil(ctx context.Context, name string, until int64) error {
	return table.update(ctx, `UPDATE claude_logins SET plan_cap_resumed_until=? WHERE name=?`, until, name)
}

func (table claudeLogins) Delete(ctx context.Context, name string) error {
	return table.update(ctx, `DELETE FROM claude_logins WHERE name=?`, name)
}

// update runs a statement on one login, ErrNotFound when it touched none.
func (table claudeLogins) update(ctx context.Context, query string, args ...any) error {
	result, err := table.db.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	if touched, err := result.RowsAffected(); err != nil {
		return err
	} else if touched == 0 {
		return store.ErrNotFound
	}
	return nil
}

// sealAccount is the account as stored: its JSON sealed, or "" for a
// login signed out.
func (table claudeLogins) sealAccount(account store.CloudAccount) (string, error) {
	if account.Token == "" {
		return "", nil
	}
	raw, err := json.Marshal(account)
	if err != nil {
		return "", err
	}
	return table.sealer.seal(string(raw)), nil
}

func checkJSON(check *store.LoginCheckRecord) (string, error) {
	if check == nil {
		return "", nil
	}
	raw, err := json.Marshal(check)
	return string(raw), err
}

// planUsageJSON is the usage as stored, "" for the zero record: no
// observation yet.
func planUsageJSON(usage store.PlanUsageRecord) (string, error) {
	if usage == (store.PlanUsageRecord{}) {
		return "", nil
	}
	raw, err := json.Marshal(usage)
	return string(raw), err
}

func (table claudeLogins) scan(row interface{ Scan(...any) error }) (*store.ClaudeLogin, error) {
	var login store.ClaudeLogin
	var sealed, check, usage string
	if err := row.Scan(&login.Name, &sealed, &login.OwnerUserID, &login.CreatedAt, &check, &usage, &login.PlanCapResumedUntil); err != nil {
		return nil, err
	}
	opened, err := table.sealer.open(sealed)
	if err != nil {
		return nil, err
	}
	// One login that doesn't read doesn't fail the list, which the hub
	// reads to start and every login refreshes by: it reads as signed out,
	// unchecked.
	if opened != "" && json.Unmarshal([]byte(opened), &login.Account) != nil {
		login.Account, login.Unreadable = store.CloudAccount{}, opened
	}
	if check != "" {
		login.Check = &store.LoginCheckRecord{}
		if json.Unmarshal([]byte(check), login.Check) != nil {
			login.Check = nil
		}
	}
	// usage that doesn't read is usage unknown, which never caps
	if usage != "" && json.Unmarshal([]byte(usage), &login.PlanUsage) != nil {
		login.PlanUsage = store.PlanUsageRecord{}
	}
	return &login, nil
}
