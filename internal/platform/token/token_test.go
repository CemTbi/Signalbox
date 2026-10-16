// SPDX-License-Identifier: Apache-2.0

package token_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/CemTbi/Signalbox/internal/platform/token"
)

func TestNewTokensAreRandomAndHashed(t *testing.T) {
	a, hashA := token.New()
	b, _ := token.New()
	if a == b {
		t.Fatal("two tokens are equal")
	}
	if len(a) != token.Length || !token.WellFormed(a) {
		t.Errorf("token %q isn't %d base64url characters", a, token.Length)
	}
	if !bytes.Equal(hashA, token.Hash(a)) || len(hashA) != 32 {
		t.Error("the hash isn't the SHA-256 of the token")
	}
}

func TestWellFormed(t *testing.T) {
	for _, s := range []string{"", "short", strings.Repeat("A", 42), strings.Repeat("A", 44), strings.Repeat("+", 43), strings.Repeat("=", 43)} {
		if token.WellFormed(s) {
			t.Errorf("WellFormed(%q) = true", s)
		}
	}
	if !token.WellFormed(strings.Repeat("aZ0-_", 8) + "abc") {
		t.Error("a token of base64url characters isn't well formed")
	}
}
