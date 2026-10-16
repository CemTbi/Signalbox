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
	b.used = now
	r := b.limiter.ReserveN(now, 1)
	if delay := r.DelayFrom(now); delay > 0 {
		r.CancelAt(now)
		return false, delay
	}
	return true, 0
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
