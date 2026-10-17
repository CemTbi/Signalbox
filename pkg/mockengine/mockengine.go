// SPDX-License-Identifier: Apache-2.0

// Package mockengine answers HTTP requests as an OpenAPI 3 document says
// the API would, so people can build against an API before it exists.
//
// A request is matched to an operation and checked against it; an invalid
// one gets an RFC 9457 problem that names what is wrong. The response is
// the operation's lowest 2xx, or the one the client asks for with
// "Prefer: code=404" or "Prefer: example=name" (RFC 7240). Its body comes
// from the document's examples, or is generated from the schema; generated
// data depends only on the method and path, so the same request always
// gets the same answer.
//
// The server serves published spec versions with it, and the CLI can use
// it for local mocks.
package mockengine

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"mime"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/pb33f/libopenapi"
	validator "github.com/pb33f/libopenapi-validator"
	verrors "github.com/pb33f/libopenapi-validator/errors"
	"github.com/pb33f/libopenapi-validator/paths"
	"github.com/pb33f/libopenapi/datamodel"
	"github.com/pb33f/libopenapi/datamodel/high/base"
	v3 "github.com/pb33f/libopenapi/datamodel/high/v3"
	"github.com/pb33f/libopenapi/orderedmap"
	"github.com/pb33f/libopenapi/renderer"
)

// ErrUnsupported means the document can't be mocked: it isn't OpenAPI 3.
var ErrUnsupported = errors.New("mockengine: only OpenAPI 3.0, 3.1 and 3.2 documents can be mocked")

// ErrTooLarge means the document's schemas, with their references
// expanded, are too large to check requests against or to generate data
// from. For generated data, an example in the document avoids it.
var ErrTooLarge = errors.New("mockengine: the schemas expand to too much to mock; add examples, or share fewer schemas")

// Mock serves one document. It is safe for concurrent use.
type Mock struct {
	model     *v3.Document
	validator validator.Validator
}

// New builds a mock of an OpenAPI 3 document. File and remote references
// aren't followed. Building is the slow part; keep the Mock and reuse it.
func New(document []byte) (*Mock, error) {
	cfg := datamodel.NewDocumentConfiguration()
	cfg.AllowFileReferences = false
	cfg.AllowRemoteReferences = false
	doc, err := libopenapi.NewDocumentWithConfiguration(document, cfg)
	if err != nil {
		return nil, fmt.Errorf("mockengine: read the document: %w", err)
	}
	if doc.GetSpecInfo().SpecFormat == datamodel.OAS2 {
		return nil, ErrUnsupported
	}
	model, err := doc.BuildV3Model()
	if err != nil || model == nil {
		return nil, fmt.Errorf("mockengine: build the document: %w", err)
	}
	if checkedSize(&model.Model) > maxChecked {
		return nil, ErrTooLarge
	}
	v, errs := validator.NewValidator(doc)
	if len(errs) > 0 {
		return nil, fmt.Errorf("mockengine: prepare request checks: %w", errors.Join(errs...))
	}
	return &Mock{model: &model.Model, validator: v}, nil
}

// maxBody bounds request bodies the mock reads.
const maxBody = 1 << 20

