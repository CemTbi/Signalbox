// SPDX-License-Identifier: Apache-2.0

package validate_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/CemTbi/Signalbox/internal/platform/validate"
)

func TestRules(t *testing.T) {
	tests := []struct {
		name     string
		check    func(*validate.Errors)
		wantCode string // "" means valid
	}{
		{"name", func(e *validate.Errors) { validate.Name(e, "/name", "Payments") }, ""},
		{"name with unicode", func(e *validate.Errors) { validate.Name(e, "/name", "Zahlungsverkehr Ünterwegs") }, ""},
		{"blank name", func(e *validate.Errors) { validate.Name(e, "/name", "   ") }, "minLength"},
		{"long name", func(e *validate.Errors) { validate.Name(e, "/name", strings.Repeat("é", 101)) }, "maxLength"},
		{"100-character name", func(e *validate.Errors) { validate.Name(e, "/name", strings.Repeat("é", 100)) }, ""},
		{"name with line break", func(e *validate.Errors) { validate.Name(e, "/name", "Pay\nments") }, "pattern"},
		{"slug", func(e *validate.Errors) { validate.Slug(e, "/slug", "payments-team-2") }, ""},
		{"empty slug", func(e *validate.Errors) { validate.Slug(e, "/slug", "") }, "minLength"},
		{"slug with capitals", func(e *validate.Errors) { validate.Slug(e, "/slug", "Payments") }, "pattern"},
		{"slug with double hyphen", func(e *validate.Errors) { validate.Slug(e, "/slug", "pay--ments") }, "pattern"},
		{"slug ending in hyphen", func(e *validate.Errors) { validate.Slug(e, "/slug", "payments-") }, "pattern"},
		{"long slug", func(e *validate.Errors) { validate.Slug(e, "/slug", strings.Repeat("a", 64)) }, "maxLength"},
		{"empty description", func(e *validate.Errors) { validate.Description(e, "/description", "") }, ""},
		{"multi-line description", func(e *validate.Errors) { validate.Description(e, "/description", "Orders.\nPayments.") }, ""},
		{"long description", func(e *validate.Errors) { validate.Description(e, "/description", strings.Repeat("x", 1001)) }, "maxLength"},
		{"description with NUL", func(e *validate.Errors) { validate.Description(e, "/description", "a\x00b") }, "pattern"},
		{"email", func(e *validate.Errors) { validate.Email(e, "/email", " ada@example.com ") }, ""},
		{"intranet email", func(e *validate.Errors) { validate.Email(e, "/email", "admin@localhost") }, ""},
		{"email with display name", func(e *validate.Errors) { validate.Email(e, "/email", "Ada <ada@example.com>") }, "format"},
		{"not an email", func(e *validate.Errors) { validate.Email(e, "/email", "ada") }, "format"},
		{"password", func(e *validate.Errors) { validate.NewPassword(e, "/password", "correct horse battery") }, ""},
		{"short password", func(e *validate.Errors) { validate.NewPassword(e, "/password", "hunter2") }, "minLength"},
		{"12-character password", func(e *validate.Errors) { validate.NewPassword(e, "/password", "abcdefghijkl") }, ""},
		{"long password", func(e *validate.Errors) { validate.NewPassword(e, "/password", strings.Repeat("x", 1025)) }, "maxLength"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var errs validate.Errors
			tt.check(&errs)
			switch {
			case tt.wantCode == "" && len(errs) > 0:
				t.Errorf("got %v, want valid", errs)
			case tt.wantCode != "" && (len(errs) != 1 || errs[0].Code != tt.wantCode):
				t.Errorf("got %v, want one %s error", errs, tt.wantCode)
			}
		})
	}
}

func TestNameAndEmailAreTrimmed(t *testing.T) {
	var errs validate.Errors
	if got := validate.Name(&errs, "/name", "  Payments  "); got != "Payments" {
		t.Errorf("Name = %q", got)
	}
	if got := validate.Email(&errs, "/email", "\tada@example.com\n"); got != "ada@example.com" {
		t.Errorf("Email = %q", got)
	}
	if err := errs.Err(); err != nil {
		t.Errorf("unexpected errors: %v", err)
	}
}

func TestErrorsCollectEveryField(t *testing.T) {
	var errs validate.Errors
	validate.Name(&errs, "/name", "")
	validate.Slug(&errs, "/slug", "Not A Slug")
	err := errs.Err()
	var got validate.Errors
	if !errors.As(err, &got) || len(got) != 2 {
		t.Fatalf("Err() = %v, want both fields", err)
	}
	if got[0].Pointer != "/name" || got[1].Pointer != "/slug" {
		t.Errorf("pointers = %s, %s", got[0].Pointer, got[1].Pointer)
	}
	if !strings.Contains(err.Error(), "/slug: Use lowercase") {
		t.Errorf("Error() = %q", err.Error())
	}
}
