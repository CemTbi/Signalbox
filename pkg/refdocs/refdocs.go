// SPDX-License-Identifier: Apache-2.0

// Package refdocs turns an OpenAPI 3 document into the reference that docs
// pages show: operations grouped by tag, with their parameters, bodies and
// responses, schemas with references resolved, and an example for every
// body.
//
// The server builds a Reference for each published spec version, and the
// web app renders it. A static HTML export can render the same model.
package refdocs

import (
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"sort"
	"strconv"
	"strings"

	"github.com/pb33f/go-yaml"
	"github.com/pb33f/libopenapi"
	"github.com/pb33f/libopenapi/datamodel"
	"github.com/pb33f/libopenapi/datamodel/high/base"
	v3 "github.com/pb33f/libopenapi/datamodel/high/v3"
	"github.com/pb33f/libopenapi/orderedmap"

	"github.com/CemTbi/Signalbox/pkg/mockengine"
)

// ErrUnsupported means the document isn't OpenAPI 3.
var ErrUnsupported = errors.New("refdocs: only OpenAPI 3.0, 3.1 and 3.2 documents have reference docs")

// Reference is an API's reference documentation. Descriptions are
// CommonMark, as the document wrote them.
type Reference struct {
	Title           string           `json:"title"`
	Version         string           `json:"version"`
	Summary         string           `json:"summary,omitempty"`
	Description     string           `json:"description,omitempty"`
	Servers         []Server         `json:"servers,omitempty"`
	Groups          []Group          `json:"groups"`
	Schemas         []NamedSchema    `json:"schemas,omitempty"`
	SecuritySchemes []SecurityScheme `json:"securitySchemes,omitempty"`
}

// Server is a base URL of the API, with its variables set to their
// defaults.
type Server struct {
	URL         string `json:"url"`
	Description string `json:"description,omitempty"`
}

// Group is a tag and its operations. The group of operations without a tag
// has no name, and comes last.
type Group struct {
	Name        string `json:"name"`
	Summary     string `json:"summary,omitempty"`
	Description string `json:"description,omitempty"`
	// Parent is the name of the group this one is nested in (OpenAPI 3.2).
	Parent     string      `json:"parent,omitempty"`
	Operations []Operation `json:"operations"`
}

// Operation is one method on one path.
type Operation struct {
	// ID identifies the operation within the reference, for links: its
	// operationId, or its method and path.
	ID          string `json:"id"`
	OperationID string `json:"operationId,omitempty"`
	Method      string `json:"method"`
	Path        string `json:"path"`
	Summary     string `json:"summary,omitempty"`
	Description string `json:"description,omitempty"`
	Deprecated  bool   `json:"deprecated,omitempty"`
	// Servers replaces the document's servers for this operation.
	Servers     []Server     `json:"servers,omitempty"`
	Parameters  []Parameter  `json:"parameters,omitempty"`
	RequestBody *RequestBody `json:"requestBody,omitempty"`
	Responses   []Response   `json:"responses"`
	// Security lists the ways to authorize a call; any one is enough. An
	// empty way means calls may be anonymous. Nil means none is needed.
	Security [][]SecurityUse `json:"security,omitempty"`
}

// Parameter is a path, query, header or cookie parameter, or a response
// header.
type Parameter struct {
	Name        string  `json:"name"`
	In          string  `json:"in"`
	Description string  `json:"description,omitempty"`
	Required    bool    `json:"required,omitempty"`
	Deprecated  bool    `json:"deprecated,omitempty"`
	Schema      *Schema `json:"schema,omitempty"`
	// Example is a JSON value.
	Example json.RawMessage `json:"example,omitempty"`
}

// RequestBody is what an operation accepts.
type RequestBody struct {
	Description string  `json:"description,omitempty"`
	Required    bool    `json:"required,omitempty"`
	Content     []Media `json:"content"`
}

// Response is one status code's response. Code may be a range such as 4XX,
// or "default".
type Response struct {
	Code        string      `json:"code"`
	Description string      `json:"description,omitempty"`
	Headers     []Parameter `json:"headers,omitempty"`
	Content     []Media     `json:"content,omitempty"`
}

// Media is a body in one media type.
type Media struct {
	Type   string  `json:"type"`
	Schema *Schema `json:"schema,omitempty"`
	// Examples are the document's named examples, or one built from its
	// example or schema, as the mock would send it.
	Examples []Example `json:"examples,omitempty"`
}

