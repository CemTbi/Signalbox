// SPDX-License-Identifier: Apache-2.0

// Package secretbox encrypts secrets at rest with envelope encryption. An
// installation's key-encryption key, from SIGNALBOX_SECRET_KEY, encrypts
// random data keys, one per workspace; data keys encrypt the secrets. Both
// layers use AES-256-GCM with a random 96-bit nonce, and bind each
// ciphertext to where it belongs with additional data, so a ciphertext
// copied to another row doesn't decrypt. Rotating the key-encryption key
// means re-encrypting the data keys only. See
// docs/rfcs/0001-api-client-and-tests.md.
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// KeySize is the size of every key, in bytes.
const KeySize = 32

// ErrNoKey means the installation has no key-encryption key, so it can't
// store secrets.
var ErrNoKey = errors.New("secretbox: no secret key is configured; set SIGNALBOX_SECRET_KEY to 32 random bytes in base64, such as the output of openssl rand -base64 32")

// ErrWrongKey means a ciphertext doesn't open with the key: it was
// encrypted with another key, or changed.
var ErrWrongKey = errors.New("secretbox: the secret can't be decrypted with this key")

// Box seals and opens with one key.
type Box struct {
	aead cipher.AEAD
	// ID names the key without revealing it: the first 8 bytes of its
	// SHA-256, in hex. Stored beside what it encrypts, it tells a changed
	// key from damaged data.
	ID string
}

// New returns a Box for a 32-byte key.
func New(key []byte) (*Box, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("secretbox: the key must be %d bytes, not %d", KeySize, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(key)
	return &Box{aead: aead, ID: fmt.Sprintf("%x", sum[:8])}, nil
}

// ParseKey reads a key-encryption key from its base64 form, as
// SIGNALBOX_SECRET_KEY holds it.
func ParseKey(s string) (*Box, error) {
	s = strings.TrimSpace(s)
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if key, err := enc.DecodeString(s); err == nil && len(key) == KeySize {
			return New(key)
		}
	}
	return nil, fmt.Errorf("secretbox: the secret key must be %d random bytes in base64 (44 characters), such as the output of openssl rand -base64 32", KeySize)
}

// NewKey returns a random key.
func NewKey() []byte {
	key := make([]byte, KeySize)
	_, _ = rand.Read(key) // crypto/rand never fails
	return key
}

// Seal encrypts plaintext, bound to additional data that Open must be
// given again. The result is the nonce followed by the ciphertext.
func (b *Box) Seal(plaintext, additional []byte) []byte {
	nonce := make([]byte, b.aead.NonceSize(), b.aead.NonceSize()+len(plaintext)+b.aead.Overhead())
	_, _ = rand.Read(nonce)
	return b.aead.Seal(nonce, nonce, plaintext, additional)
}

// Open decrypts what Seal made with the same additional data.
func (b *Box) Open(sealed, additional []byte) ([]byte, error) {
	n := b.aead.NonceSize()
	if len(sealed) < n+b.aead.Overhead() {
		return nil, ErrWrongKey
	}
	plain, err := b.aead.Open(nil, sealed[:n], sealed[n:], additional)
	if err != nil {
		return nil, ErrWrongKey
	}
	return plain, nil
}