// ServeHTTP answers a request. The request's URL path is the API path, such
// as /orders/42; a server mounting the mock under a prefix strips it first.
func (m *Mock) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Body != nil {
		r.Body = http.MaxBytesReader(w, r.Body, maxBody)
		// The validator reads the body; keep a copy for nothing else to
		// consume first.
		body, err := readAll(r)
		if err != nil {
			problem(w, http.StatusRequestEntityTooLarge, "body-too-large", "The request body is larger than 1 MiB.", nil)
			return
		}
		r.Body = body
	}

	pathItem, findErrs, template := paths.FindPath(r, m.model, nil)
	if pathItem == nil {
		detail := "The API has no path that matches " + r.URL.Path + "."
		if len(findErrs) > 0 && findErrs[0].ValidationSubType == "missingOperation" {
			// The path exists without this method.
			if allowed := methodsOf(m.pathItemFor(r)); len(allowed) > 0 {
				w.Header().Set("Allow", strings.Join(allowed, ", "))
			}
			problem(w, http.StatusMethodNotAllowed, "method-not-allowed",
				"The API doesn't accept "+r.Method+" on "+r.URL.Path+".", nil)
			return
		}
		problem(w, http.StatusNotFound, "no-such-path", detail, nil)
		return
	}
	op := operation(pathItem, r.Method)
	if op == nil {
		w.Header().Set("Allow", strings.Join(methodsOf(pathItem), ", "))
		problem(w, http.StatusMethodNotAllowed, "method-not-allowed", "The API doesn't accept "+r.Method+" on "+template+".", nil)
		return
	}

	prefer := parsePrefer(r.Header.Values("Prefer"))
	// A client that asks for a particular response wants it whatever the
	// request looks like, for example to see a 400 example.
	if prefer.code == "" {
		if ok, errs := m.validator.ValidateHttpRequestSyncWithPathItem(r, pathItem, template); !ok {
			problem(w, http.StatusBadRequest, "invalid-request",
				"The request doesn't match the API's description. The errors list what to fix.", fieldErrors(errs))
			return
		}
	}

	code, resp, applied := chooseResponse(op, prefer.code)
	if resp == nil {
		problem(w, http.StatusNotImplemented, "no-response",
			"The API's description has no response for "+r.Method+" "+template+" to mock.", nil)
		return
	}
	var preferences []string
	if applied {
		preferences = append(preferences, "code="+prefer.code)
	}

	mediaType, media := chooseMedia(resp, r.Header.Get("Accept"))
	if media == nil {
		writePreferenceApplied(w, preferences)
		w.WriteHeader(code)
		return
	}
	body, err := render(mediaType, media, prefer.example, seed(r.Method, r.URL.Path))
	if errors.Is(err, ErrTooLarge) {
		problem(w, http.StatusNotImplemented, "schema-too-large",
			"The response's schema expands to too much data to generate. Add an example to the response in the API's description.", nil)
		return
	}
	if err != nil {
		problem(w, http.StatusInternalServerError, "mock-failed", "The mock couldn't build a response body: "+err.Error(), nil)
		return
	}
	if prefer.example != "" && media.Examples != nil {
		if _, ok := media.Examples.Get(prefer.example); ok {
			preferences = append(preferences, "example="+prefer.example)
		}
	}
	writePreferenceApplied(w, preferences)
	w.Header().Set("Content-Type", mediaType)
	w.WriteHeader(code)
	_, _ = w.Write(body)
}

// pathItemFor finds the path item a request's path matches, whatever its
// method, to name the methods it has.
func (m *Mock) pathItemFor(r *http.Request) *v3.PathItem {
	probe := r.Clone(r.Context())
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodHead, http.MethodOptions} {
		probe.Method = method
		if item, _, _ := paths.FindPath(probe, m.model, nil); item != nil {
			return item
		}
	}
	return nil
}

func operation(p *v3.PathItem, method string) *v3.Operation {
	switch method {
	case http.MethodGet:
		return p.Get
	case http.MethodPost:
		return p.Post
	case http.MethodPut:
		return p.Put
	case http.MethodPatch:
		return p.Patch
	case http.MethodDelete:
		return p.Delete
	case http.MethodHead:
		if p.Head != nil {
			return p.Head
		}
		return p.Get
	case http.MethodOptions:
		return p.Options
	case http.MethodTrace:
		return p.Trace
	}
	return nil
}

func methodsOf(p *v3.PathItem) []string {
	if p == nil {
		return nil
	}
	var out []string
	for _, m := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions, http.MethodTrace} {
		if operation(p, m) != nil {
			out = append(out, m)
		}
	}
	return out
}

// chooseResponse returns the response to send: the one preferred, if the
// operation has it, else the lowest 2xx, else default, else the lowest.
func chooseResponse(op *v3.Operation, preferred string) (int, *v3.Response, bool) {
	if op.Responses == nil {
		return 0, nil, false
	}
	codes := map[string]*v3.Response{}
	var numeric []int
	if op.Responses.Codes != nil {
		for code, resp := range op.Responses.Codes.FromOldest() {
			codes[code] = resp
			if n, err := strconv.Atoi(code); err == nil {
				numeric = append(numeric, n)
			}
		}
	}
	sort.Ints(numeric)
	if preferred != "" {
		if resp, ok := codes[preferred]; ok {
			n, _ := strconv.Atoi(preferred)
			return n, resp, true
		}
		// A range such as 4XX answers for any code in it.
		if n, err := strconv.Atoi(preferred); err == nil && n >= 100 && n <= 599 {
			if resp, ok := codes[fmt.Sprintf("%dXX", n/100)]; ok {
				return n, resp, true
			}
		}
	}
	for _, n := range numeric {
		if n >= 200 && n < 300 {
			return n, codes[strconv.Itoa(n)], false
		}
	}
	if op.Responses.Default != nil {
		return http.StatusOK, op.Responses.Default, false
	}
	if len(numeric) > 0 {
		return numeric[0], codes[strconv.Itoa(numeric[0])], false
	}
	return 0, nil, false
}

