// SPDX-License-Identifier: Apache-2.0

// Package validate checks the fields of a request and collects every
// problem, so a client can fix them all in one go. The rules mirror the
// schemas in api/openapi.yaml, and the codes name the JSON Schema keyword
// that failed.
package validate

import (
	"fmt"
	"net/mail"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// FieldError is one invalid field.
type FieldError struct {
	// Pointer is a JSON Pointer (RFC 6901) to the field, such as "/slug".
	Pointer string
	// Code is the rule that failed: required, minLength, maxLength,
	// pattern, format, type or additionalProperties.
	Code string
	// Detail tells a person what is wrong and how to fix it.
	Detail string
}

// Errors is every invalid field in a request. It is an error when it isn't
// empty.
type Errors []FieldError

func (e Errors) Error() string {
	parts := make([]string, len(e))
	for i, f := range e {
		parts[i] = f.Pointer + ": " + f.Detail
	}
	return "invalid request: " + strings.Join(parts, "; ")
}

// Add records an invalid field.
func (e *Errors) Add(pointer, code, detail string) {
	*e = append(*e, FieldError{Pointer: pointer, Code: code, Detail: detail})
}

// Err returns the errors as an error, or nil when there are none.
func (e Errors) Err() error {
	if len(e) == 0 {
		return nil
	}
	return e
}

// Limits shared with api/openapi.yaml.
const (
	MaxName         = 100
	MaxSlug         = 63
	MaxDescription  = 1000
	MaxEmail        = 254
	MinPassword     = 12
	MaxPassword     = 1024
	minEmailLength  = 3
	controlsAllowed = "\n\t"
)

var slugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// Name checks a display name and returns it without surrounding spaces.
func Name(errs *Errors, pointer, v string) string {
	v = strings.TrimSpace(v)
	switch {
	case v == "":
		errs.Add(pointer, "minLength", "Enter a name.")
	case utf8.RuneCountInString(v) > MaxName:
		errs.Add(pointer, "maxLength", fmt.Sprintf("Use at most %d characters.", MaxName))
	case hasControl(v, ""):
		errs.Add(pointer, "pattern", "Remove line breaks and other control characters.")
	}
	return v
}

// Slug checks a URL-safe short name.
func Slug(errs *Errors, pointer, v string) string {
	switch {
	case v == "":
		errs.Add(pointer, "minLength", "Enter a slug.")
	case len(v) > MaxSlug:
		errs.Add(pointer, "maxLength", fmt.Sprintf("Use at most %d characters.", MaxSlug))
	case !slugPattern.MatchString(v):
		errs.Add(pointer, "pattern", "Use lowercase letters, digits and single hyphens, such as payments-team.")
	}
	return v
}

// Description checks a plain-text description and returns it without
// surrounding spaces. It may be empty.
func Description(errs *Errors, pointer, v string) string {
	v = strings.TrimSpace(v)
	switch {
	case utf8.RuneCountInString(v) > MaxDescription:
		errs.Add(pointer, "maxLength", fmt.Sprintf("Use at most %d characters.", MaxDescription))
	case hasControl(v, controlsAllowed):
		errs.Add(pointer, "pattern", "Remove control characters.")
	}
	return v
}

// Email checks an email address and returns it without surrounding spaces.
func Email(errs *Errors, pointer, v string) string {
	v = strings.TrimSpace(v)
	if len(v) < minEmailLength || len(v) > MaxEmail {
		errs.Add(pointer, "format", "Enter an email address, such as ada@example.com.")
		return v
	}
	addr, err := mail.ParseAddress(v)
	if err != nil || addr.Address != v || addr.Name != "" {
		errs.Add(pointer, "format", "Enter an email address, such as ada@example.com.")
	}
	return v
}

// NewPassword checks a password someone is choosing. Passwords aren't
// trimmed: every character counts.
func NewPassword(errs *Errors, pointer, v string) {
	switch n := utf8.RuneCountInString(v); {
	case n < MinPassword:
		errs.Add(pointer, "minLength", fmt.Sprintf("Use at least %d characters. A few unrelated words make a strong password.", MinPassword))
	case n > MaxPassword:
		errs.Add(pointer, "maxLength", fmt.Sprintf("Use at most %d characters.", MaxPassword))
	case !utf8.ValidString(v):
		errs.Add(pointer, "format", "The password contains invalid characters.")
	}
}

func hasControl(s, allowed string) bool {
	for _, r := range s {
		if unicode.IsControl(r) && !strings.ContainsRune(allowed, r) {
			return true
		}
	}
	return false
}
