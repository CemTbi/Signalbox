// SPDX-License-Identifier: Apache-2.0

// Package convert turns Postman collections and environments and cURL
// commands into Signalbox's collections, variables and requests, and
// collections back into Postman's format. It touches no database: the
// client module stores what it returns. Every conversion comes with a
// report of what will run differently and what was left out. See
// docs/adr/0031-postman-import.md.
package convert

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Level says how much a note matters.
type Level string

// Levels of notes.
const (
	// Info is something worth knowing that doesn't change how anything
	// runs, such as saved examples that weren't kept.
	Info Level = "info"
	// Differs is something that will run differently than in Postman or
	// curl, such as a script that uses something the sandbox lacks.
	Differs Level = "differs"
	// Omitted is something left out, such as a file body.
	Omitted Level = "omitted"
)

// Note is one line of a report.
type Note struct {
	// Item names where it is, as folder names and the request's name
	// joined by " / "; empty for the whole collection.
	Item    string
	Level   Level
	Message string
}

// Report is what a conversion found.
type Report struct {
	Folders   int
	Requests  int
	Variables int
	Notes     []Note
}

// Clean reports whether nothing was left out and nothing will run
// differently.
func (r *Report) Clean() bool {
	for _, n := range r.Notes {
		if n.Level != Info {
			return false
		}
	}
	return true
}

func (r *Report) add(item string, level Level, format string, args ...any) {
	r.Notes = append(r.Notes, Note{Item: item, Level: level, Message: fmt.Sprintf(format, args...)})
}

// Header is a request header. Unticked headers are kept but not sent.
type Header struct {
	Key     string
	Value   string
	Enabled bool
}

// Auth is a request's auth, as the client module applies it.
type Auth struct {
	Type     string
	Username string
	Password string
	Token    string
	Key      string
	Value    string
	In       string
}

// Request is a request to save.
type Request struct {
	Method  string
	URL     string
	Headers []Header
	Body    string
	Auth    *Auth
}

// Variable is a collection or environment variable.
type Variable struct {
	Key    string
	Value  string
	Secret bool
}

// Scripts are JavaScript run before a request and after its response.
type Scripts struct {
	PreRequest string
	Test       string
}

// Item is a folder, with items, or a request.
type Item struct {
	Name        string
	Description string
	// Request is nil for a folder.
	Request *Request
	Scripts Scripts
	Items   []Item
}

// Collection is a collection to store.
type Collection struct {
	Name        string
	Description string
	Variables   []Variable
	Scripts     Scripts
	Items       []Item
}

// Limits match the client module's, so anything converted can be stored.
const (
	MaxItems       = 10_000
	MaxDepth       = 20
	MaxNameLength  = 200
	MaxDescription = 10_000
	// MaxCollectionName and MaxCollectionDescription bound a collection's
	// own name and description.
	MaxCollectionName        = 100
	MaxCollectionDescription = 2000
	MaxHeaders               = 200
	MaxURL                   = 8192
	MaxBody                  = 10 << 20
	MaxValue                 = 64 << 10
	MaxVariables             = 500
	// MaxCollectionVariables bounds the size of all a collection's
	// variables together, keys and values, well inside what the database
	// keeps.
	MaxCollectionVariables = 2 << 20
	// MaxFileSize bounds a Postman file.
	MaxFileSize = 25 << 20
)

// supportedBuiltins are the {{$name}} variables Signalbox fills in.
var supportedBuiltins = map[string]bool{"$guid": true, "$randomUUID": true, "$timestamp": true, "$isoTimestamp": true, "$randomInt": true}

var builtinPattern = regexp.MustCompile(`\{\{\s*(\$[A-Za-z0-9_]+)\s*\}\}`)

// unsupportedBuiltins lists the {{$name}} variables in s that Signalbox
// doesn't fill in, such as Postman's {{$randomEmail}}.
func unsupportedBuiltins(s string) []string {
	var out []string
	for _, m := range builtinPattern.FindAllStringSubmatch(s, -1) {
		if !supportedBuiltins[m[1]] {
			out = append(out, m[1])
		}
	}
	return out
}

// ValidKey reports whether a variable name can be stored: no spaces or
// braces, and not starting with $.
func ValidKey(key string) bool {
	if key == "" || utf8.RuneCountInString(key) > 200 || strings.HasPrefix(key, "$") || !utf8.ValidString(key) {
		return false
	}
	return !strings.ContainsAny(key, "{} \t\r\n\f\v")
}

// shorten cuts s to at most n characters, and says whether it did.
func shorten(s string, n int) (string, bool) {
	if utf8.RuneCountInString(s) <= n {
		return s, false
	}
	return string([]rune(s)[:n]), true
}