// Example is a body, as text in its media type.
type Example struct {
	Name        string `json:"name,omitempty"`
	Summary     string `json:"summary,omitempty"`
	Description string `json:"description,omitempty"`
	Value       string `json:"value"`
}

// Schema describes a value. References to component schemas are resolved
// in place, and Name says which component a schema is. A reference that
// would repeat one already being shown, or nest too deeply, isn't
// expanded: Ref is set, and the component is in Reference.Schemas.
type Schema struct {
	Name        string   `json:"name,omitempty"`
	Ref         bool     `json:"ref,omitempty"`
	Types       []string `json:"types,omitempty"`
	Format      string   `json:"format,omitempty"`
	Title       string   `json:"title,omitempty"`
	Description string   `json:"description,omitempty"`
	// Constraints are short phrases such as ">= 1" or "at most 64
	// characters".
	Constraints []string          `json:"constraints,omitempty"`
	Enum        []json.RawMessage `json:"enum,omitempty"`
	Const       json.RawMessage   `json:"const,omitempty"`
	Default     json.RawMessage   `json:"default,omitempty"`
	Example     json.RawMessage   `json:"example,omitempty"`
	Nullable    bool              `json:"nullable,omitempty"`
	ReadOnly    bool              `json:"readOnly,omitempty"`
	WriteOnly   bool              `json:"writeOnly,omitempty"`
	Deprecated  bool              `json:"deprecated,omitempty"`
	Properties  []Property        `json:"properties,omitempty"`
	// AdditionalProperties describes values of keys Properties doesn't
	// list.
	AdditionalProperties *Schema   `json:"additionalProperties,omitempty"`
	Items                *Schema   `json:"items,omitempty"`
	AllOf                []*Schema `json:"allOf,omitempty"`
	OneOf                []*Schema `json:"oneOf,omitempty"`
	AnyOf                []*Schema `json:"anyOf,omitempty"`
	Not                  *Schema   `json:"not,omitempty"`
	// Discriminator names the property that says which of OneOf or AnyOf
	// a value is.
	Discriminator string `json:"discriminator,omitempty"`
}

// Property is a named property of an object schema.
type Property struct {
	Name     string  `json:"name"`
	Required bool    `json:"required,omitempty"`
	Schema   *Schema `json:"schema"`
}

// NamedSchema is a component schema.
type NamedSchema struct {
	Name   string  `json:"name"`
	Schema *Schema `json:"schema"`
}

// SecurityScheme is a way to authorize calls.
type SecurityScheme struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
	// In and ParameterName place an API key.
	In            string `json:"in,omitempty"`
	ParameterName string `json:"parameterName,omitempty"`
	// Scheme is an HTTP authorization scheme, such as bearer.
	Scheme           string `json:"scheme,omitempty"`
	BearerFormat     string `json:"bearerFormat,omitempty"`
	OpenIDConnectURL string `json:"openIdConnectUrl,omitempty"`
	Flows            []Flow `json:"flows,omitempty"`
}

// Flow is an OAuth 2 flow.
type Flow struct {
	Type             string  `json:"type"`
	AuthorizationURL string  `json:"authorizationUrl,omitempty"`
	TokenURL         string  `json:"tokenUrl,omitempty"`
	RefreshURL       string  `json:"refreshUrl,omitempty"`
	Scopes           []Scope `json:"scopes,omitempty"`
}

// Scope is an OAuth 2 scope.
type Scope struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// SecurityUse is a security scheme an operation needs, with the scopes.
type SecurityUse struct {
	Scheme string   `json:"scheme"`
	Scopes []string `json:"scopes,omitempty"`
}

// Limits on how much of a schema graph is written out. Without them, a
// large document with deeply shared schemas would expand into a reference
// many times its size.
const (
	// maxRefDepth is how many references deep schemas are expanded.
	maxRefDepth = 3
	// maxNodes bounds the schemas a reference describes in total; past
	// it, references are left as links.
	maxNodes = 50_000
)

// Build makes the reference of an OpenAPI 3 document. File and remote
// references aren't followed.
func Build(document []byte) (*Reference, error) {
	cfg := datamodel.NewDocumentConfiguration()
	cfg.AllowFileReferences = false
	cfg.AllowRemoteReferences = false
	doc, err := libopenapi.NewDocumentWithConfiguration(document, cfg)
	if err != nil {
		return nil, fmt.Errorf("refdocs: read the document: %w", err)
	}
	if doc.GetSpecInfo().SpecFormat == datamodel.OAS2 {
		return nil, ErrUnsupported
	}
	model, err := doc.BuildV3Model()
	if err != nil || model == nil {
		return nil, fmt.Errorf("refdocs: build the document: %w", err)
	}
	b := &builder{}
	return b.reference(&model.Model), nil
}

