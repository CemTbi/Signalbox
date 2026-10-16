// SPDX-License-Identifier: Apache-2.0

// Package specengine parses, validates and lints OpenAPI documents.
//
// It is the single place where Signalbox decides what a specification means.
// The server roles and the signalbox CLI both use it, so the editor, CI and
// production always agree.
//
// A typical caller runs the three stages in order:
//
//	doc, diags := specengine.Parse(data, specengine.Options{SourceName: "openapi.yaml"})
//	if doc != nil {
//		diags = append(diags, doc.Validate()...)
//		lint, err := doc.Lint(ctx, specengine.RuleSetRecommended)
//		...
//	}
//
// or calls Check, which does the same and returns a Report.
//
// Every Diagnostic carries a 1-based line and column, so editors can place a
// marker on the exact spot.
package specengine
