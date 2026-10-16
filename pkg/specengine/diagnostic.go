// SPDX-License-Identifier: Apache-2.0

package specengine

import (
	"fmt"
	"sort"
	"strings"
)

// Severity ranks how serious a diagnostic is.
type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
	SeverityInfo    Severity = "info"
	SeverityHint    Severity = "hint"
)

// ParseSeverity accepts the severity names used by the CLI and the API.
// "warn" is accepted as an alias for "warning".
func ParseSeverity(s string) (Severity, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "error":
		return SeverityError, nil
	case "warning", "warn":
		return SeverityWarning, nil
	case "info":
		return SeverityInfo, nil
	case "hint":
		return SeverityHint, nil
	}
	return "", fmt.Errorf("unknown severity %q: use error, warning, info or hint", s)
}

func (s Severity) rank() int {
	switch s {
	case SeverityError:
		return 0
	case SeverityWarning:
		return 1
	case SeverityInfo:
		return 2
	default:
		return 3
	}
}

// AtLeast reports whether s is at least as severe as minimum.
func (s Severity) AtLeast(minimum Severity) bool {
	return s.rank() <= minimum.rank()
}

// Source names the stage of the engine that produced a diagnostic.
type Source string

const (
	SourceParser    Source = "parser"
	SourceValidator Source = "validator"
	SourceResolver  Source = "resolver"
	SourceLinter    Source = "linter"
)

// Position is a 1-based line and column in the source document.
// Zero values mean the position is unknown.
type Position struct {
	Line   int `json:"line"`
	Column int `json:"column"`
}

// Range spans from Start to End.
type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

func pointAt(line, column int) Range {
	p := Position{Line: max(line, 0), Column: max(column, 0)}
	return Range{Start: p, End: p}
}

// Diagnostic is one problem found in a document.
type Diagnostic struct {
	Severity Severity `json:"severity"`
	Source   Source   `json:"source"`
	// Code identifies the kind of problem, for example "unresolved-reference"
	// or a lint rule ID such as "operation-operationId".
	Code    string `json:"code"`
	Message string `json:"message"`
	// Path locates the problem in the document, as a JSON Pointer for
	// validation problems and as a JSONPath for lint results.
	Path     string `json:"path,omitempty"`
	Range    Range  `json:"range"`
	HowToFix string `json:"howToFix,omitempty"`
}

// Counts tallies diagnostics by severity.
type Counts struct {
	Errors   int `json:"errors"`
	Warnings int `json:"warnings"`
	Infos    int `json:"infos"`
	Hints    int `json:"hints"`
}

// CountDiagnostics returns how many diagnostics there are of each severity.
func CountDiagnostics(diags []Diagnostic) Counts {
	var c Counts
	for _, d := range diags {
		switch d.Severity {
		case SeverityError:
			c.Errors++
		case SeverityWarning:
			c.Warnings++
		case SeverityInfo:
			c.Infos++
		default:
			c.Hints++
		}
	}
	return c
}

// SortDiagnostics orders diagnostics by position, then severity, then code,
// so output is stable from run to run.
func SortDiagnostics(diags []Diagnostic) {
	sort.SliceStable(diags, func(i, j int) bool {
		a, b := diags[i], diags[j]
		if a.Range.Start.Line != b.Range.Start.Line {
			return a.Range.Start.Line < b.Range.Start.Line
		}
		if a.Range.Start.Column != b.Range.Start.Column {
			return a.Range.Start.Column < b.Range.Start.Column
		}
		if a.Severity.rank() != b.Severity.rank() {
			return a.Severity.rank() < b.Severity.rank()
		}
		if a.Code != b.Code {
			return a.Code < b.Code
		}
		return a.Message < b.Message
	})
}

// dedupe drops repeated diagnostics. The input must be sorted.
func dedupe(diags []Diagnostic) []Diagnostic {
	out := diags[:0]
	for _, d := range diags {
		if len(out) > 0 && d == out[len(out)-1] {
			continue
		}
		out = append(out, d)
	}
	return out
}
