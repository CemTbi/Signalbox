// SPDX-License-Identifier: Apache-2.0

package client

import (
	"errors"
	"strings"
)

// Ranks order the items of a folder: strings that sort byte by byte, so an
// item moves by taking a rank between its new neighbours, without
// renumbering the others. This is fractional indexing, as in David
// Greenspan's "Implementing Fractional Indexing": base-62 digits in ASCII
// order, and no rank ends in the lowest digit, so there's always room
// between two ranks.
const rankDigits = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

var errBadRank = errors.New("client: invalid rank")

// rankBetween returns a rank after a and before b. An empty a means the
// start of the list, an empty b its end.
func rankBetween(a, b string) (string, error) {
	if !validRank(a) || !validRank(b) || (b != "" && a >= b) {
		return "", errBadRank
	}
	if a != "" && b == "" {
		return after(a), nil
	}
	return midpoint(a, b, b != ""), nil
}

// after is a short rank after a, for appending, the usual case: the first
// digit with room goes up by one, and the rest is dropped. The midpoint
// alone would add a character every few appends.
func after(a string) string {
	for i := 0; i < len(a); i++ {
		if d := strings.IndexByte(rankDigits, a[i]); d < len(rankDigits)-1 {
			return a[:i] + string(rankDigits[d+1])
		}
	}
	return midpoint(a, "", false)
}

func validRank(r string) bool {
	if r == "" {
		return true
	}
	if r[len(r)-1] == rankDigits[0] {
		return false
	}
	for i := 0; i < len(r); i++ {
		if strings.IndexByte(rankDigits, r[i]) < 0 {
			return false
		}
	}
	return true
}

// midpoint finds a string between a and b, where hasB says whether b
// bounds it at all.
func midpoint(a, b string, hasB bool) string {
	if hasB {
		// Keep the prefix they share, reading a missing digit of a as 0.
		n := 0
		for n < len(b) && digitAt(a, n) == b[n] {
			n++
		}
		if n > 0 {
			rest := ""
			if n < len(a) {
				rest = a[n:]
			}
			return b[:n] + midpoint(rest, b[n:], true)
		}
	}
	lo := 0
	if a != "" {
		lo = strings.IndexByte(rankDigits, a[0])
	}
	hi := len(rankDigits)
	if hasB {
		hi = strings.IndexByte(rankDigits, b[0])
	}
	if hi-lo > 1 {
		return string(rankDigits[(lo+hi+1)/2])
	}
	// The first digits are neighbours.
	if hasB && len(b) > 1 {
		return b[:1]
	}
	rest := ""
	if a != "" {
		rest = a[1:]
	}
	return string(rankDigits[lo]) + midpoint(rest, "", false)
}

func digitAt(s string, i int) byte {
	if i < len(s) {
		return s[i]
	}
	return rankDigits[0]
}
