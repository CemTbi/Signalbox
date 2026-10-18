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
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/getkin/kin-openapi/openapi2"
	"github.com/getkin/kin-openapi/openapi2conv"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/oasdiff/oasdiff/checker"
	"github.com/oasdiff/oasdiff/diff"
	"github.com/oasdiff/oasdiff/load"
	"github.com/pb33f/go-yaml"
	"github.com/pb33f/libopenapi"
	"github.com/pb33f/libopenapi/datamodel"

	"github.com/CemTbi/Signalbox/pkg/specsize"
)

// ErrTooLarge means a document's schemas, with references expanded, are
// too large to compare. Comparing walks every expanded schema, so a schema
// shared many times over could otherwise take minutes.
var ErrTooLarge = errors.New("specdiff: the schemas expand to too much to compare; share fewer schemas or split the API")

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
type Document struct {
	Data []byte
	// Name is what reports call the document, such as its path.
	Name string
	// Dir is where relative file references resolve from. Without it,
	// file references aren't followed.
	Dir string
}

// Options control what a comparison may read.
type Options struct {
	// AllowRemoteReferences lets $ref fetch http and https URLs.
	AllowRemoteReferences bool
}

// Compare reports what changed from base to head.
func Compare(base, head Document, opts Options) (*Report, error) {
	b, err := load3(base, opts)
	if err != nil {
		return nil, err
	}
	h, err := load3(head, opts)
	if err != nil {
		return nil, err
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

// load3 reads a document as OpenAPI 3, converting Swagger 2.0, after
// checking it isn't too large to compare.
func load3(doc Document, opts Options) (*openapi3.T, error) {
	swagger2, err := isSwagger2(doc.Data)
	if err != nil {
		return nil, fmt.Errorf("specdiff: read %s: %w", doc.Name, err)
	}
	loader := openapi3.NewLoader()
	loader.IncludeOrigin = true
	loader.IsExternalRefsAllowed = doc.Dir != "" || opts.AllowRemoteReferences
	loader.ReadFromURIFunc = readFrom(doc.Dir, opts.AllowRemoteReferences)
	location := &url.URL{Path: filepath.ToSlash(filepath.Join(doc.Dir, filepath.Base(doc.Name)))}

	var t *openapi3.T
	if swagger2 {
		var v2 openapi2.T
		if err := unmarshal(doc.Data, &v2); err != nil {
			return nil, fmt.Errorf("specdiff: read %s: %w", doc.Name, err)
		}
		if t, err = openapi2conv.ToV3WithLoader(&v2, loader, location); err != nil {
			return nil, fmt.Errorf("specdiff: convert %s to OpenAPI 3: %w", doc.Name, err)
		}
	} else if t, err = loader.LoadFromDataWithPath(doc.Data, location); err != nil {
		return nil, fmt.Errorf("specdiff: read %s: %w", doc.Name, err)
	}

	// Measure what the comparison will walk, in the form it will walk it.
	data := doc.Data
	if swagger2 {
		if data, err = json.Marshal(t); err != nil {
			return nil, fmt.Errorf("specdiff: read %s: %w", doc.Name, err)
		}
	}
	if err := checkSize(data, doc, opts); err != nil {
		return nil, err
	}
	return t, nil
}

func checkSize(data []byte, doc Document, opts Options) error {
	cfg := datamodel.NewDocumentConfiguration()
	cfg.AllowFileReferences = doc.Dir != ""
	cfg.AllowRemoteReferences = opts.AllowRemoteReferences
	cfg.BasePath = doc.Dir
	d, err := libopenapi.NewDocumentWithConfiguration(data, cfg)
	if err != nil {
		return fmt.Errorf("specdiff: read %s: %w", doc.Name, err)
	}
	m, err := d.BuildV3Model()
	if m == nil {
		return fmt.Errorf("specdiff: read %s: %w", doc.Name, err)
	}
	if specsize.Operations(&m.Model, maxCompared) > maxCompared {
		return fmt.Errorf("%s: %w", doc.Name, ErrTooLarge)
	}
	return nil
}

func isSwagger2(data []byte) (bool, error) {
	var head struct {
		Swagger any `yaml:"swagger"`
	}
	if err := yaml.Unmarshal(data, &head); err != nil {
		return false, err
	}
	return head.Swagger != nil, nil
}

// unmarshal reads YAML or JSON into v, through JSON so that kin-openapi's
// JSON tags apply.
func unmarshal(data []byte, v any) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) > 0 && trimmed[0] == '{' {
		return json.Unmarshal(trimmed, v)
	}
	var doc any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return err
	}
	j, err := json.Marshal(normalize(doc))
	if err != nil {
		return err
	}
	return json.Unmarshal(j, v)
}

// normalize makes YAML's maps JSON-encodable.
func normalize(v any) any {
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			x[k] = normalize(e)
		}
		return x
	case map[any]any:
		m := make(map[string]any, len(x))
		for k, e := range x {
			m[fmt.Sprint(k)] = normalize(e)
		}
		return m
	case []any:
		for i, e := range x {
			x[i] = normalize(e)
		}
		return x
	}
	return v
}

// readFrom reads referenced files under dir, and http and https URLs only
// when allowed.
func readFrom(dir string, remote bool) openapi3.ReadFromURIFunc {
	return func(loader *openapi3.Loader, location *url.URL) ([]byte, error) {
		switch location.Scheme {
		case "http", "https":
			if !remote {
				return nil, fmt.Errorf("%s: remote references aren't allowed", location)
			}
			return openapi3.ReadFromHTTP(nil)(loader, location)
		case "", "file":
			if dir == "" {
				return nil, fmt.Errorf("%s: file references aren't allowed", location.Path)
			}
			// The document's author chose the file, as with any multi-file
			// spec; servers compare documents without a directory.
			return os.ReadFile(filepath.FromSlash(location.Path)) //nolint:gosec // G703: see above.
		}
		return nil, fmt.Errorf("%s: unsupported reference", location)
	}
}
