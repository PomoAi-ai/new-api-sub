package channels

import "time"

// ProbeBusy lets the HTTP tests mark a channel's test or balance refresh as running.
var ProbeBusy = &probeBusy

// TakeRate lets the tests check rateWindow without waiting for a window.
func TakeRate(limit, key, n int) (first, second int) {
	w := rateWindow{limit: limit, per: time.Hour}
	return w.take(key, n), w.take(key, n)
}

// ResetPushRate gives the push worker a new window: the tests push far more
// than a process does in 3 minutes.
func ResetPushRate() {
	for _, w := range []*rateWindow{&pushRate, &stateRate} {
		w.mu.Lock()
		w.used = map[int]int{}
		w.mu.Unlock()
	}
}