type builder struct {
	nodes int
	// expanding lists the components being expanded, innermost last.
	expanding []string
}

func (b *builder) reference(m *v3.Document) *Reference {
	ref := &Reference{Groups: []Group{}}
	if m.Info != nil {
		ref.Title, ref.Version, ref.Summary, ref.Description = m.Info.Title, m.Info.Version, m.Info.Summary, m.Info.Description
	}
	ref.Servers = servers(m.Servers)

	groups := map[string]*Group{}
	var order []string
	group := func(name string) *Group {
		if g, ok := groups[name]; ok {
			return g
		}
		g := &Group{Name: name, Operations: []Operation{}}
		groups[name] = g
		order = append(order, name)
		return g
	}
	// Declared tags first, in the document's order.
	for _, t := range m.Tags {
		if t == nil || t.Name == "" {
			continue
		}
		g := group(t.Name)
		g.Summary, g.Description, g.Parent = t.Summary, t.Description, t.Parent
	}
	if m.Paths != nil && m.Paths.PathItems != nil {
		for path, item := range m.Paths.PathItems.FromOldest() {
			if item == nil {
				continue
			}
			for method, op := range item.GetOperations().FromOldest() {
				if op == nil {
					continue
				}
				o := b.operation(m, path, method, item, op)
				tags := op.Tags
				if len(tags) == 0 {
					tags = []string{""}
				}
				for _, t := range tags {
					g := group(t)
					g.Operations = append(g.Operations, o)
				}
			}
		}
	}
	for _, name := range order {
		g := groups[name]
		// A declared tag with no operations, and no groups under it, says
		// nothing in a reference.
		if len(g.Operations) == 0 && !hasChildren(groups, name) {
			continue
		}
		if name != "" {
			ref.Groups = append(ref.Groups, *g)
		}
	}
	if g, ok := groups[""]; ok && len(g.Operations) > 0 {
		ref.Groups = append(ref.Groups, *g)
	}

	if m.Components != nil {
		if m.Components.Schemas != nil {
			for name, proxy := range m.Components.Schemas.FromOldest() {
				b.expanding = append(b.expanding[:0], name)
				s := b.schemaOf(proxy, 0)
				if s != nil {
					s.Name = name
				}
				ref.Schemas = append(ref.Schemas, NamedSchema{Name: name, Schema: s})
			}
			b.expanding = b.expanding[:0]
		}
		if m.Components.SecuritySchemes != nil {
			for name, s := range m.Components.SecuritySchemes.FromOldest() {
				if s != nil {
					ref.SecuritySchemes = append(ref.SecuritySchemes, securityScheme(name, s))
				}
			}
		}
	}
	return ref
}

func hasChildren(groups map[string]*Group, name string) bool {
	for _, g := range groups {
		if g.Parent == name && len(g.Operations) > 0 {
			return true
		}
	}
	return false
}

func servers(in []*v3.Server) []Server {
	var out []Server
	for _, s := range in {
		if s == nil {
			continue
		}
		u := s.URL
		if s.Variables != nil {
			for name, v := range s.Variables.FromOldest() {
				if v != nil {
					u = strings.ReplaceAll(u, "{"+name+"}", v.Default)
				}
			}
		}
		out = append(out, Server{URL: u, Description: s.Description})
	}
	return out
}

