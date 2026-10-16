// SPDX-License-Identifier: Apache-2.0

package ratelimit

import (
	"testing"
	"time"
)

func TestBurstThenSteadyRate(t *testing.T) {
	l := New(3, 10*time.Second)
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	l.now = func() time.Time { return now }

	for i := range 3 {
		if ok, _ := l.Allow("192.0.2.1"); !ok {
			t.Fatalf("request %d of the burst was refused", i+1)
		}
	}
	ok, retry := l.Allow("192.0.2.1")
	if ok || retry != 10*time.Second {
		t.Errorf("fourth request: ok=%v retry=%s, want refused with 10s", ok, retry)
	}
	if ok, _ := l.Allow("192.0.2.2"); !ok {
		t.Error("another key shares the first key's bucket")
	}

	now = now.Add(4 * time.Second)
	// x/time/rate counts tokens in floating point, so allow for rounding.
	if ok, retry := l.Allow("192.0.2.1"); ok || retry.Round(time.Millisecond) != 6*time.Second {
		t.Errorf("after 4s: ok=%v retry=%s, want refused with 6s", ok, retry)
	}
	now = now.Add(6 * time.Second)
	if ok, _ := l.Allow("192.0.2.1"); !ok {
		t.Error("a token didn't come back after the interval")
	}
}

func TestRefusalsDontConsumeTokens(t *testing.T) {
	l := New(1, time.Minute)
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	l.now = func() time.Time { return now }
	l.Allow("k")
	for range 10 {
		l.Allow("k") // refused
	}
	now = now.Add(time.Minute)
	if ok, _ := l.Allow("k"); !ok {
		t.Error("refused requests pushed the next token further out")
	}
}

func TestSweepForgetsRefilledBuckets(t *testing.T) {
	l := New(2, time.Second)
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	l.now = func() time.Time { return now }
	l.Allow("a")
	l.Allow("b")
	now = now.Add(2 * time.Minute)
	l.Allow("c")
	if len(l.buckets) != 1 {
		t.Errorf("%d buckets after sweeping, want only the new one", len(l.buckets))
	}
}

func TestTooManyKeysAreRefused(t *testing.T) {
	l := New(1, time.Second)
	l.maxKeys = 2
	l.Allow("a")
	l.Allow("b")
	if ok, _ := l.Allow("c"); ok {
		t.Error("a key beyond the limit was allowed")
	}
}
