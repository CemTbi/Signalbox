// SPDX-License-Identifier: Apache-2.0

// Package token makes the secret tokens behind session cookies and
// invitation links. A token is 256 random bits in unpadded base64url. Only
// its SHA-256 hash is stored, so a copy of the database can't be used to
// sign in or to accept an invitation; with 256 bits of entropy, a fast hash
// is enough.
package token

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
)

// Length is the length of a token in characters.
const Length = 43

// Random returns a new token.
func Random() string {
	var b [32]byte
	_, _ = rand.Read(b[:]) // crypto/rand never fails
	return base64.RawURLEncoding.EncodeToString(b[:])
}

// New returns a new token and the hash to store.
func New() (token string, hash []byte) {
	token = Random()
	return token, Hash(token)
}

// Hash returns the SHA-256 hash of a token.
func Hash(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

// WellFormed reports whether s could be a token, so lookups for strings
// that can't exist never reach the database.
func WellFormed(s string) bool {
	if len(s) != Length {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !isBase64URL(s[i]) {
			return false
		}
	}
	return true
}

func isBase64URL(c byte) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || c == '-' || c == '_'
}
