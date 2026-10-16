// SPDX-License-Identifier: Apache-2.0

package identity

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

var cheap = Argon2Params{Memory: 64, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32}

func TestPasswordHashRoundTrip(t *testing.T) {
	pw, err := newPasswords(cheap, 1)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	hash, err := pw.hash(ctx, "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=64,t=1,p=1$") {
		t.Errorf("hash %s isn't in PHC format with the configured parameters", hash)
	}
	if other, _ := pw.hash(ctx, "correct horse battery staple"); other == hash {
		t.Error("two hashes of one password are equal; the salt isn't random")
	}

	for _, tt := range []struct {
		password string
		want     bool
	}{
		{"correct horse battery staple", true},
		{"correct horse battery stapl", false},
		{"", false},
	} {
		match, rehash, err := pw.verify(ctx, tt.password, hash)
		if err != nil || match != tt.want || rehash {
			t.Errorf("verify(%q) = %v, %v, %v; want %v, false, nil", tt.password, match, rehash, err, tt.want)
		}
	}
}

func TestPasswordsAreNormalized(t *testing.T) {
	pw, err := newPasswords(cheap, 1)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	composed := "café-pässword"     // é and ä as single code points
	decomposed := "café-pässword" // e and a with combining marks
	hash, _ := pw.hash(ctx, composed)
	if match, _, _ := pw.verify(ctx, decomposed, hash); !match {
		t.Error("the same password in decomposed form doesn't match")
	}
}

func TestVerifyRejectsMalformedHashes(t *testing.T) {
	pw, err := newPasswords(cheap, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range []string{
		"",
		"plain-text-password",
		"$argon2i$v=19$m=64,t=1,p=1$c2FsdHNhbHRzYWx0$a2V5a2V5a2V5a2V5a2V5a2V5",
		"$argon2id$v=16$m=64,t=1,p=1$c2FsdHNhbHRzYWx0$a2V5a2V5a2V5a2V5a2V5a2V5",
		"$argon2id$v=19$m=4194304,t=1,p=1$c2FsdHNhbHRzYWx0$a2V5a2V5a2V5a2V5a2V5a2V5", // 4 GiB
		"$argon2id$v=19$m=64,t=1,p=1$!!!$a2V5a2V5a2V5a2V5a2V5a2V5",
	} {
		if _, _, err := pw.verify(context.Background(), "x", h); !errors.Is(err, errMalformedHash) {
			t.Errorf("verify with %q: error = %v, want errMalformedHash", h, err)
		}
	}
}

func TestHashingIsBoundedAndCancellable(t *testing.T) {
	pw, err := newPasswords(cheap, 1)
	if err != nil {
		t.Fatal(err)
	}
	pw.slots <- struct{}{} // occupy the only slot
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := pw.hash(ctx, "password"); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("hash while every slot is busy: error = %v, want the context's deadline", err)
	}
	<-pw.slots
}

func TestDefaultParametersMatchADR(t *testing.T) {
	want := Argon2Params{Memory: 64 * 1024, Iterations: 3, Parallelism: 1, SaltLength: 16, KeyLength: 32}
	if DefaultArgon2 != want {
		t.Errorf("DefaultArgon2 = %+v, want %+v from ADR-0004", DefaultArgon2, want)
	}
}

func TestSetupCodes(t *testing.T) {
	seen := map[string]bool{}
	for range 100 {
		code := newSetupCode()
		if len(code) != 19 || strings.Count(code, "-") != 3 || strings.ContainsAny(code, "ILOU") {
			t.Fatalf("setup code %q has the wrong shape", code)
		}
		if seen[code] {
			t.Fatalf("setup code %q repeated", code)
		}
		seen[code] = true
	}
	if a, b := normalizeSetupCode("k7qm-2xwp 9d4h-tr6b"), normalizeSetupCode("K7QM2XWP9D4HTR6B"); a != b {
		t.Errorf("normalized codes differ: %q and %q", a, b)
	}
}
