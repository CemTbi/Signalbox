// SPDX-License-Identifier: Apache-2.0

package id_test

import (
	"errors"
	"regexp"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/CemTbi/Signalbox/internal/platform/id"
)

// Vectors from the TypeID specification's valid.yml.
func TestFormatMatchesTypeIDSpec(t *testing.T) {
	tests := []struct {
		uuid, want string
	}{
		{"00000000-0000-0000-0000-000000000000", "ws_00000000000000000000000000"},
		{"00000000-0000-0000-0000-000000000001", "ws_00000000000000000000000001"},
		{"00000000-0000-0000-0000-00000000000a", "ws_0000000000000000000000000a"},
		{"00000000-0000-0000-0000-000000000010", "ws_0000000000000000000000000g"},
		{"00000000-0000-0000-0000-000000000020", "ws_00000000000000000000000010"},
		{"ffffffff-ffff-ffff-ffff-ffffffffffff", "ws_7zzzzzzzzzzzzzzzzzzzzzzzzz"},
		{"0110c853-1d09-52d8-d73e-1194e95b5f19", "ws_0123456789abcdefghjkmnpqrs"},
		{"01890a5d-ac96-774b-bcce-b302099a8057", "ws_01h455vb4pex5vsknk084sn02q"},
	}
	for _, tt := range tests {
		u := uuid.MustParse(tt.uuid)
		if got := id.Format(id.Workspace, u); got != tt.want {
			t.Errorf("Format(%s) = %s, want %s", tt.uuid, got, tt.want)
		}
		back, err := id.Parse(id.Workspace, tt.want)
		if err != nil || back != u {
			t.Errorf("Parse(%s) = %s, %v, want %s", tt.want, back, err, tt.uuid)
		}
	}
}

func TestParseRejectsMalformedIDs(t *testing.T) {
	for _, s := range []string{
		"",
		"ws_",
		"prj_01h455vb4pex5vsknk084sn02q",   // wrong type
		"ws01h455vb4pex5vsknk084sn02q",     // no separator
		"ws_01h455vb4pex5vsknk084sn02",     // too short
		"ws_01h455vb4pex5vsknk084sn02qq",   // too long
		"ws_81h455vb4pex5vsknk084sn02q",    // more than 128 bits
		"ws_01H455VB4PEX5VSKNK084SN02Q",    // uppercase
		"ws_01h455vb4pex5vsknk084sn0iu",    // i and u aren't in the alphabet
		"ws_01h455vb4pex5vsknk084sn0\x00q", // control character
	} {
		if _, err := id.Parse(id.Workspace, s); !errors.Is(err, id.ErrInvalid) {
			t.Errorf("Parse(%q) error = %v, want ErrInvalid", s, err)
		}
	}
}

// The OpenAPI contract describes IDs with this pattern.
var contractPattern = regexp.MustCompile(`^ws_[0-9a-hjkmnp-tv-z]{26}$`)

func TestNewIDsMatchTheContractAndSortByTime(t *testing.T) {
	ids := make([]string, 1000)
	for i := range ids {
		ids[i] = id.Format(id.Workspace, id.New())
		if !contractPattern.MatchString(ids[i]) {
			t.Fatalf("%s doesn't match the contract's pattern", ids[i])
		}
	}
	if !slices.IsSorted(ids) {
		t.Error("IDs created one after another don't sort in creation order")
	}
}
