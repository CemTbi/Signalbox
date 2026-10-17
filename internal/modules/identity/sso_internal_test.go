// SPDX-License-Identifier: Apache-2.0

package identity

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestDisplayNameFallsBack(t *testing.T) {
	for _, tt := range []struct {
		claims ssoClaims
		want   string
	}{
		{ssoClaims{Name: "Grace Hopper", PreferredUsername: "ghopper"}, "Grace Hopper"},
		{ssoClaims{GivenName: "Grace", FamilyName: "Hopper"}, "Grace Hopper"},
		{ssoClaims{PreferredUsername: "ghopper", Email: "g@example.com"}, "ghopper"},
		{ssoClaims{Email: "grace.hopper@example.com"}, "grace.hopper"},
		{ssoClaims{Name: "  \tGrace\nHopper  "}, "GraceHopper"},
		{ssoClaims{}, "Signalbox user"},
	} {
		if got := tt.claims.displayName(); got != tt.want {
			t.Errorf("displayName(%+v) = %q, want %q", tt.claims, got, tt.want)
		}
	}
	long := ssoClaims{Name: strings.Repeat("é", 150)}.displayName()
	if utf8.RuneCountInString(long) != 100 || !utf8.ValidString(long) {
		t.Errorf("a 150-character name became %d characters", utf8.RuneCountInString(long))
	}
}

func TestEmailVerifiedAcceptsStrings(t *testing.T) {
	for in, want := range map[string]bool{`true`: true, `false`: false, `"true"`: true, `"TRUE"`: true, `"false"`: false, `1`: false} {
		var c ssoClaims
		if err := json.Unmarshal([]byte(`{"email_verified":`+in+`}`), &c); err != nil {
			t.Fatal(err)
		}
		if bool(c.EmailVerified) != want {
			t.Errorf("email_verified %s = %v, want %v", in, c.EmailVerified, want)
		}
	}
}
