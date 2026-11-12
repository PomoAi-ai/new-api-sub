package auth

import (
	"sync"
	"time"
)

const (
	maxFailures = 5
	failWindow  = 15 * time.Minute
	lockFor     = 15 * time.Minute
	pruneAbove  = 10000
)

type attempt struct {
	fails       int
	inFlight    int
	first       time.Time
	lockedUntil time.Time
}

// Limiter locks a username after 5 failures within 15 minutes, for 15
// minutes. In-memory and per process.
type Limiter struct {
	mu  sync.Mutex
	m   map[string]*attempt
	now func() time.Time
}

func NewLimiter() *Limiter {
	return &Limiter{m: map[string]*attempt{}, now: time.Now}
}

// Acquire reserves one login attempt. Attempts in flight count against the
// limit, so concurrent requests cannot get past it. Every true result must be
// settled by exactly one Fail, Reset or Release.
func (l *Limiter) Acquire(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if len(l.m) > pruneAbove {
		for k, a := range l.m {
			if a.inFlight == 0 && now.Sub(a.first) > failWindow && now.After(a.lockedUntil) {
				delete(l.m, k)
			}
		}
	}
	a := l.m[key]
	if a == nil {
		a = &attempt{first: now}
		l.m[key] = a
	}
	if now.Before(a.lockedUntil) {
		return false
	}
	if now.Sub(a.first) > failWindow {
		a.fails, a.first = 0, now
	}
	if a.fails+a.inFlight >= maxFailures {
		return false
	}
	a.inFlight++
	return true
}

// Failures is the count of recent failures at key, for asking a captcha
// before the lock. A lock starts the count again.
func (l *Limiter) Failures(key string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	if a := l.m[key]; a != nil && l.now().Sub(a.first) <= failWindow {
		return a.fails
	}
	return 0
}

// Locked says whether key is locked now, as it is right after its fifth failure.
func (l *Limiter) Locked(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	a := l.m[key]
	return a != nil && l.now().Before(a.lockedUntil)
}

// settle ends one acquired attempt and returns its record.
func (l *Limiter) settle(key string) *attempt {
	a := l.m[key]
	if a == nil {
		a = &attempt{first: l.now()}
		l.m[key] = a
	}
	if a.inFlight > 0 {
		a.inFlight--
	}
	return a
}

// Fail settles an attempt as a failed login.
func (l *Limiter) Fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	a := l.settle(key)
	a.fails++
	if a.fails >= maxFailures {
		now := l.now()
		a.lockedUntil = now.Add(lockFor)
		a.fails = 0
		a.first = now
	}
}

// Reset settles an attempt as a successful login and clears the failures.
func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if a := l.settle(key); a.inFlight == 0 {
		delete(l.m, key)
	} else {
		a.fails = 0
	}
}

// Release settles an attempt that ended without a verdict (e.g. a store
// error), so it counts neither as a failure nor as a success.
func (l *Limiter) Release(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.settle(key)
}
