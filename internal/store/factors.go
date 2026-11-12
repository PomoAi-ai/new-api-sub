package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Kinds of auth_flows.
const (
	FlowLogin        = "login"         // a correct password waiting for its second step
	FlowPasskeyLogin = "passkey_login" // the challenge of an anonymous passkey login
	FlowPasskeyProof = "passkey_proof" // the challenge of a passkey re-verification
	FlowTOTPSetup    = "totp_setup"    // a TOTP secret waiting for its first code
	FlowPasskeySetup = "passkey_setup" // the challenge of a passkey registration
	FlowProof        = "proof"         // a re-verification, good for one purpose
	FlowCaptcha      = "captcha"       // the HMAC of a captcha's answer under its id
)

// Kinds of auth_factors.
const (
	FactorTOTP    = "totp"
	FactorPasskey = "passkey"
	FactorBackup  = "backup"
)

// shortcut: anonymous flows (passkey logins, captchas) are capped by row count per system and kind, so a flood from many addresses blocks that kind for up to 2 minutes, add a per-network limit if that is ever seen.
const maxAnonymousFlows = 10000

// Flow is one row of auth_flows. The token itself is only ever with the client.
type Flow struct {
	Kind      string
	AccountID int64
	Version   int // the account's session_version when the flow began
	Purpose   string
	Data      []byte
}

// CreateFlow stores f under the hash of its token, clearing expired rows as it
// goes. ErrConflict means this system has too many anonymous flows of that kind.
func (u Users) CreateFlow(ctx context.Context, hash string, f Flow, ttl time.Duration) error {
	res, err := u.db.ExecContext(ctx, `
		WITH expired AS (
		  DELETE FROM auth_flows WHERE token_hash IN (
		    SELECT token_hash FROM auth_flows WHERE expires_at <= now() FOR UPDATE SKIP LOCKED))
		INSERT INTO auth_flows (token_hash, realm, kind, account_id, session_version, purpose, data, expires_at)
		SELECT $1::TEXT, $2::TEXT, $3::TEXT, $4::BIGINT, $5::INT, $6::TEXT, $7::BYTEA, now() + make_interval(secs => $8::FLOAT8)
		WHERE $3::TEXT NOT IN ('passkey_login', 'captcha') OR
		  (SELECT count(*) FROM auth_flows WHERE realm = $2::TEXT AND kind = $3::TEXT AND expires_at > now()) < $9`,
		hash, u.table, f.Kind, f.AccountID, f.Version, f.Purpose, f.Data, ttl.Seconds(), maxAnonymousFlows)
	if errors.Is(affectedOne(res, err), ErrNotFound) {
		return ErrConflict
	}
	return err
}

// liveFlow is what makes a flow usable: unexpired, of this table and kind, and
// its account still enabled at the session version the flow began with, so
// every factor change, logout and disable voids the flows under way.
const liveFlow = `f.token_hash = $1 AND f.realm = $2 AND f.kind = $3 AND f.expires_at > now()
	AND u.id = f.account_id AND u.session_version = f.session_version AND u.status = 1`

// Flow reads a live flow without using it up, so a mistyped code can be retried.
func (u Users) Flow(ctx context.Context, hash, kind string) (Flow, error) {
	f := Flow{Kind: kind}
	err := u.db.QueryRowContext(ctx, fmt.Sprintf(
		`SELECT f.account_id, f.session_version, f.purpose, f.data FROM auth_flows f, %s u WHERE %s`, u.table, liveFlow),
		hash, u.table, kind).Scan(&f.AccountID, &f.Version, &f.Purpose, &f.Data)
	return f, notFound(err)
}

// querier is a *sql.DB or a *sql.Tx.
type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func (u Users) takeFlow(ctx context.Context, q querier, hash, kind, purpose string, account int64) ([]byte, error) {
	var data []byte
	err := q.QueryRowContext(ctx, fmt.Sprintf(
		`DELETE FROM auth_flows f USING %s u WHERE %s AND f.purpose = $4 AND f.account_id = $5 RETURNING f.data`, u.table, liveFlow),
		hash, u.table, kind, purpose, account).Scan(&data)
	return data, notFound(err)
}

