// SPDX-License-Identifier: Apache-2.0

package client

import (
	"math/rand/v2"
	"slices"
	"testing"
)

func TestRanksSortBetweenTheirNeighbours(t *testing.T) {
	cases := [][2]string{{"", ""}, {"", "V"}, {"V", ""}, {"V", "W"}, {"V", "V1"}, {"a0V", "a1"}, {"z", ""}, {"", "01"}, {"0001", "0002"}}
	for _, c := range cases {
		r, err := rankBetween(c[0], c[1])
		if err != nil {
			t.Errorf("between(%q, %q): %v", c[0], c[1], err)
			continue
		}
		if r <= c[0] || (c[1] != "" && r >= c[1]) || !validRank(r) {
			t.Errorf("between(%q, %q) = %q", c[0], c[1], r)
		}
	}
	for _, bad := range [][2]string{{"W", "V"}, {"V", "V"}, {"V0", ""}, {"", "a b"}} {
		if _, err := rankBetween(bad[0], bad[1]); err == nil {
			t.Errorf("between(%q, %q) didn't fail", bad[0], bad[1])
		}
	}
}

// Inserting anywhere, many times, keeps every rank unique and in order, and
// ranks stay short.
func TestRanksStayOrderedUnderRandomInserts(t *testing.T) {
	rnd := rand.New(rand.NewPCG(1, 2))
	ranks := []string{}
	for range 2000 {
		i := rnd.IntN(len(ranks) + 1)
		a, b := "", ""
		if i > 0 {
			a = ranks[i-1]
		}
		if i < len(ranks) {
			b = ranks[i]
		}
		r, err := rankBetween(a, b)
		if err != nil {
			t.Fatalf("between(%q, %q): %v", a, b, err)
		}
		ranks = slices.Insert(ranks, i, r)
	}
	if !slices.IsSorted(ranks) {
		t.Fatal("ranks out of order")
	}
	for i := 1; i < len(ranks); i++ {
		if ranks[i] == ranks[i-1] {
			t.Fatalf("duplicate rank %q", ranks[i])
		}
	}
	// Appending, the common case, grows a character per 37 appends or so:
	// short for any real folder.
	last := ""
	for range 1000 {
		last, _ = rankBetween(last, "")
	}
	if len(last) > 40 {
		t.Errorf("1,000 appends gave a rank of %d characters", len(last))
	}
}
