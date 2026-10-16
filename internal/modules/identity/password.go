// SPDX-License-Identifier: Apache-2.0

package identity

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
	"golang.org/x/text/unicode/norm"
)

// Argon2Params are the cost parameters for password hashes. Each hash
// records the parameters it was made with, so raising them only changes new
// hashes, and older ones are upgraded when their owner next signs in.
type Argon2Params struct {
	// Memory is in KiB.
	Memory      uint32
	Iterations  uint32
	Parallelism uint8
	SaltLength  uint32
	KeyLength   uint32
}

// DefaultArgon2 is the configuration from ADR-0004: Argon2id with 64 MiB of
// memory, 3 iterations and a parallelism of 1.
var DefaultArgon2 = Argon2Params{Memory: 64 * 1024, Iterations: 3, Parallelism: 1, SaltLength: 16, KeyLength: 32}

var errMalformedHash = errors.New("malformed password hash")

// passwords hashes and verifies passwords. Every hash holds Memory KiB while
// it runs, so the number of hashes in flight is bounded; otherwise a burst of
// sign-in attempts could exhaust the server's memory.
type passwords struct {
	params Argon2Params
	slots  chan struct{}
	// dummy is verified when an account doesn't exist, so a failed sign-in
	// takes as long whether or not the email address has an account.
	dummy string
}

func newPasswords(params Argon2Params, concurrency int) (*passwords, error) {
	pw := &passwords{params: params, slots: make(chan struct{}, max(1, concurrency))}
	dummy, err := pw.hash(context.Background(), rand.Text())
	if err != nil {
		return nil, err
	}
	pw.dummy = dummy
	return pw, nil
}

func (pw *passwords) hash(ctx context.Context, password string) (string, error) {
	p := pw.params
	salt := make([]byte, p.SaltLength)
	_, _ = rand.Read(salt) // crypto/rand never fails
	key, err := pw.derive(ctx, password, salt, p.Iterations, p.Memory, p.Parallelism, p.KeyLength)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, p.Memory, p.Iterations, p.Parallelism,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

// verify reports whether the password matches the encoded hash, and whether
// the hash should be replaced because its parameters are out of date.
func (pw *passwords) verify(ctx context.Context, password, encoded string) (match, rehash bool, err error) {
	params, salt, key, err := decodeHash(encoded)
	if err != nil {
		return false, false, err
	}
	other, err := pw.derive(ctx, password, salt, params.Iterations, params.Memory, params.Parallelism, params.KeyLength)
	if err != nil {
		return false, false, err
	}
	if subtle.ConstantTimeCompare(key, other) != 1 {
		return false, false, nil
	}
	current := pw.params
	rehash = params.Memory != current.Memory || params.Iterations != current.Iterations ||
		params.Parallelism != current.Parallelism || params.KeyLength != current.KeyLength
	return true, rehash, nil
}

// verifyDummy spends the time a real verification would.
func (pw *passwords) verifyDummy(ctx context.Context, password string) {
	_, _, _ = pw.verify(ctx, password, pw.dummy)
}

// derive runs Argon2id once a slot is free. Passwords are normalized to NFKC
// first, so the same password typed on different systems matches.
func (pw *passwords) derive(ctx context.Context, password string, salt []byte, t, m uint32, p uint8, keyLen uint32) ([]byte, error) {
	select {
	case pw.slots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-pw.slots }()
	return argon2.IDKey([]byte(norm.NFKC.String(password)), salt, t, m, p, keyLen), nil
}

// decodeHash reads a hash in PHC string format:
// $argon2id$v=19$m=65536,t=3,p=1$<salt>$<key>, base64 without padding.
func decodeHash(encoded string) (Argon2Params, []byte, []byte, error) {
	var p Argon2Params
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return p, nil, nil, errMalformedHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return p, nil, nil, fmt.Errorf("%w: unsupported version %q", errMalformedHash, parts[2])
	}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.Memory, &p.Iterations, &p.Parallelism); err != nil {
		return p, nil, nil, fmt.Errorf("%w: parameters %q", errMalformedHash, parts[3])
	}
	// Bounds guard against a corrupt row making the server allocate
	// gigabytes: at most 1 GiB, 100 iterations and 16 lanes.
	if p.Memory < 8 || p.Memory > 1<<20 || p.Iterations < 1 || p.Iterations > 100 || p.Parallelism < 1 || p.Parallelism > 16 {
		return p, nil, nil, fmt.Errorf("%w: parameters out of range", errMalformedHash)
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) < 8 || len(salt) > maxHashPart {
		return p, nil, nil, fmt.Errorf("%w: salt", errMalformedHash)
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(key) < 16 || len(key) > maxHashPart {
		return p, nil, nil, fmt.Errorf("%w: key", errMalformedHash)
	}
	p.SaltLength, p.KeyLength = byteLen(salt), byteLen(key)
	return p, salt, key, nil
}

// maxHashPart bounds the salt and key of a stored hash.
const maxHashPart = 1024

// byteLen returns the length of a salt or key that decodeHash has already
// bounded by maxHashPart.
func byteLen(b []byte) uint32 {
	return uint32(min(len(b), maxHashPart)) //nolint:gosec // G115: bounded by maxHashPart above.
}