// chooseMedia picks the response's content type the client accepts best,
// preferring JSON when it accepts anything.
func chooseMedia(resp *v3.Response, accept string) (string, *v3.MediaType) {
	if resp.Content == nil || resp.Content.Len() == 0 {
		return "", nil
	}
	var types []string
	for t := range resp.Content.KeysFromOldest() {
		types = append(types, t)
	}
	pick := func(t string) (string, *v3.MediaType) {
		mt, _ := resp.Content.Get(t)
		return t, mt
	}
	for _, want := range acceptList(accept) {
		for _, t := range types {
			if matchesMediaRange(want, t) {
				if want == "*/*" {
					break // fall through to the JSON preference below
				}
				return pick(t)
			}
		}
	}
	for _, t := range types {
		if isJSON(t) {
			return pick(t)
		}
	}
	return pick(types[0])
}

func acceptList(accept string) []string {
	var out []string
	for _, part := range strings.Split(accept, ",") {
		mt, params, err := mime.ParseMediaType(strings.TrimSpace(part))
		if err != nil || params["q"] == "0" {
			continue
		}
		out = append(out, mt)
	}
	return out
}

func matchesMediaRange(want, have string) bool {
	have = strings.ToLower(strings.TrimSpace(strings.Split(have, ";")[0]))
	switch {
	case want == "*/*":
		return true
	case strings.HasSuffix(want, "/*"):
		return strings.HasPrefix(have, strings.TrimSuffix(want, "*"))
	}
	return want == have
}

func isJSON(t string) bool {
	t = strings.ToLower(strings.Split(t, ";")[0])
	return t == "application/json" || strings.HasSuffix(t, "+json")
}

// Body builds a body for a media type as a mock would send it: the named
// example, or the first one, or data generated from the schema. Generated
// data depends only on seed.
func Body(mediaType string, media *v3.MediaType, example string, seed int64) ([]byte, error) {
	return render(mediaType, media, example, seed)
}

// render builds the body for a media type from its examples or schema.
func render(mediaType string, media *v3.MediaType, example string, seed int64) ([]byte, error) {
	kind := renderer.JSON
	t := strings.ToLower(strings.Split(mediaType, ";")[0])
	switch {
	case strings.Contains(t, "yaml"):
		kind = renderer.YAML
	case strings.HasSuffix(t, "/xml") || strings.HasSuffix(t, "+xml"):
		kind = renderer.XML
	}
	if media.Example == nil && (media.Examples == nil || media.Examples.Len() == 0) && media.Schema != nil &&
		expandedSize(media.Schema, maxGenerated, map[string]int{}, map[string]bool{}) > maxGenerated {
		return nil, ErrTooLarge
	}
	gen := renderer.NewMockGenerator(kind)
	gen.SetSeed(seed)
	gen.SetPretty()
	body, err := gen.GenerateMock(media, example)
	if err != nil {
		return nil, err
	}
	// A plain-text example is sent as text, not as a JSON string.
	if kind == renderer.JSON && !isJSON(t) {
		var s string
		if json.Unmarshal(body, &s) == nil {
			return []byte(s), nil
		}
	}
	return body, nil
}

// maxGenerated bounds the schemas a generated body may expand to. The
// generator's own budgets apply only after it has copied shared schemas,
// which grows exponentially when schemas refer to others more than once.
const maxGenerated = 10_000

// maxChecked bounds the schemas, counted with references expanded, that
// request checks prepare. Preparing takes about 15 µs a schema.
const maxChecked = 250_000

// checkedSize counts the schemas the request validator prepares: those of
// every parameter, request body and response, each with its references
// expanded. It stops counting soon after maxChecked.
func checkedSize(m *v3.Document) int {
	if m.Paths == nil || m.Paths.PathItems == nil {
		return 0
	}
	sizes, active := map[string]int{}, map[string]bool{}
	n := 0
	add := func(p *base.SchemaProxy) {
		if n <= maxChecked {
			n += expandedSize(p, maxChecked-n, sizes, active)
		}
	}
	addContent := func(content *orderedmap.Map[string, *v3.MediaType]) {
		if content == nil {
			return
		}
		for _, mt := range content.FromOldest() {
			if mt != nil {
				add(mt.Schema)
			}
		}
	}
	addParams := func(params []*v3.Parameter) {
		for _, p := range params {
			if p != nil {
				add(p.Schema)
				addContent(p.Content)
			}
		}
	}
	for _, item := range m.Paths.PathItems.FromOldest() {
		if item == nil {
			continue
		}
		addParams(item.Parameters)
		for _, op := range item.GetOperations().FromOldest() {
			if op == nil {
				continue
			}
			addParams(op.Parameters)
			if op.RequestBody != nil {
				addContent(op.RequestBody.Content)
			}
			if op.Responses != nil {
				if op.Responses.Codes != nil {
					for _, r := range op.Responses.Codes.FromOldest() {
						if r != nil {
							addContent(r.Content)
						}
					}
				}
				if op.Responses.Default != nil {
					addContent(op.Responses.Default.Content)
				}
			}
		}
	}
	return n
}

