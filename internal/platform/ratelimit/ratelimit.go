// SPDX-License-Identifier: Apache-2.0

// Package ratelimit limits how often a key, such as a client address or an
// account, may do something. Limits live in process memory: with several
// api processes, each enforces its own.
package ratelimit

import (
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// Limiter is a set of token buckets, one per key.
type Limiter struct {
	every   time.Duration
	burst   int
	maxKeys int
	now     func() time.Time

	mu      sync.Mutex
	buckets map[string]*bucket
	swept   time.Time
}

type bucket struct {
	limiter *rate.Limiter
	used    time.Time
}

// New returns a limiter that allows burst events at once per key, and then
// one more each time the interval passes.
func New(burst int, every time.Duration) *Limiter {
	return &Limiter{
		every:   every,
		burst:   burst,
		maxKeys: 100_000,
		now:     time.Now,
		buckets: map[string]*bucket{},
	}
}

// Allow takes a token for the key. When none is left it returns false and
// how long until the next one.
func (l *Limiter) Allow(key string) (ok bool, retryAfter time.Duration) {
	return l.AllowN(key, 1)
}

// AllowN takes n tokens for the key at once, such as one per span in a
// request. When too few are left it takes none, and returns false and how
// long until there are. More than the burst passes only with a full
// bucket, and then puts it in debt for the rest, so later events wait
// until the whole request is paid for.
func (l *Limiter) AllowN(key string, n int) (ok bool, retryAfter time.Duration) {
	n = max(n, 1)
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sweep(now)

	b, found := l.buckets[key]
	if !found {
		if len(l.buckets) >= l.maxKeys {
			// Too many keys to track, which only happens under attack.
			// Refusing is safer than forgetting buckets that are in use.
			return false, l.every
		}
		b = &bucket{limiter: rate.NewLimiter(rate.Every(l.every), l.burst)}
		l.buckets[key] = b
	}
	first := min(n, l.burst)
	r := b.limiter.ReserveN(now, first)
	if delay := r.DelayFrom(now); delay > 0 {
		r.CancelAt(now)
		b.used = later(b.used, now)
		return false, delay
	}
	// Reservations that wait aren't cancelled: they are the debt.
	for rest := n - first; rest > 0; rest -= l.burst {
		b.limiter.ReserveN(now, min(rest, l.burst))
	}
	// The sweep keeps the bucket until the debt is paid and it refills.
	b.used = later(b.used, now.Add(time.Duration(n-first)*l.every))
	return true, 0
}

func later(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// sweep drops buckets that have refilled completely, at most once a minute.
// A full bucket behaves exactly like a new one, so nothing is lost.
func (l *Limiter) sweep(now time.Time) {
	if now.Sub(l.swept) < time.Minute {
		return
	}
	l.swept = now
	full := l.every * time.Duration(l.burst)
	for key, b := range l.buckets {
		if now.Sub(b.used) > full {
			delete(l.buckets, key)
		}
	}
}
