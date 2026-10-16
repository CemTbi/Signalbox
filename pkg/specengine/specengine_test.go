// SPDX-License-Identifier: Apache-2.0

package specengine_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CemTbi/Signalbox/pkg/specengine"
)

func fixture(t testing.TB, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return data
}

func parse(t *testing.T, name string) *specengine.Document {
	t.Helper()
	doc, diags := specengine.Parse(fixture(t, name), specengine.Options{SourceName: name})
	if doc == nil {
		t.Fatalf("Parse(%s) returned no document: %+v", name, diags)
	}
	return doc
}

// find returns the first diagnostic with the given code, failing the test
// with the full list when there is none.
func find(t *testing.T, diags []specengine.Diagnostic, code string) specengine.Diagnostic {
	t.Helper()
	for _, d := range diags {
		if d.Code == code {
			return d
		}
	}
	t.Fatalf("no %q diagnostic in %+v", code, diags)
	return specengine.Diagnostic{}
}

func errorsIn(diags []specengine.Diagnostic) []specengine.Diagnostic {
	var out []specengine.Diagnostic
	for _, d := range diags {
		if d.Severity == specengine.SeverityError {
			out = append(out, d)
		}
	}
	return out
}

func TestParseDetectsFormatAndVersion(t *testing.T) {
	tests := []struct {
		file     string
		format   specengine.Format
		version  string
		fileType string
	}{
		{"swagger-2.0.yaml", specengine.FormatSwagger20, "2.0", "yaml"},
		{"valid-3.0.yaml", specengine.FormatOpenAPI30, "3.0.3", "yaml"},
		{"valid-3.1.yaml", specengine.FormatOpenAPI31, "3.1.1", "yaml"},
		{"valid-3.1.json", specengine.FormatOpenAPI31, "3.1.0", "json"},
		{"valid-3.2.yaml", specengine.FormatOpenAPI32, "3.2.0", "yaml"},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			doc := parse(t, tt.file)
			if doc.Format() != tt.format || doc.Version() != tt.version || doc.FileType() != tt.fileType {
				t.Errorf("got %s %s %s, want %s %s %s",
					doc.Format(), doc.Version(), doc.FileType(), tt.format, tt.version, tt.fileType)
			}
		})
	}
}

func TestValidDocumentsHaveNoErrors(t *testing.T) {
	for _, file := range []string{
		"swagger-2.0.yaml", "valid-3.0.yaml", "valid-3.1.yaml", "valid-3.1.json", "valid-3.2.yaml", "recursive.yaml",
	} {
		t.Run(file, func(t *testing.T) {
			if errs := errorsIn(parse(t, file).Validate()); len(errs) > 0 {
				t.Errorf("Validate() reported errors: %+v", errs)
			}
		})
	}
}

func TestParseRejectsInput(t *testing.T) {
	tests := []struct {
		file       string
		code       string
		line, col  int
		messageHas string
	}{
		{"broken.yaml", "syntax", 8, 19, "Invalid YAML: mapping values are not allowed"},
		{"not-openapi.yaml", "unsupported-document", 0, 0, "isn't an OpenAPI or Swagger document"},
		{"unsupported-version.yaml", "unsupported-version", 1, 10, "3.9.0 isn't supported"},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			doc, diags := specengine.Parse(fixture(t, tt.file), specengine.Options{})
			if doc != nil {
				t.Fatalf("Parse returned a document for invalid input")
			}
			d := find(t, diags, tt.code)
			if d.Severity != specengine.SeverityError || d.Source != specengine.SourceParser {
				t.Errorf("got %s from %s, want error from parser", d.Severity, d.Source)
			}
			if d.Range.Start.Line != tt.line || d.Range.Start.Column != tt.col {
				t.Errorf("position %d:%d, want %d:%d", d.Range.Start.Line, d.Range.Start.Column, tt.line, tt.col)
			}
			if !strings.Contains(d.Message, tt.messageHas) {
				t.Errorf("message %q does not contain %q", d.Message, tt.messageHas)
			}
		})
	}
}

func TestParseRejectsEmptyInput(t *testing.T) {
	_, diags := specengine.Parse([]byte("  \n"), specengine.Options{})
	find(t, diags, "empty-document")
}

func TestValidateReportsMissingRequiredField(t *testing.T) {
	d := find(t, parse(t, "missing-title.yaml").Validate(), "structure")
	if d.Severity != specengine.SeverityError {
		t.Errorf("severity %s, want error", d.Severity)
	}
	if !strings.Contains(d.Message, "title") || d.Path != "/info" {
		t.Errorf("got %q at %q, want a message about title at /info", d.Message, d.Path)
	}
	if d.Range.Start.Line != 2 {
		t.Errorf("line %d, want 2 (the info object)", d.Range.Start.Line)
	}
}

func TestValidateReportsUnresolvedReference(t *testing.T) {
	d := find(t, parse(t, "unresolved-ref.yaml").Validate(), "unresolved-reference")
	if d.Severity != specengine.SeverityError || d.Source != specengine.SourceResolver {
		t.Errorf("got %s from %s, want error from resolver", d.Severity, d.Source)
	}
	if d.Range.Start.Line != 14 || d.Range.Start.Column != 17 {
		t.Errorf("position %d:%d, want 14:17 (the $ref value)", d.Range.Start.Line, d.Range.Start.Column)
	}
	if !strings.Contains(d.Message, "#/components/schemas/Missing") {
		t.Errorf("message %q does not name the missing component", d.Message)
	}
}