// TakeFlow uses a live flow of account up and returns its data: of two
// concurrent calls one gets ErrNotFound.
func (u Users) TakeFlow(ctx context.Context, hash, kind, purpose string, account int64) ([]byte, error) {
	return u.takeFlow(ctx, u.db, hash, kind, purpose, account)
}

// TakeChallenge is TakeFlow for a flow that has no account yet.
func (u Users) TakeChallenge(ctx context.Context, hash, kind string) ([]byte, error) {
	var data []byte
	err := u.db.QueryRowContext(ctx, `
		DELETE FROM auth_flows WHERE token_hash = $1 AND realm = $2 AND kind = $3 AND expires_at > now() RETURNING data`,
		hash, u.table, kind).Scan(&data)
	return data, notFound(err)
}

// FactorStatus says what an account has bound.
type FactorStatus struct {
	TOTP       bool
	Passkey    bool
	BackupLeft int
}

// Any is whether a password alone is no longer enough.
func (s FactorStatus) Any() bool { return s.TOTP || s.Passkey }

func (u Users) FactorStatus(ctx context.Context, id int64) (FactorStatus, error) {
	var s FactorStatus
	err := u.db.QueryRowContext(ctx, fmt.Sprintf(`
		SELECT count(*) FILTER (WHERE kind = 'totp') > 0, count(*) FILTER (WHERE kind = 'passkey') > 0,
		       count(*) FILTER (WHERE kind = 'backup')
		FROM auth_factors WHERE %s = $1`, u.fk), id).Scan(&s.TOTP, &s.Passkey, &s.BackupLeft)
	return s, err
}

// change runs fn with the session version bumped in the same transaction, which
// also locks the account: two binds at once cannot both be the first.
func (u Users) change(ctx context.Context, id int64, fn func(tx *sql.Tx) error) (int, error) {
	tx, err := u.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var ver int
	err = tx.QueryRowContext(ctx, fmt.Sprintf(
		`UPDATE %s SET session_version = session_version + 1 WHERE id = $1 AND status = 1 RETURNING session_version`, u.table), id).Scan(&ver)
	if err != nil {
		return 0, notFound(err)
	}
	if err := fn(tx); err != nil {
		return 0, err
	}
	return ver, tx.Commit()
}

func (u Users) insertBackup(ctx context.Context, tx *sql.Tx, id int64, hashes [][]byte) error {
	for _, h := range hashes {
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`INSERT INTO auth_factors (%s, kind, lookup) VALUES ($1, 'backup', $2)`, u.fk), id, h); err != nil {
			return err
		}
	}
	return nil
}

// BindFactor adds a TOTP or passkey and returns the new session version. The
// backup codes are stored only with the account's first factor, which first
// reports. Having that kind already is ErrConflict.
func (u Users) BindFactor(ctx context.Context, id int64, kind string, lookup, secret []byte, step int64, backup [][]byte) (ver int, first bool, err error) {
	ver, err = u.change(ctx, id, func(tx *sql.Tx) error {
		var bound bool
		if err := tx.QueryRowContext(ctx, fmt.Sprintf(
			`SELECT EXISTS (SELECT 1 FROM auth_factors WHERE %s = $1 AND kind <> 'backup')`, u.fk), id).Scan(&bound); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(
			`INSERT INTO auth_factors (%s, kind, lookup, secret, last_step) VALUES ($1, $2, $3, $4, $5)`, u.fk), id, kind, lookup, secret, step); err != nil {
			if pgCode(err) == "23505" {
				return ErrConflict
			}
			return err
		}
		first = !bound
		if bound {
			return nil
		}
		return u.insertBackup(ctx, tx, id, backup)
	})
	return ver, first, err
}

// UnbindFactor removes a TOTP or passkey, and the backup codes with the last of
// them, and returns the new session version. Having none is ErrNotFound.
func (u Users) UnbindFactor(ctx context.Context, id int64, kind string) (int, error) {
	return u.change(ctx, id, func(tx *sql.Tx) error {
		if err := affectedOne(tx.ExecContext(ctx, fmt.Sprintf(`DELETE FROM auth_factors WHERE %s = $1 AND kind = $2`, u.fk), id, kind)); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, fmt.Sprintf(`
			DELETE FROM auth_factors b WHERE b.%[1]s = $1 AND b.kind = 'backup'
			  AND NOT EXISTS (SELECT 1 FROM auth_factors f WHERE f.%[1]s = $1 AND f.kind <> 'backup')`, u.fk), id)
		return err
	})
}