func (b *builder) operation(m *v3.Document, path, method string, item *v3.PathItem, op *v3.Operation) Operation {
	o := Operation{
		OperationID: op.OperationId,
		Method:      strings.ToUpper(method),
		Path:        path,
		Summary:     op.Summary,
		Description: op.Description,
		Deprecated:  op.Deprecated != nil && *op.Deprecated,
		Responses:   []Response{},
	}
	o.ID = op.OperationId
	if o.ID == "" {
		o.ID = strings.ToLower(method) + "-" + path
	}
	switch {
	case len(op.Servers) > 0:
		o.Servers = servers(op.Servers)
	case len(item.Servers) > 0:
		o.Servers = servers(item.Servers)
	}

	// Operation parameters override path item parameters of the same name
	// and location.
	params := map[string]bool{}
	for _, p := range op.Parameters {
		if p != nil {
			params[p.In+" "+p.Name] = true
		}
	}
	for _, p := range item.Parameters {
		if p != nil && !params[p.In+" "+p.Name] {
			o.Parameters = append(o.Parameters, b.parameter(p.Name, p.In, p.Description, p.Required != nil && *p.Required, p.Deprecated, p.Schema, p.Example, p.Content))
		}
	}
	for _, p := range op.Parameters {
		if p != nil {
			o.Parameters = append(o.Parameters, b.parameter(p.Name, p.In, p.Description, p.Required != nil && *p.Required, p.Deprecated, p.Schema, p.Example, p.Content))
		}
	}
	// Path parameters must be given; documents sometimes forget to say so.
	for i := range o.Parameters {
		if o.Parameters[i].In == "path" {
			o.Parameters[i].Required = true
		}
	}

	seedOf := seed(o.Method, path)
	if rb := op.RequestBody; rb != nil {
		o.RequestBody = &RequestBody{Description: rb.Description, Required: rb.Required != nil && *rb.Required, Content: b.content(rb.Content, seedOf)}
	}
	if op.Responses != nil {
		var codes []string
		resps := map[string]*v3.Response{}
		if op.Responses.Codes != nil {
			for code, r := range op.Responses.Codes.FromOldest() {
				if r != nil {
					codes = append(codes, code)
					resps[code] = r
				}
			}
		}
		sort.SliceStable(codes, func(i, j int) bool { return codeOrder(codes[i]) < codeOrder(codes[j]) })
		if op.Responses.Default != nil {
			codes = append(codes, "default")
			resps["default"] = op.Responses.Default
		}
		for _, code := range codes {
			r := resps[code]
			resp := Response{Code: code, Description: r.Description, Content: b.content(r.Content, seedOf)}
			if r.Headers != nil {
				for name, h := range r.Headers.FromOldest() {
					// Content-Type is described by the content, as the
					// specification says.
					if h == nil || strings.EqualFold(name, "Content-Type") {
						continue
					}
					resp.Headers = append(resp.Headers, b.parameter(name, "header", h.Description, h.Required, h.Deprecated, h.Schema, h.Example, h.Content))
				}
			}
			o.Responses = append(o.Responses, resp)
		}
	}

	security := m.Security
	if low := op.GoLow(); low != nil && !low.Security.IsEmpty() {
		security = op.Security
	}
	o.Security = securityUses(security)
	return o
}

// codeOrder sorts status codes numerically, with ranges after the codes
// they cover.
func codeOrder(code string) int {
	if n, err := strconv.Atoi(code); err == nil {
		return n * 10
	}
	if len(code) == 3 && (code[1] == 'X' || code[1] == 'x') {
		return int(code[0]-'0')*1000 + 999
	}
	return 1 << 30
}

func (b *builder) parameter(name, in, description string, required, deprecated bool, schema *base.SchemaProxy, example *yaml.Node, content *orderedmap.Map[string, *v3.MediaType]) Parameter {
	p := Parameter{Name: name, In: in, Description: description, Required: required, Deprecated: deprecated, Example: jsonOf(example)}
	if schema == nil && content != nil {
		// A parameter with content has its schema in its one media type.
		for _, mt := range content.FromOldest() {
			if mt != nil {
				schema = mt.Schema
			}
			break
		}
	}
	if schema != nil {
		p.Schema = b.schemaOf(schema, 0)
	}
	return p
}

func (b *builder) content(content *orderedmap.Map[string, *v3.MediaType], seedOf int64) []Media {
	if content == nil {
		return nil
	}
	var out []Media
	for mediaType, mt := range content.FromOldest() {
		if mt == nil {
			continue
		}
		m := Media{Type: mediaType}
		if mt.Schema != nil {
			m.Schema = b.schemaOf(mt.Schema, 0)
		}
		m.Examples = examples(mediaType, mt, seedOf)
		out = append(out, m)
	}
	return out
}

// examples renders a body's examples as the mock would send them.
func examples(mediaType string, mt *v3.MediaType, seedOf int64) []Example {
	var out []Example
	if mt.Examples != nil && mt.Examples.Len() > 0 {
		for name, ex := range mt.Examples.FromOldest() {
			if ex == nil || ex.Value == nil {
				continue
			}
			body, err := mockengine.Body(mediaType, mt, name, seedOf)
			if err != nil {
				continue
			}
			out = append(out, Example{Name: name, Summary: ex.Summary, Description: ex.Description, Value: string(body)})
		}
		if len(out) > 0 {
			return out
		}
	}
	if mt.Example == nil && mt.Schema == nil {
		return nil
	}
	body, err := mockengine.Body(mediaType, mt, "", seedOf)
	if err != nil || len(body) == 0 {
		return nil
	}
	return []Example{{Value: string(body)}}
}