// expandedSize counts the schemas a body generated from proxy would
// contain, stopping once the count passes limit. A reference to a schema
// already being expanded counts once, as the generator stops there too.
// sizes remembers each referenced schema's size; active holds the
// references being expanded.
func expandedSize(proxy *base.SchemaProxy, limit int, sizes map[string]int, active map[string]bool) int {
	if proxy == nil {
		return 0
	}
	ref := ""
	if proxy.IsReference() {
		ref = proxy.GetReference()
		if n, ok := sizes[ref]; ok {
			return n
		}
		if active[ref] {
			return 1
		}
		active[ref] = true
		defer delete(active, ref)
	}
	s := proxy.Schema()
	if s == nil {
		return 1
	}
	n := 1
	add := func(p *base.SchemaProxy) {
		if n <= limit {
			n += expandedSize(p, limit-n, sizes, active)
		}
	}
	if s.Properties != nil {
		for _, p := range s.Properties.FromOldest() {
			add(p)
		}
	}
	if s.Items != nil && s.Items.IsA() {
		add(s.Items.A)
	}
	if s.AdditionalProperties != nil && s.AdditionalProperties.IsA() {
		add(s.AdditionalProperties.A)
	}
	for _, list := range [][]*base.SchemaProxy{s.AllOf, s.OneOf, s.AnyOf, s.PrefixItems} {
		for _, p := range list {
			add(p)
		}
	}
	if s.DependentSchemas != nil {
		for _, p := range s.DependentSchemas.FromOldest() {
			add(p)
		}
	}
	// A count cut short at the limit is only good for this limit.
	if ref != "" && n <= limit {
		sizes[ref] = n
	}
	return n
}

// seed makes generated data depend only on the request's method and path.
func seed(method, path string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(method + " " + path))
	return int64(h.Sum64() & (1<<63 - 1)) //nolint:gosec // G115: masked to 63 bits.
}

type preferences struct {
	code    string
	example string
}

// parsePrefer reads the code and example preferences of RFC 7240 Prefer
// headers, such as "Prefer: code=404, example=not-found".
func parsePrefer(values []string) preferences {
	var p preferences
	for _, v := range values {
		for _, part := range strings.Split(v, ",") {
			for _, kv := range strings.Split(part, ";") {
				key, value, ok := strings.Cut(strings.TrimSpace(kv), "=")
				if !ok {
					continue
				}
				value = strings.Trim(strings.TrimSpace(value), `"`)
				switch strings.ToLower(strings.TrimSpace(key)) {
				case "code":
					if n, err := strconv.Atoi(value); err == nil && n >= 100 && n <= 599 {
						p.code = value
					} else if value == "default" {
						p.code = value
					}
				case "example":
					p.example = value
				}
			}
		}
	}
	return p
}

func writePreferenceApplied(w http.ResponseWriter, applied []string) {
	if len(applied) > 0 {
		w.Header().Set("Preference-Applied", strings.Join(applied, ", "))
		w.Header().Add("Vary", "Prefer")
	}
}

// FieldError is one problem with a request.
type FieldError struct {
	// Location names where: "query parameter limit", or a JSON pointer
	// into the body such as "/items/0/qty".
	Location string `json:"location,omitempty"`
	Detail   string `json:"detail"`
}

func fieldErrors(errs []*verrors.ValidationError) []FieldError {
	var out []FieldError
	for _, e := range errs {
		if len(e.SchemaValidationErrors) > 0 {
			for _, f := range e.SchemaValidationErrors {
				loc := "/" + strings.Join(f.InstancePath, "/")
				if e.ParameterName != "" {
					loc = e.ValidationSubType + " parameter " + e.ParameterName
				}
				out = append(out, FieldError{Location: loc, Detail: f.Reason})
			}
			continue
		}
		loc := ""
		if e.ParameterName != "" {
			loc = e.ValidationSubType + " parameter " + e.ParameterName
		}
		detail := e.Reason
		if detail == "" {
			detail = e.Message
		}
		out = append(out, FieldError{Location: loc, Detail: detail})
	}
	return out
}

// problem writes an RFC 9457 problem document.
func problem(w http.ResponseWriter, status int, code, detail string, errs []FieldError) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Type   string       `json:"type"`
		Title  string       `json:"title"`
		Status int          `json:"status"`
		Detail string       `json:"detail"`
		Code   string       `json:"code"`
		Errors []FieldError `json:"errors,omitempty"`
	}{"about:blank", http.StatusText(status), status, detail, code, errs})
}

// bodyReader lets the validator read a body that was already read.
type bodyReader struct{ *bytes.Reader }

func (bodyReader) Close() error { return nil }

func readAll(r *http.Request) (bodyReader, error) {
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r.Body); err != nil {
		return bodyReader{}, err
	}
	return bodyReader{bytes.NewReader(buf.Bytes())}, nil
}
