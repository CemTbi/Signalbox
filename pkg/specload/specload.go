// SPDX-License-Identifier: Apache-2.0

// Package specload reads OpenAPI documents with kin-openapi, for the
// libraries built on it, such as oasdiff and oapi-codegen. Swagger 2.0 is
// converted to OpenAPI 3. File references are followed only from a given
// directory, and remote ones only when allowed, as the spec engine does.
//
// Everything else in Signalbox reads documents with libopenapi; see
// docs/adr/0023-breaking-changes.md for why some libraries need this one.
package specload

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"github.com/getkin/kin-openapi/openapi2"
	"github.com/getkin/kin-openapi/openapi2conv"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/pb33f/go-yaml"
	"github.com/pb33f/libopenapi"
	"github.com/pb33f/libopenapi/datamodel"

	"github.com/CemTbi/Signalbox/pkg/specsize"
)

// ErrTooLarge means a document's schemas, with references expanded, are
// more than Options.MaxSchemas.
var ErrTooLarge = errors.New("the schemas expand to too much to process; share fewer schemas or split the API")

// Document is a document to read.
type Document struct {
	Data []byte
	// Name is what errors and reports call the document, such as its path.
	Name string
	// Dir is where relative file references resolve from. Without it,
	// file references aren't followed.
	Dir string
}

// Options control what loading may read.
type Options struct {
	// AllowRemoteReferences lets $ref fetch http and https URLs.
	AllowRemoteReferences bool
	// MaxSchemas, when positive, refuses documents whose parameters,
	// bodies and responses expand to more schemas, for callers that walk
	// them expanded.
	MaxSchemas int
}

// Load reads a document as OpenAPI 3, converting Swagger 2.0. With
// Options.MaxSchemas, it first checks that the document isn't too large.
func Load(doc Document, opts Options) (*openapi3.T, error) {
	swagger2, err := isSwagger2(doc.Data)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", doc.Name, err)
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
			return nil, fmt.Errorf("read %s: %w", doc.Name, err)
		}
		if t, err = openapi2conv.ToV3WithLoader(&v2, loader, location); err != nil {
			return nil, fmt.Errorf("convert %s to OpenAPI 3: %w", doc.Name, err)
		}
	} else if t, err = loader.LoadFromDataWithPath(doc.Data, location); err != nil {
		return nil, fmt.Errorf("read %s: %w", doc.Name, err)
	}

	if opts.MaxSchemas <= 0 {
		return t, nil
	}
	// Measure the document in the form callers will walk it.
	data := doc.Data
	if swagger2 {
		if data, err = json.Marshal(t); err != nil {
			return nil, fmt.Errorf("read %s: %w", doc.Name, err)
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
		return fmt.Errorf("read %s: %w", doc.Name, err)
	}
	m, err := d.BuildV3Model()
	if m == nil {
		return fmt.Errorf("read %s: %w", doc.Name, err)
	}
	if specsize.Operations(&m.Model, opts.MaxSchemas) > opts.MaxSchemas {
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
