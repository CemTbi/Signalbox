// SPDX-License-Identifier: Apache-2.0

package secretbox_test

import (
	"bytes"
	"encoding/base64"
	"errors"
	"testing"

	"github.com/CemTbi/Signalbox/internal/platform/secretbox"
)

func TestSealAndOpen(t *testing.T) {
	box, err := secretbox.New(secretbox.NewKey())
	if err != nil {
		t.Fatal(err)
	}
	sealed := box.Seal([]byte("s3cret"), []byte("env-1/API_KEY"))
	if bytes.Contains(sealed, []byte("s3cret")) {
		t.Fatal("the plaintext is in the ciphertext")
	}
	// The same plaintext seals differently each time.
	if bytes.Equal(sealed, box.Seal([]byte("s3cret"), []byte("env-1/API_KEY"))) {
		t.Error("two seals are equal")
	}
	plain, err := box.Open(sealed, []byte("env-1/API_KEY"))
	if err != nil || string(plain) != "s3cret" {
		t.Fatalf("open = %q, %v", plain, err)
	}
	// Bound to where it belongs: another row's additional data fails.
	if _, err := box.Open(sealed, []byte("env-2/API_KEY")); !errors.Is(err, secretbox.ErrWrongKey) {
		t.Errorf("other additional data: %v", err)
	}
	other, _ := secretbox.New(secretbox.NewKey())
	if _, err := other.Open(sealed, []byte("env-1/API_KEY")); !errors.Is(err, secretbox.ErrWrongKey) {
		t.Errorf("another key: %v", err)
	}
	sealed[len(sealed)-1] ^= 1
	if _, err := box.Open(sealed, []byte("env-1/API_KEY")); !errors.Is(err, secretbox.ErrWrongKey) {
		t.Errorf("tampered: %v", err)
	}
	if _, err := box.Open([]byte("short"), nil); !errors.Is(err, secretbox.ErrWrongKey) {
		t.Errorf("short: %v", err)
	}
}

func TestParseKey(t *testing.T) {
	key := secretbox.NewKey()
	a, err := secretbox.ParseKey(" " + base64.StdEncoding.EncodeToString(key) + "\n")
	if err != nil {
		t.Fatal(err)
	}
	b, err := secretbox.ParseKey(base64.RawURLEncoding.EncodeToString(key))
	if err != nil || a.ID != b.ID || len(a.ID) != 16 {
		t.Errorf("ids %q %q, %v", a.ID, b.ID, err)
	}
	for _, bad := range []string{"", "not base64!", base64.StdEncoding.EncodeToString(key[:16])} {
		if _, err := secretbox.ParseKey(bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}
