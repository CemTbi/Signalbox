// SPDX-License-Identifier: Apache-2.0

package specengine

import (
	"errors"
	"strings"

	"github.com/pb33f/libopenapi-validator/schema_validation"
	"github.com/pb33f/libopenapi/index"
	"github.com/pb33f/libopenapi/utils"
)

// Validate checks the document against the official JSON Schema for its
// version, then resolves every $ref. It reports problems that make the
// document invalid; style issues are Lint's job.
func (d *Document) Validate() []Diagnostic {
	diags := append(d.validateStructure(), d.resolveReferences()...)
	SortDiagnostics(diags)
	return dedupe(diags)
}

func (d *Document) validateStructure() []Diagnostic {
	valid, failures := schema_validation.ValidateOpenAPIDocument(d.doc)
	if valid {
		return nil
	}
	var diags []Diagnostic
	for _, ve := range failures {
		if ve == nil {
			continue
		}
		if len(ve.SchemaValidationErrors) == 0 {
			diags = append(diags, Diagnostic{
				Severity: SeverityError,
				Source:   SourceValidator,
				Code:     "structure",
				Message:  firstNonEmpty(ve.Reason, ve.Message),
				Range:    pointAt(ve.SpecLine, ve.SpecCol),
				HowToFix: ve.HowToFix,
			})
			continue
		}
		for _, f := range ve.SchemaValidationErrors {
			if f == nil {
				continue
			}
			line, col := f.Line, f.Column
			if line == 0 {
				line, col = ve.SpecLine, ve.SpecCol
			}
			diags = append(diags, Diagnostic{
				Severity: SeverityError,
				Source:   SourceValidator,
				Code:     "structure",
				Message:  f.Reason,
				Path:     jsonPointer(f.InstancePath),
				Range:    pointAt(line, col),
			})
		}
	}
	return diags
}

func (d *Document) resolveReferences() []Diagnostic {
	var err error
	if d.format == FormatSwagger20 {
		_, err = d.doc.BuildV2Model()
	} else {
		_, err = d.doc.BuildV3Model()
	}
	if err == nil {
		return nil
	}
	var diags []Diagnostic
	for _, e := range utils.UnwrapErrors(err) {
		diags = append(diags, referenceDiagnostic(e))
	}
	return diags
}

func referenceDiagnostic(err error) Diagnostic {
	var re *index.ResolvingError
	if !errors.As(err, &re) {
		var ie *index.IndexingError
		if errors.As(err, &ie) {
			return indexingDiagnostic(ie)
		}
		return Diagnostic{
			Severity: SeverityError,
			Source:   SourceResolver,
			Code:     "model",
			Message:  sentence(err.Error()),
		}
	}

	d := Diagnostic{Source: SourceResolver, Path: re.Path}
	if re.Node != nil {
		d.Range = pointAt(re.Node.Line, re.Node.Column)
	}
	circular := re.CircularReference
	switch {
	case circular == nil:
		d.Severity = SeverityError
		d.Code = "unresolved-reference"
		d.Message = referenceMessage(re)
		d.HowToFix = "Check that the referenced component exists and that the $ref is spelled correctly."
	case circular.IsInfiniteLoop:
		d.Severity = SeverityError
		d.Code = "infinite-circular-reference"
		d.Message = "These references form a loop that can never end, because every step is required: " + circular.GenerateJourneyPath() + "."
		d.HowToFix = "Make at least one property in the loop optional, so a value can stop the recursion."
	default:
		d.Severity = SeverityInfo
		d.Code = "circular-reference"
		d.Message = "Circular reference: " + circular.GenerateJourneyPath() + ". This is valid, but some code generators handle it poorly."
	}
	if circular != nil && circular.ParentNode != nil && d.Range.Start.Line == 0 {
		d.Range = pointAt(circular.ParentNode.Line, circular.ParentNode.Column)
	}
	return d
}

// indexingDiagnostic reports a $ref the index couldn't find, such as a
// component that doesn't exist.
func indexingDiagnostic(ie *index.IndexingError) Diagnostic {
	d := Diagnostic{
		Severity: SeverityError,
		Source:   SourceResolver,
		Code:     "unresolved-reference",
		Message:  sentence(ie.Error()),
		Path:     ie.Path,
		HowToFix: "Check that the referenced component exists and that the $ref is spelled correctly.",
	}
	if ie.Node != nil {
		d.Range = pointAt(ie.Node.Line, ie.Node.Column)
	}
	return d
}

func referenceMessage(re *index.ResolvingError) string {
	if re.ErrorRef == nil {
		return sentence(re.Error())
	}
	return sentence(re.ErrorRef.Error())
}

// sentence capitalizes the first letter and ends the message with a period.
func sentence(msg string) string {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return msg
	}
	msg = strings.ToUpper(msg[:1]) + msg[1:]
	if !strings.HasSuffix(msg, ".") {
		msg += "."
	}
	return msg
}

// jsonPointer builds an RFC 6901 pointer from path segments.
func jsonPointer(segments []string) string {
	if len(segments) == 0 {
		return ""
	}
	var b strings.Builder
	r := strings.NewReplacer("~", "~0", "/", "~1")
	for _, s := range segments {
		b.WriteByte('/')
		b.WriteString(r.Replace(s))
	}
	return b.String()
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