const componentPrefix = "#/components/schemas/"

func (b *builder) schemaOf(proxy *base.SchemaProxy, refDepth int) *Schema {
	if proxy == nil {
		return nil
	}
	name := ""
	if proxy.IsReference() {
		ref := proxy.GetReference()
		if strings.HasPrefix(ref, componentPrefix) {
			name = strings.ReplaceAll(strings.ReplaceAll(strings.TrimPrefix(ref, componentPrefix), "~1", "/"), "~0", "~")
		}
		if name != "" {
			refDepth++
			if refDepth > maxRefDepth || b.nodes >= maxNodes || b.isExpanding(name) {
				return &Schema{Name: name, Ref: true}
			}
			b.expanding = append(b.expanding, name)
			defer func() { b.expanding = b.expanding[:len(b.expanding)-1] }()
		}
	}
	s := proxy.Schema()
	if s == nil {
		if name != "" {
			return &Schema{Name: name, Ref: true}
		}
		return &Schema{}
	}
	b.nodes++
	out := &Schema{
		Name:        name,
		Types:       s.Type,
		Format:      s.Format,
		Title:       s.Title,
		Description: s.Description,
		Constraints: constraints(s),
		Const:       jsonOf(s.Const),
		Default:     jsonOf(s.Default),
		Example:     jsonOf(s.Example),
		Nullable:    s.Nullable != nil && *s.Nullable,
		ReadOnly:    s.ReadOnly != nil && *s.ReadOnly,
		WriteOnly:   s.WriteOnly != nil && *s.WriteOnly,
		Deprecated:  s.Deprecated != nil && *s.Deprecated,
	}
	if out.Example == nil && len(s.Examples) > 0 {
		out.Example = jsonOf(s.Examples[0])
	}
	for _, e := range s.Enum {
		if v := jsonOf(e); v != nil {
			out.Enum = append(out.Enum, v)
		}
	}
	// OpenAPI 3.1 writes nullable as a "null" type.
	for i, t := range out.Types {
		if t == "null" && len(out.Types) > 1 {
			out.Types = append(append([]string{}, out.Types[:i]...), out.Types[i+1:]...)
			out.Nullable = true
			break
		}
	}
	if s.Properties != nil {
		required := map[string]bool{}
		for _, r := range s.Required {
			required[r] = true
		}
		for pname, p := range s.Properties.FromOldest() {
			out.Properties = append(out.Properties, Property{Name: pname, Required: required[pname], Schema: b.schemaOf(p, refDepth)})
		}
	}
	if ap := s.AdditionalProperties; ap != nil && ap.IsA() && ap.A != nil {
		out.AdditionalProperties = b.schemaOf(ap.A, refDepth)
	}
	if it := s.Items; it != nil && it.IsA() && it.A != nil {
		out.Items = b.schemaOf(it.A, refDepth)
	}
	out.AllOf = b.schemas(s.AllOf, refDepth)
	out.OneOf = b.schemas(s.OneOf, refDepth)
	out.AnyOf = b.schemas(s.AnyOf, refDepth)
	if s.Not != nil {
		out.Not = b.schemaOf(s.Not, refDepth)
	}
	if s.Discriminator != nil {
		out.Discriminator = s.Discriminator.PropertyName
	}
	return out
}

func (b *builder) schemas(in []*base.SchemaProxy, refDepth int) []*Schema {
	var out []*Schema
	for _, p := range in {
		if s := b.schemaOf(p, refDepth); s != nil {
			out = append(out, s)
		}
	}
	return out
}

func (b *builder) isExpanding(name string) bool {
	for _, n := range b.expanding {
		if n == name {
			return true
		}
	}
	return false
}

