package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Log scopes of portal users. A user's own value may also be empty, which
// follows the global setting.
const (
	LogScopeAll  = "all"
	LogScopeSelf = "self"
)

// Keys of app_settings the two processes read on their own.
const (
	SettingPortalName      = "portal_name"
	SettingAdminName       = "admin_name"
	settingLogScope        = "log_scope"
	settingPortalPublicURL = "portal_public_url"
	settingKeyProbe        = "key_probe" // "on" or "off"
)

// MaxLogoBytes is what app_logo's check allows.
const MaxLogoBytes = 256 << 10

// Settings is the row set of app_settings a super edits.
type Settings struct {
	LogScope        string `json:"log_scope"`
	PortalName      string `json:"portal_name"`
	AdminName       string `json:"admin_name"`
	PortalPublicURL string `json:"portal_public_url"`
	// KeyProbe lets the channel pages test multi-key channels' keys and query
	// their balances, which reads the keys.
	KeyProbe bool `json:"key_probe"`
}

func (x *Settings) fields() map[string]*string {
	return map[string]*string{settingLogScope: &x.LogScope, SettingPortalName: &x.PortalName,
		SettingAdminName: &x.AdminName, settingPortalPublicURL: &x.PortalPublicURL}
}

// Setting reads one setting. A missing row is an error: schema.sql seeds them
// all, so its absence is damage and not a value.
func (s *Store) Setting(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key = $1`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("app_settings: %s is missing", key)
	}
	return v, err
}

func (s *Store) PortalPublicURL(ctx context.Context) (string, error) {
	return s.Setting(ctx, settingPortalPublicURL)
}

func (s *Store) Settings(ctx context.Context) (Settings, error) {
	var out Settings
	for key, dst := range out.fields() {
		v, err := s.Setting(ctx, key)
		if err != nil {
			return out, err
		}
		*dst = v
	}
	var err error
	out.KeyProbe, err = s.KeyProbe(ctx)
	return out, err
}

// KeyProbe reads the key_probe switch. Anything but on or off is an error,
// never read as on.
func (s *Store) KeyProbe(ctx context.Context) (bool, error) {
	v, err := s.Setting(ctx, settingKeyProbe)
	if err != nil {
		return false, err
	}
	if v != "on" && v != "off" {
		return false, fmt.Errorf("app_settings: key_probe is %q", v)
	}
	return v == "on", nil
}

// SaveSettings writes all of x or nothing.
func (s *Store) SaveSettings(ctx context.Context, x Settings) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	values := map[string]string{settingKeyProbe: "off"}
	if x.KeyProbe {
		values[settingKeyProbe] = "on"
	}
	for key, v := range x.fields() {
		values[key] = *v
	}
	for key, v := range values {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO app_settings (key, value) VALUES ($1, $2)
			ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`, key, v); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// EffectiveLogScope is what the portal user may read: their own value, or else
// the global one. Anything but a known value is an error, never read as all.
func (s *Store) EffectiveLogScope(ctx context.Context, userID int64) (string, error) {
	var scope sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(NULLIF(u.log_scope, ''), (SELECT value FROM app_settings WHERE key = 'log_scope'))
		FROM portal_users u WHERE u.id = $1`, userID).Scan(&scope)
	if err != nil {
		return "", notFound(err)
	}
	if scope.String != LogScopeAll && scope.String != LogScopeSelf {
		return "", fmt.Errorf("app_settings: log_scope for user %d is %q", userID, scope.String)
	}
	return scope.String, nil
}

// SetPortalLogScope sets the user's own value and returns the previous one.
func (s *Store) SetPortalLogScope(ctx context.Context, id int64, scope string) (string, error) {
	var prev string
	err := s.db.QueryRowContext(ctx, `
		UPDATE portal_users n SET log_scope = $2
		FROM portal_users o WHERE n.id = o.id AND n.id = $1
		RETURNING o.log_scope`, id, scope).Scan(&prev)
	return prev, notFound(err)
}

// Logo is the uploaded logo. Version changes with every upload.
type Logo struct {
	ContentType string
	Data        []byte
	Version     int64 // Unix milliseconds of the upload
}

const logoVersion = `(EXTRACT(EPOCH FROM updated_at) * 1000)::BIGINT`

// LogoVersion is ErrNotFound while the default logo is in use.
func (s *Store) LogoVersion(ctx context.Context) (int64, error) {
	var v int64
	err := s.db.QueryRowContext(ctx, `SELECT `+logoVersion+` FROM app_logo`).Scan(&v)
	return v, notFound(err)
}

// Logo is ErrNotFound while the default logo is in use.
func (s *Store) Logo(ctx context.Context) (Logo, error) {
	var l Logo
	err := s.db.QueryRowContext(ctx, `SELECT content_type, data, `+logoVersion+` FROM app_logo`).
		Scan(&l.ContentType, &l.Data, &l.Version)
	return l, notFound(err)
}

func (s *Store) SaveLogo(ctx context.Context, contentType string, data []byte) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO app_logo (content_type, data) VALUES ($1, $2)
		ON CONFLICT (id) DO UPDATE SET content_type = EXCLUDED.content_type, data = EXCLUDED.data, updated_at = now()`,
		contentType, data)
	return err
}

// DeleteLogo returns to the default logo; having none already is fine.
func (s *Store) DeleteLogo(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM app_logo`)
	return err
}

// Settlement says the user was settled up to AmountUSD of what they ran.
type Settlement struct {
	ID        int64   `json:"id"`
	AmountUSD float64 `json:"amount_usd"`
	Note      string  `json:"note"`
	CreatedBy string  `json:"created_by,omitempty"` // an admin's login name: not for the portal
	CreatedAt int64   `json:"created_at"`
}

const settlementCols = `id, amount_usd, note, created_by, EXTRACT(EPOCH FROM created_at)::BIGINT`

func scanSettlement(r scanner) (Settlement, error) {
	var x Settlement
	err := r.Scan(&x.ID, &x.AmountUSD, &x.Note, &x.CreatedBy, &x.CreatedAt)
	return x, err
}

func (s *Store) AddSettlement(ctx context.Context, userID int64, amountUSD float64, note, by string) (Settlement, error) {
	return scanSettlement(s.db.QueryRowContext(ctx, `
		INSERT INTO settlements (user_id, amount_usd, note, created_by) VALUES ($1, $2, $3, $4)
		RETURNING `+settlementCols, userID, amountUSD, note, by))
}

// Settlements lists the user's records, newest first.
func (s *Store) Settlements(ctx context.Context, userID int64) ([]Settlement, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+settlementCols+` FROM settlements WHERE user_id = $1 ORDER BY id DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Settlement{}
	for rows.Next() {
		x, err := scanSettlement(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}
