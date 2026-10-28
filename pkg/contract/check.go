// SPDX-License-Identifier: Apache-2.0

// Package contract checks responses to an OpenAPI 3 document's operations:
// that the status is documented, and that the body matches its schema.
// Requests generated as contract tests remember their operation, so their
// responses are checked wherever they run. See
// docs/adr/0035-contract-tests.md.
package contract

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/pb33f/libopenapi"
	verrors "github.com/pb33f/libopenapi-validator/errors"
	"github.com/pb33f/libopenapi-validator/responses"
	"github.com/pb33f/libopenapi/datamodel"
	v3 "github.com/pb33f/libopenapi/datamodel/high/v3"

	"github.com/CemTbi/Signalbox/pkg/specsize"
)

// ErrUnsupported means the document isn't OpenAPI 3.
var ErrUnsupported = errors.New("contract: only OpenAPI 3.0, 3.1 and 3.2 documents have contract tests")

// ErrTooLarge means the document's schemas, with their references
// expanded, are too large to check responses against.
var ErrTooLarge = errors.New("contract: the schemas expand to too much to check responses against; share fewer schemas")

// maxChecked bounds the schemas, counted with references expanded, that a
// document may have, as for mocks.
const maxChecked = 250_000

// From names the contract's checks where tests list who ran them.
const From = "Contract"

// Operation identifies an operation of a document.
type Operation struct {
	// Method is upper case, such as GET.
	Method string `json:"method"`
	// Path is the path template, such as /orders/{orderId}.
	Path string `json:"path"`
}

func (o Operation) String() string { return o.Method + " " + o.Path }

// Result is one check of a response, shown as a test.
type Result struct {
	Name   string
	Passed bool
	Error  string
}

// Checker checks responses against one document. It is safe for
// concurrent use.
type Checker struct {
	model *v3.Document
	body  responses.ResponseBodyValidator
}

func load(document []byte) (*v3.Document, error) {
	cfg := datamodel.NewDocumentConfiguration()
	cfg.AllowFileReferences = false
	cfg.AllowRemoteReferences = false
	doc, err := libopenapi.NewDocumentWithConfiguration(document, cfg)
	if err != nil {
		return nil, fmt.Errorf("contract: read the document: %w", err)
	}
	if doc.GetSpecInfo().SpecFormat == datamodel.OAS2 {
		return nil, ErrUnsupported
	}
	model, err := doc.BuildV3Model()
	if err != nil || model == nil {
		return nil, fmt.Errorf("contract: build the document: %w", err)
	}
	return &model.Model, nil
}

// NewChecker prepares to check responses against a document. File and
// remote references aren't followed. Building is the slow part; keep the
// Checker and reuse it.
func NewChecker(document []byte) (*Checker, error) {
	model, err := load(document)
	if err != nil {
		return nil, err
	}
	if specsize.Operations(model, maxChecked) > maxChecked {
		return nil, ErrTooLarge
	}
	return &Checker{model: model, body: responses.NewResponseBodyValidator(model)}, nil
}

// maxReasons bounds the schema errors one check reports.
const maxReasons = 5

// Check checks a response to an operation: that the operation is still in
// the document, that it documents the status, and that the body matches
// the status's schema. req is the request that was sent; resp's body must
// be readable.
func (c *Checker) Check(op Operation, req *http.Request, resp *http.Response) []Result {
	var item *v3.PathItem
	if c.model.Paths != nil && c.model.Paths.PathItems != nil {
		item = c.model.Paths.PathItems.GetOrZero(op.Path)
	}
	var operation *v3.Operation
	if item != nil {
		operation = item.GetOperations().GetOrZero(strings.ToLower(op.Method))
	}
	if operation == nil {
		return []Result{{Name: op.String() + " is in the spec", Error: "The spec no longer has " + op.String() + ". Generate the contract tests again from the version that has it."}}
	}
	status := resp.StatusCode
	documented := documents(operation, status)
	results := []Result{{Name: fmt.Sprintf("Status %d is documented", status), Passed: documented}}
	if !documented {
		results[0].Error = fmt.Sprintf("%s doesn't document status %d. It documents %s.", op, status, strings.Join(codes(operation), ", "))
		return results
	}
	// The validator finds the operation by the request's method.
	r := req.Clone(req.Context())
	r.Method = strings.ToUpper(op.Method)
	ok, errs := c.body.ValidateResponseBodyWithPathItem(r, resp, item, op.Path)
	body := Result{Name: "The body matches the documented schema", Passed: ok}
	if !ok {
		body.Error = reasons(errs)
	}
	return append(results, body)
}

// documents reports whether an operation documents a status: exactly, as
// a range such as 2XX, or by a default response.
func documents(op *v3.Operation, status int) bool {
	if op.Responses == nil {
		return false
	}
	if op.Responses.Default != nil {
		return true
	}
	code := strconv.Itoa(status)
	for c := range op.Responses.Codes.KeysFromOldest() {
		if c == code || strings.EqualFold(c, code[:1]+"XX") {
			return true
		}
	}
	return false
}

func codes(op *v3.Operation) []string {
	var out []string
	if op.Responses != nil {
		for c := range op.Responses.Codes.KeysFromOldest() {
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		return []string{"no status"}
	}
	return out
}

// reasons says what's wrong with a body, in at most a few lines.
func reasons(errs []*verrors.ValidationError) string {
	var lines []string
	for _, e := range errs {
		if len(e.SchemaValidationErrors) == 0 {
			lines = append(lines, strings.TrimSpace(e.Message+". "+e.Reason))
		}
		for _, f := range e.SchemaValidationErrors {
			at := f.FieldPath
			if at == "" {
				at = "$"
			}
			lines = append(lines, at+": "+f.Reason)
		}
	}
	more := 0
	if len(lines) > maxReasons {
		more = len(lines) - maxReasons
		lines = lines[:maxReasons]
	}
	out := strings.Join(lines, "\n")
	if more > 0 {
		out += fmt.Sprintf("\n…and %d more.", more)
	}
	if out == "" {
		out = "The body doesn't match the documented schema."
	}
	return out
}