// constraints describes a schema's limits in short phrases.
func constraints(s *base.Schema) []string {
	var out []string
	num := func(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }
	switch {
	case s.ExclusiveMinimum != nil && s.ExclusiveMinimum.IsB():
		out = append(out, "> "+num(s.ExclusiveMinimum.B))
	case s.Minimum != nil && s.ExclusiveMinimum != nil && s.ExclusiveMinimum.IsA() && s.ExclusiveMinimum.A:
		out = append(out, "> "+num(*s.Minimum))
	case s.Minimum != nil:
		out = append(out, ">= "+num(*s.Minimum))
	}
	switch {
	case s.ExclusiveMaximum != nil && s.ExclusiveMaximum.IsB():
		out = append(out, "< "+num(s.ExclusiveMaximum.B))
	case s.Maximum != nil && s.ExclusiveMaximum != nil && s.ExclusiveMaximum.IsA() && s.ExclusiveMaximum.A:
		out = append(out, "< "+num(*s.Maximum))
	case s.Maximum != nil:
		out = append(out, "<= "+num(*s.Maximum))
	}
	if s.MultipleOf != nil {
		out = append(out, "multiple of "+num(*s.MultipleOf))
	}
	out = appendRange(out, s.MinLength, s.MaxLength, "character")
	if s.Pattern != "" {
		out = append(out, "matches "+s.Pattern)
	}
	out = appendRange(out, s.MinItems, s.MaxItems, "item")
	if s.UniqueItems != nil && *s.UniqueItems {
		out = append(out, "unique items")
	}
	out = appendRange(out, s.MinProperties, s.MaxProperties, "property")
	return out
}

func appendRange(out []string, lo, hi *int64, unit string) []string {
	plural := func(n int64) string {
		if n == 1 {
			return unit
		}
		if strings.HasSuffix(unit, "y") {
			return strings.TrimSuffix(unit, "y") + "ies"
		}
		return unit + "s"
	}
	switch {
	case lo != nil && hi != nil && *lo == *hi:
		out = append(out, fmt.Sprintf("exactly %d %s", *lo, plural(*lo)))
	case lo != nil && hi != nil:
		out = append(out, fmt.Sprintf("%d to %d %s", *lo, *hi, plural(*hi)))
	case lo != nil && *lo > 0:
		out = append(out, fmt.Sprintf("at least %d %s", *lo, plural(*lo)))
	case hi != nil:
		out = append(out, fmt.Sprintf("at most %d %s", *hi, plural(*hi)))
	}
	return out
}

func securityScheme(name string, s *v3.SecurityScheme) SecurityScheme {
	out := SecurityScheme{
		Name: name, Type: s.Type, Description: s.Description, In: s.In, ParameterName: s.Name,
		Scheme: s.Scheme, BearerFormat: s.BearerFormat, OpenIDConnectURL: s.OpenIdConnectUrl,
	}
	if f := s.Flows; f != nil {
		for _, flow := range []struct {
			kind string
			f    *v3.OAuthFlow
		}{{"authorizationCode", f.AuthorizationCode}, {"clientCredentials", f.ClientCredentials}, {"implicit", f.Implicit}, {"password", f.Password}} {
			if flow.f == nil {
				continue
			}
			fl := Flow{Type: flow.kind, AuthorizationURL: flow.f.AuthorizationUrl, TokenURL: flow.f.TokenUrl, RefreshURL: flow.f.RefreshUrl}
			if flow.f.Scopes != nil {
				for scope, desc := range flow.f.Scopes.FromOldest() {
					fl.Scopes = append(fl.Scopes, Scope{Name: scope, Description: desc})
				}
			}
			out.Flows = append(out.Flows, fl)
		}
	}
	return out
}

func securityUses(reqs []*base.SecurityRequirement) [][]SecurityUse {
	var out [][]SecurityUse
	for _, r := range reqs {
		if r == nil {
			continue
		}
		if r.ContainsEmptyRequirement || r.Requirements == nil || r.Requirements.Len() == 0 {
			out = append(out, []SecurityUse{})
			continue
		}
		var way []SecurityUse
		for scheme, scopes := range r.Requirements.FromOldest() {
			way = append(way, SecurityUse{Scheme: scheme, Scopes: scopes})
		}
		out = append(out, way)
	}
	return out
}

// jsonOf converts a YAML value to JSON. Values JSON can't hold, such as
// maps with non-string keys, are left out.
func jsonOf(n *yaml.Node) json.RawMessage {
	if n == nil {
		return nil
	}
	var v any
	if err := n.Decode(&v); err != nil {
		return nil
	}
	out, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return out
}

// seed matches the mock's, so generated examples are stable.
func seed(method, path string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(method + " " + path))
	return int64(h.Sum64() & (1<<63 - 1)) //nolint:gosec // G115: masked to 63 bits.
}
