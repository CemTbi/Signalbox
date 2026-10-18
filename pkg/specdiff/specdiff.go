// SPDX-License-Identifier: Apache-2.0

// Package specdiff compares two versions of an OpenAPI document and reports
// what changed for the API's clients: breaking changes as errors, changes
// that may break some clients as warnings, and the rest as information.
//
// The checks are oasdiff's, which name each change's operation and the
// lines it is on in either document. Swagger 2.0 documents are converted
// to OpenAPI 3 first. See docs/adr/0023-breaking-changes.md.
package specdiff

import (
	"fmt"
	"strings"

	"github.com/oasdiff/oasdiff/checker"
	"github.com/oasdiff/oasdiff/diff"
	"github.com/oasdiff/oasdiff/load"

	"github.com/CemTbi/Signalbox/pkg/specload"
)

// ErrTooLarge means a document's schemas, with references expanded, are
// too large to compare. Comparing walks every expanded schema, so a schema
// shared many times over could otherwise take minutes.
var ErrTooLarge = specload.ErrTooLarge

// maxCompared bounds the schemas, counted with references expanded, that a
// comparison walks in each document. 100,000 take about a second.
const maxCompared = 100_000

// Level is how a change affects the API's clients.
type Level string

const (
	// LevelError is a breaking change: clients that worked may fail.
	LevelError Level = "error"
	// LevelWarning is a change that may break some clients.
	LevelWarning Level = "warning"
	// LevelInfo is a change that doesn't break clients.
	LevelInfo Level = "info"
)

// AtLeast reports whether l is as severe as other or more.
func (l Level) AtLeast(other Level) bool { return l.rank() >= other.rank() }

func (l Level) rank() int {
	switch l {
	case LevelError:
		return 3
	case LevelWarning:
		return 2
	case LevelInfo:
		return 1
	}
	return 0
}

// ParseLevel reads a level name: error, warning or info.
func ParseLevel(s string) (Level, error) {
	switch l := Level(strings.ToLower(strings.TrimSpace(s))); l {
	case LevelError, LevelWarning, LevelInfo:
		return l, nil
	}
	return "", fmt.Errorf("unknown level %q: use error, warning or info", s)
}

// Location is where a change is in one of the documents.
type Location struct {
	Line   int `json:"line"`
	Column int `json:"column,omitempty"`
}

// Change is one difference between the documents that matters to clients.
type Change struct {
	// ID names the check, such as request-parameter-became-required.
	ID    string `json:"id"`
	Level Level  `json:"level"`
	// Method and Path name the operation, when the change is in one.
	Method      string `json:"method,omitempty"`
	Path        string `json:"path,omitempty"`
	OperationID string `json:"operationId,omitempty"`
	// Message says what changed, such as "the query request parameter
	// limit became required".
	Message string `json:"message"`
	// Comment explains why the change matters, when the check has more to
	// say.
	Comment string `json:"comment,omitempty"`
	// Base and Head are where the change is in each document; a change
	// that removes something has only Base, one that adds only Head.
	Base *Location `json:"base,omitempty"`
	Head *Location `json:"head,omitempty"`
}

// Counts are the number of changes at each level.
type Counts struct {
	Errors   int `json:"errors"`
	Warnings int `json:"warnings"`
	Infos    int `json:"infos"`
}

// Report is what changed between two documents.
type Report struct {
	Changes []Change `json:"changes"`
	Counts  Counts   `json:"counts"`
}

// Document is one of the two documents to compare.
type Document = specload.Document

// Options control what a comparison may read.
type Options struct {
	// AllowRemoteReferences lets $ref fetch http and https URLs.
	AllowRemoteReferences bool
}

// Compare reports what changed from base to head.
func Compare(base, head Document, opts Options) (*Report, error) {
	loadOpts := specload.Options{AllowRemoteReferences: opts.AllowRemoteReferences, MaxSchemas: maxCompared}
	b, err := specload.Load(base, loadOpts)
	if err != nil {
		return nil, fmt.Errorf("specdiff: %w", err)
	}
	h, err := specload.Load(head, loadOpts)
	if err != nil {
		return nil, fmt.Errorf("specdiff: %w", err)
	}
	d, sources, err := diff.GetWithOperationsSourcesMap(diff.NewConfig(),
		&load.SpecInfo{Url: base.Name, Spec: b}, &load.SpecInfo{Url: head.Name, Spec: h})
	if err != nil {
		return nil, fmt.Errorf("specdiff: compare: %w", err)
	}
	changes := checker.CheckBackwardCompatibilityUntilLevel(checker.NewConfig(checker.GetAllChecks()), d, sources, checker.INFO)
	localizer := checker.NewDefaultLocalizer()
	report := &Report{Changes: make([]Change, 0, len(changes))}
	for _, c := range changes {
		ch := Change{
			ID:          c.GetId(),
			Level:       level(c.GetLevel()),
			Method:      c.GetOperation(),
			Path:        c.GetPath(),
			OperationID: c.GetOperationId(),
			Message:     strings.TrimSpace(c.GetUncolorizedText(localizer)),
			Comment:     strings.TrimSpace(c.GetComment(localizer)),
			Base:        location(c.GetBaseSource()),
			Head:        location(c.GetRevisionSource()),
		}
		switch ch.Level {
		case LevelError:
			report.Counts.Errors++
		case LevelWarning:
			report.Counts.Warnings++
		default:
			report.Counts.Infos++
		}
		report.Changes = append(report.Changes, ch)
	}
	return report, nil
}

func level(l checker.Level) Level {
	switch l {
	case checker.ERR:
		return LevelError
	case checker.WARN:
		return LevelWarning
	}
	return LevelInfo
}

func location(s *checker.Source) *Location {
	if s == nil || s.Line <= 0 {
		return nil
	}
	return &Location{Line: s.Line, Column: s.Column}
}
