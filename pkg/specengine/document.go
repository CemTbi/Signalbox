// SPDX-License-Identifier: Apache-2.0

package specengine

import (
	"log/slog"
	"strings"

	"github.com/pb33f/libopenapi"
	"github.com/pb33f/libopenapi/datamodel"
)

// Format identifies the specification family and minor version of a document.
type Format string

const (
	FormatSwagger20 Format = "swagger-2.0"
	FormatOpenAPI30 Format = "openapi-3.0"
	FormatOpenAPI31 Format = "openapi-3.1"
	FormatOpenAPI32 Format = "openapi-3.2"
)

// Options controls how a document is read.
type Options struct {
	// SourceName labels the document in diagnostics, usually its file name.
	SourceName string
	// BaseDir is the directory that relative file references resolve from.
	// Leave it empty to refuse file references, which is what a server
	// handling uploaded content must do.
	BaseDir string
	// AllowRemoteReferences lets $ref fetch http and https URLs. It is off
	// by default because a server that fetches arbitrary URLs is an SSRF risk.
	AllowRemoteReferences bool
}

// Document is a parsed OpenAPI or Swagger document.
type Document struct {
	format   Format
	version  string
	fileType string
	raw      []byte
	opts     Options
	doc      libopenapi.Document
}

// Format returns the specification family and minor version.
func (d *Document) Format() Format { return d.format }

// Version returns the exact version the document declares, such as "3.1.0".
func (d *Document) Version() string { return d.version }

// FileType returns "yaml" or "json".
func (d *Document) FileType() string { return d.fileType }

// Parse reads data as an OpenAPI 3.x or Swagger 2.0 document. When data can't
// be read as one, Parse returns a nil Document and at least one error
// diagnostic that explains why.
func Parse(data []byte, opts Options) (*Document, []Diagnostic) {
	doc, err := libopenapi.NewDocumentWithConfiguration(data, documentConfig(opts))
	if err != nil {
		return nil, []Diagnostic{parseErrorDiagnostic(err, data)}
	}

	info := doc.GetSpecInfo()
	format, ok := formatFromSpecInfo(info)
	if !ok {
		return nil, []Diagnostic{{
			Severity: SeverityError,
			Source:   SourceParser,
			Code:     "unsupported-version",
			Message:  "Version " + info.Version + " isn't supported. Signalbox reads Swagger 2.0 and OpenAPI 3.0, 3.1 and 3.2.",
			Range:    versionRange(info.RootNode),
			HowToFix: "Set the version field to 2.0, 3.0.x, 3.1.x or 3.2.x.",
		}}
	}

	return &Document{
		format:   format,
		version:  info.Version,
		fileType: info.SpecFileType,
		raw:      data,
		opts:     opts,
		doc:      doc,
	}, nil
}

func documentConfig(opts Options) *datamodel.DocumentConfiguration {
	cfg := datamodel.NewDocumentConfiguration()
	cfg.Logger = slog.New(slog.DiscardHandler)
	if opts.BaseDir != "" {
		cfg.BasePath = opts.BaseDir
		cfg.AllowFileReferences = true
	}
	cfg.AllowRemoteReferences = opts.AllowRemoteReferences
	return cfg
}

// formatFromSpecInfo maps libopenapi's format to ours. It also rejects minor
// versions this engine doesn't know, which libopenapi would otherwise read
// with the 3.0 rules.
func formatFromSpecInfo(info *datamodel.SpecInfo) (Format, bool) {
	switch info.SpecFormat {
	case datamodel.OAS2:
		return FormatSwagger20, info.Version == "2.0"
	case datamodel.OAS3:
		return FormatOpenAPI30, strings.HasPrefix(info.Version, "3.0")
	case datamodel.OAS31:
		return FormatOpenAPI31, true
	case datamodel.OAS32:
		return FormatOpenAPI32, true
	}
	return "", false
}

func parseErrorDiagnostic(err error, data []byte) Diagnostic {
	msg := err.Error()
	d := Diagnostic{
		Severity: SeverityError,
		Source:   SourceParser,
		Code:     "syntax",
		Range:    positionFromError(err, data),
	}
	switch {
	case strings.Contains(msg, "nothing in the spec"):
		d.Code = "empty-document"
		d.Message = "The document is empty."
	case strings.Contains(msg, "spec type not supported"):
		d.Code = "unsupported-document"
		d.Message = "The document has no top-level openapi or swagger field, so it isn't an OpenAPI or Swagger document."
		d.HowToFix = "Add a version field such as 'openapi: 3.1.1' at the top of the document."
	case strings.Contains(msg, "unable to extract version"),
		strings.Contains(msg, "using a swagger (2.0), or unknown version"):
		d.Code = "unsupported-version"
		d.Message = "The version field can't be read. Use 2.0 for Swagger, or 3.0.x, 3.1.x or 3.2.x for OpenAPI."
	default:
		d.Message = syntaxMessage(msg)
	}
	return d
}
