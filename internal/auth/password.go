package auth

import (
	"context"
	"errors"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

var (
	ErrLocked         = errors.New("too many failed logins")
	ErrBadCredentials = errors.New("invalid username or password")
	ErrDisabled       = errors.New("account disabled")
)

// dummyHash is compared against when the user does not exist so that
// response timing does not reveal valid usernames.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("dummy-password-for-timing"), bcrypt.DefaultCost)

// ValidatePassword enforces 8-72 bytes (bcrypt's limit).
func ValidatePassword(pw string) error {
	if len(pw) < 8 || len(pw) > 72 {
		return errors.New("密码长度须为 8–72 字节")
	}
	return nil
}

func HashPassword(pw string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	return string(h), err
}

func CheckPassword(hash, pw string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil
}

// Account is what Login needs to know about a stored user.
type Account struct {
	ID      int64
	Name    string
	Hash    string
	Version int
	Active  bool
}

// FindAccount looks a user up by username; found is false if absent.
type FindAccount func(ctx context.Context, username string) (acc Account, found bool, err error)

// LoginKey is what Login counts the failures of a username under, whether or
// not the account exists.
func LoginKey(username string) string { return strings.ToLower(strings.TrimSpace(username)) }

// Login checks rate limit and credentials. On ErrBadCredentials the returned
// Account has ID 0 when the user does not exist.
func Login(ctx context.Context, lim *Limiter, find FindAccount, username, password string) (Account, error) {
	key := LoginKey(username)
	if !lim.Acquire(key) {
		return Account{}, ErrLocked
	}
	// Settled on every return path, panics included.
	settle := lim.Release
	defer func() { settle(key) }()
	acc, found, err := find(ctx, strings.TrimSpace(username))
	if err != nil {
		return Account{}, err
	}
	if !found {
		CheckPassword(string(dummyHash), password)
		settle = lim.Fail
		return Account{}, ErrBadCredentials
	}
	if !CheckPassword(acc.Hash, password) {
		settle = lim.Fail
		return Account{ID: acc.ID, Name: acc.Name}, ErrBadCredentials
	}
	settle = lim.Reset
	if !acc.Active {
		return acc, ErrDisabled
	}
	return acc, nil
}