func TestValidateRejectsInfiniteCircularReference(t *testing.T) {
	d := find(t, parse(t, "infinite.yaml").Validate(), "infinite-circular-reference")
	if d.Severity != specengine.SeverityError || d.Range.Start.Line == 0 {
		t.Errorf("got %s at line %d, want an error with a position", d.Severity, d.Range.Start.Line)
	}
}

func TestLintFindsQualityIssues(t *testing.T) {
	diags, err := parse(t, "unresolved-ref.yaml").Lint(context.Background(), specengine.RuleSetRecommended)
	if err != nil {
		t.Fatalf("Lint: %v", err)
	}
	d := find(t, diags, "operation-operationId")
	if d.Severity != specengine.SeverityWarning {
		t.Errorf("operation-operationId is %s, want warning (aligned with Spectral)", d.Severity)
	}
	if d.Range.Start.Line != 7 || d.Path != "$.paths['/things'].get" {
		t.Errorf("got line %d path %q, want line 7 at the GET operation", d.Range.Start.Line, d.Path)
	}
	for _, d := range diags {
		if d.Source != specengine.SourceLinter {
			t.Errorf("Lint returned a %s diagnostic: %+v", d.Source, d)
		}
		if d.Range.Start.Line == 0 {
			t.Errorf("lint result without a position: %+v", d)
		}
		if d.Code == "resolving-references" || strings.Contains(d.Message, "does not exist") {
			t.Errorf("Lint repeats a reference problem that Validate reports: %+v", d)
		}
	}
}

func TestLintWithoutRuleSetsDoesNothing(t *testing.T) {
	diags, err := parse(t, "valid-3.0.yaml").Lint(context.Background())
	if err != nil || len(diags) != 0 {
		t.Fatalf("Lint() = %v, %v; want no diagnostics", diags, err)
	}
}

func TestLintOWASPFindsSecurityIssues(t *testing.T) {
	diags, err := parse(t, "valid-3.1.yaml").Lint(context.Background(), specengine.RuleSetOWASP)
	if err != nil {
		t.Fatalf("Lint: %v", err)
	}
	found := false
	for _, d := range diags {
		if strings.HasPrefix(d.Code, "owasp-") {
			found = true
		}
	}
	if !found {
		t.Errorf("OWASP rules found nothing in a spec with no security schemes: %+v", diags)
	}
}

func TestCheckCombinesAllStages(t *testing.T) {
	rep, err := specengine.Check(context.Background(), fixture(t, "unresolved-ref.yaml"),
		specengine.Options{SourceName: "unresolved-ref.yaml"}, specengine.RuleSetRecommended)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if rep.Format != specengine.FormatOpenAPI31 || rep.Version != "3.1.0" {
		t.Errorf("got %s %s, want openapi-3.1 3.1.0", rep.Format, rep.Version)
	}
	if rep.Counts.Errors != 1 || rep.Counts.Warnings == 0 {
		t.Errorf("counts %+v, want exactly 1 error and some warnings", rep.Counts)
	}
	for i := 1; i < len(rep.Diagnostics); i++ {
		if rep.Diagnostics[i].Range.Start.Line < rep.Diagnostics[i-1].Range.Start.Line {
			t.Fatalf("diagnostics are not sorted by line: %+v", rep.Diagnostics)
		}
	}
}

func TestCheckReportsParseFailureWithoutError(t *testing.T) {
	rep, err := specengine.Check(context.Background(), fixture(t, "broken.yaml"), specengine.Options{})
	if err != nil {
		t.Fatalf("Check returned an engine error for a document problem: %v", err)
	}
	if rep.Counts.Errors != 1 || rep.Format != "" {
		t.Errorf("got %+v, want one parse error and no format", rep)
	}
}

func TestCheckStopsWhenContextIsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := specengine.Check(ctx, fixture(t, "valid-3.1.yaml"), specengine.Options{}, specengine.RuleSetRecommended)
	if err == nil {
		t.Fatal("Check succeeded with a cancelled context")
	}
}

func TestParseRuleSets(t *testing.T) {
	tests := []struct {
		in      string
		want    []specengine.RuleSet
		wantErr bool
	}{
		{"recommended", []specengine.RuleSet{specengine.RuleSetRecommended}, false},
		{" recommended , OWASP,recommended", []specengine.RuleSet{specengine.RuleSetRecommended, specengine.RuleSetOWASP}, false},
		{"none", nil, false},
		{"", nil, false},
		{"strict", nil, true},
	}
	for _, tt := range tests {
		got, err := specengine.ParseRuleSets(tt.in)
		if (err != nil) != tt.wantErr {
			t.Errorf("ParseRuleSets(%q) error = %v", tt.in, err)
			continue
		}
		if strings.Join(toStrings(got), ",") != strings.Join(toStrings(tt.want), ",") {
			t.Errorf("ParseRuleSets(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func toStrings(sets []specengine.RuleSet) []string {
	out := make([]string, len(sets))
	for i, s := range sets {
		out[i] = string(s)
	}
	return out
}

func TestSeverity(t *testing.T) {
	for in, want := range map[string]specengine.Severity{
		"error": specengine.SeverityError, "WARN": specengine.SeverityWarning,
		"warning": specengine.SeverityWarning, "info": specengine.SeverityInfo, "hint": specengine.SeverityHint,
	} {
		got, err := specengine.ParseSeverity(in)
		if err != nil || got != want {
			t.Errorf("ParseSeverity(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	if _, err := specengine.ParseSeverity("fatal"); err == nil {
		t.Error("ParseSeverity accepted an unknown severity")
	}
	if !specengine.SeverityError.AtLeast(specengine.SeverityWarning) || specengine.SeverityInfo.AtLeast(specengine.SeverityWarning) {
		t.Error("AtLeast ranks severities incorrectly")
	}
}
