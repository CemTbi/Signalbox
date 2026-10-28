// SPDX-License-Identifier: Apache-2.0

// Package id creates and reads Signalbox's resource IDs.
//
// Each resource is stored under a UUIDv7, which sorts by creation time and
// indexes well in PostgreSQL. The API shows it as a type prefix and the UUID
// in 26 lowercase Crockford base32 characters, such as
// "ws_01h455vb4pex5vsknk084sn02q". The encoding matches the TypeID
// specification, so IDs sort the same way as text and as UUIDs.
// See docs/adr/0005-http-api-conventions.md.
package id

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"
)

// Prefix names the type of resource an ID belongs to.
type Prefix string

// The prefixes of the resources in the API.
const (
	User        Prefix = "usr"
	Workspace   Prefix = "ws"
	Project     Prefix = "prj"
	Environment Prefix = "env"
	Service     Prefix = "svc"
	Spec        Prefix = "spec"
	Revision    Prefix = "rev"
	Version     Prefix = "ver"
	Invitation  Prefix = "inv"
	AccessToken Prefix = "tok"
	History     Prefix = "hist"
	Collection  Prefix = "col"
	Item        Prefix = "item"
	Run         Prefix = "run"
	Agent       Prefix = "agt"
	Job         Prefix = "job"
)

// ErrInvalid means a string isn't an ID of the expected type.
var ErrInvalid = errors.New("invalid ID")

const (
	alphabet   = "0123456789abcdefghjkmnpqrstvwxyz"
	encodedLen = 26
)

// decoding maps an ASCII character to its 5-bit value, or 0xff.
var decoding = func() [256]byte {
	var d [256]byte
	for i := range d {
		d[i] = 0xff
	}
	for i := 0; i < len(alphabet); i++ {
		d[alphabet[i]] = byte(i)
	}
	return d
}()

// New returns a new UUIDv7. It panics only if the system's random source
// fails, which the Go runtime treats as fatal anyway.
func New() uuid.UUID {
	return uuid.Must(uuid.NewV7())
}

// Format returns the API form of an ID.
func Format(p Prefix, u uuid.UUID) string {
	return string(p) + "_" + encode(u)
}

// LogAttr is a log attribute that shows an ID the way the API does, so log
// lines match what people see in responses and the web app.
func LogAttr(key string, p Prefix, u uuid.UUID) slog.Attr {
	return slog.String(key, Format(p, u))
}

// Parse reads the API form of an ID with the given prefix.
func Parse(p Prefix, s string) (uuid.UUID, error) {
	rest, ok := strings.CutPrefix(s, string(p)+"_")
	if !ok {
		return uuid.Nil, fmt.Errorf("%w: %q doesn't start with %s_", ErrInvalid, truncate(s), p)
	}
	u, ok := decode(rest)
	if !ok {
		return uuid.Nil, fmt.Errorf("%w: %q", ErrInvalid, truncate(s))
	}
	return u, nil
}

// encode writes the 128 bits of u as 26 base32 characters. The first
// character carries two zero bits of padding, so it is always 0 to 7.
func encode(u uuid.UUID) string {
	var out [encodedLen]byte
	for i := range out {
		var v byte
		for b := range 5 {
			bit := i*5 + b - 2 // index into u from the most significant bit
			v <<= 1
			if bit >= 0 && u[bit/8]&(0x80>>(bit%8)) != 0 {
				v |= 1
			}
		}
		out[i] = alphabet[v]
	}
	return string(out[:])
}

func decode(s string) (uuid.UUID, bool) {
	var u uuid.UUID
	if len(s) != encodedLen || decoding[s[0]] > 7 {
		return u, false
	}
	for i := 0; i < encodedLen; i++ {
		v := decoding[s[i]]
		if v == 0xff {
			return u, false
		}
		for b := range 5 {
			bit := i*5 + b - 2
			if bit >= 0 && v&(0x10>>b) != 0 {
				u[bit/8] |= 0x80 >> (bit % 8)
			}
		}
	}
	return u, true
}

// truncate keeps error messages short when a client sends something huge.
func truncate(s string) string {
	if len(s) > 64 {
		return s[:64] + "…"
	}
	return s
}
