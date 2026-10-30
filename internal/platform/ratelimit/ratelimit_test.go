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

func TestAllowNTakesManyTokens(t *testing.T) {
	// 10 spans a second, up to 20 at once.
	l := New(20, 100*time.Millisecond)
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	l.now = func() time.Time { return now }

	if ok, _ := l.AllowN("env", 15); !ok {
		t.Fatal("15 of a bucket of 20 were refused")
	}
	ok, retry := l.AllowN("env", 10)
	if ok || retry.Round(time.Millisecond) != 500*time.Millisecond {
		t.Errorf("10 more: ok=%v retry=%s, want refused with 500ms", ok, retry)
	}
	// A refusal took nothing: the 5 left are still there.
	if ok, _ := l.AllowN("env", 5); !ok {
		t.Error("the 5 tokens left were gone")
	}
	// More than the burst waits for a full bucket, then takes all of it.
	now = now.Add(2 * time.Second)
	if ok, _ := l.AllowN("env", 1000); !ok {
		t.Error("a request larger than the burst never passes")
	}
	if ok, _ := l.Allow("env"); ok {
		t.Error("the large request didn't empty the bucket")
	}
}
