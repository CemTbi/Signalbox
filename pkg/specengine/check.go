// SPDX-License-Identifier: Apache-2.0

package specengine

import "context"

// Report is the combined result of parsing, validating and linting a document.
type Report struct {
	Format      Format       `json:"format,omitempty"`
	Version     string       `json:"version,omitempty"`
	Diagnostics []Diagnostic `json:"diagnostics"`
	Counts      Counts       `json:"counts"`
}

// Check parses, validates and lints data in one call. Lint runs only when at
// least one rule set is given. Problems in the document come back as
// diagnostics in the report; the error is reserved for failures of the
// engine itself, such as a cancelled context.
func Check(ctx context.Context, data []byte, opts Options, sets ...RuleSet) (Report, error) {
	doc, diags := Parse(data, opts)
	if doc == nil {
		return newReport("", "", diags), nil
	}

	diags = append(diags, doc.Validate()...)
	lint, err := doc.Lint(ctx, sets...)
	if err != nil {
		return Report{}, err
	}
	diags = append(diags, lint...)
	SortDiagnostics(diags)
	return newReport(doc.Format(), doc.Version(), dedupe(diags)), nil
}

func newReport(format Format, version string, diags []Diagnostic) Report {
	if diags == nil {
		diags = []Diagnostic{}
	}
	return Report{
		Format:      format,
		Version:     version,
		Diagnostics: diags,
		Counts:      CountDiagnostics(diags),
	}
}