// ReplaceBackup swaps the backup codes and returns the new session version. An
// account with no factor has none to replace: ErrNotFound.
func (u Users) ReplaceBackup(ctx context.Context, id int64, hashes [][]byte) (int, error) {
	return u.change(ctx, id, func(tx *sql.Tx) error {
		var bound bool
		if err := tx.QueryRowContext(ctx, fmt.Sprintf(
			`SELECT EXISTS (SELECT 1 FROM auth_factors WHERE %s = $1 AND kind <> 'backup')`, u.fk), id).Scan(&bound); err != nil {
			return err
		}
		if !bound {
			return ErrNotFound
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`DELETE FROM auth_factors WHERE %s = $1 AND kind = 'backup'`, u.fk), id); err != nil {
			return err
		}
		return u.insertBackup(ctx, tx, id, hashes)
	})
}

// ResetFactors removes every factor and logs the user out; API tokens stay.
func (u Users) ResetFactors(ctx context.Context, id int64) error {
	return affectedOne(u.db.ExecContext(ctx, fmt.Sprintf(`
		WITH gone AS (DELETE FROM auth_factors WHERE %s = $1)
		UPDATE %s SET session_version = session_version + 1 WHERE id = $1`, u.fk, u.table), id))
}

// TOTP returns the account's sealed secret. Having none is ErrNotFound.
func (u Users) TOTP(ctx context.Context, id int64) ([]byte, error) {
	var secret []byte
	err := u.db.QueryRowContext(ctx, fmt.Sprintf(`SELECT secret FROM auth_factors WHERE %s = $1 AND kind = 'totp'`, u.fk), id).Scan(&secret)
	return secret, notFound(err)
}

// AdvanceTOTP records a used time step. False means that step or a later one
// was used before: the code is a replay.
func (u Users) AdvanceTOTP(ctx context.Context, id, step int64) (bool, error) {
	err := affectedOne(u.db.ExecContext(ctx, fmt.Sprintf(`
		UPDATE auth_factors SET last_step = $2, last_used_at = now() WHERE %s = $1 AND kind = 'totp' AND last_step < $2`, u.fk), id, step))
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

// UseBackup uses a backup code up. A wrong one is ErrNotFound.
func (u Users) UseBackup(ctx context.Context, id int64, hash []byte) error {
	return affectedOne(u.db.ExecContext(ctx, fmt.Sprintf(`DELETE FROM auth_factors WHERE %s = $1 AND kind = 'backup' AND lookup = $2`, u.fk), id, hash))
}

// LoginBackup uses up a backup code and the login flow together, so neither is
// lost without the other. A wrong code or a dead flow is ErrNotFound.
func (u Users) LoginBackup(ctx context.Context, flowHash string, id int64, codeHash []byte) error {
	tx, err := u.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := affectedOne(tx.ExecContext(ctx, fmt.Sprintf(`DELETE FROM auth_factors WHERE %s = $1 AND kind = 'backup' AND lookup = $2`, u.fk), id, codeHash)); err != nil {
		return err
	}
	if _, err := u.takeFlow(ctx, tx, flowHash, FlowLogin, "", id); err != nil {
		return err
	}
	return tx.Commit()
}

// Passkey returns the account's credential JSON. Having none is ErrNotFound.
func (u Users) Passkey(ctx context.Context, id int64) ([]byte, error) {
	var secret []byte
	err := u.db.QueryRowContext(ctx, fmt.Sprintf(`SELECT secret FROM auth_factors WHERE %s = $1 AND kind = 'passkey'`, u.fk), id).Scan(&secret)
	return secret, notFound(err)
}

// SavePasskey stores the credential after a use: its counter and flags change.
func (u Users) SavePasskey(ctx context.Context, id int64, secret []byte) error {
	return affectedOne(u.db.ExecContext(ctx, fmt.Sprintf(`
		UPDATE auth_factors SET secret = $2, last_used_at = now() WHERE %s = $1 AND kind = 'passkey'`, u.fk), id, secret))
}
