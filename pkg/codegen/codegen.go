// SPDX-License-Identifier: Apache-2.0

// Package codegen generates code from an OpenAPI document: clients that
// call the API and server stubs that implement it.
//
// Go code comes from oapi-codegen, in process. Before generating, OpenAPI
// 3.1 features that generators can't read are rewritten into equivalents,
// and those they can't express are reported as warnings with their lines.
// See docs/adr/0024-code-generation.md.
package codegen

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/oapi-codegen/oapi-codegen/v2/pkg/codegen"

	"github.com/CemTbi/Signalbox/pkg/specload"
)

// Target is what to generate.
type Target string

const (
	// TargetGoClient is a Go client: a type per schema and a method per
	// operation.
	TargetGoClient Target = "go-client"
	// TargetGoServer is a Go server stub on net/http: an interface with a
	// method per operation, typed requests and responses, and a router.
	TargetGoServer Target = "go-server"
)

// TargetInfo describes a target for people choosing one.
type TargetInfo struct {
	ID          Target `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// Targets lists what can be generated.
var Targets = []TargetInfo{
	{TargetGoClient, "Go client", "A Go package with a type per schema and a method per operation, on net/http."},
	{TargetGoServer, "Go server", "A Go package with an interface to implement, typed requests and responses, and a net/http router."},
}

// ParseTarget reads a target's ID.
func ParseTarget(s string) (Target, error) {
	for _, t := range Targets {
		if string(t.ID) == strings.ToLower(strings.TrimSpace(s)) {
			return t.ID, nil
		}
	}
	ids := make([]string, len(Targets))
	for i, t := range Targets {
		ids[i] = string(t.ID)
	}
	return "", fmt.Errorf("unknown target %q: use %s", s, strings.Join(ids, " or "))
}

// Options choose what to generate.
type Options struct {
	Target Target
	// Package names the generated package. It defaults to one made from
	// the document's title, such as orders for "Orders API".
	Package string
	// AllowRemoteReferences lets $ref fetch http and https URLs.
	AllowRemoteReferences bool
}

// File is a generated file. Path is relative and uses forward slashes.
type File struct {
	Path    string `json:"path"`
	Content []byte `json:"-"`
}

// Result is what a generation produced.
type Result struct {
	Package  string    `json:"package"`
	Files    []File    `json:"files"`
	Warnings []Warning `json:"warnings"`
}

// ErrInvalidPackage means Options.Package isn't a valid package name.
var ErrInvalidPackage = errors.New("codegen: a package name is lowercase letters and digits, starting with a letter")

var packageName = regexp.MustCompile(`^[a-z][a-z0-9]*$`)

// Generate generates code from a document.
func Generate(doc specload.Document, opts Options) (*Result, error) {
	if opts.Package != "" && !packageName.MatchString(opts.Package) {
		return nil, ErrInvalidPackage
	}
	data, warnings, err := prepare(doc.Data)
	if err != nil {
		return nil, fmt.Errorf("codegen: read %s: %w", doc.Name, err)
	}
	prepared := doc
	prepared.Data = data
	spec, err := specload.Load(prepared, specload.Options{AllowRemoteReferences: opts.AllowRemoteReferences})
	if err != nil {
		return nil, fmt.Errorf("codegen: %w", err)
	}
	pkg := opts.Package
	if pkg == "" {
		title := ""
		if spec.Info != nil {
			title = spec.Info.Title
		}
		pkg = packageFrom(title)
	}

	cfg := codegen.Configuration{PackageName: pkg}
	var readme string
	switch opts.Target {
	case TargetGoClient:
		cfg.Generate = codegen.GenerateOptions{Client: true, Models: true}
		readme = goClientReadme
	case TargetGoServer:
		// The strict server decodes requests and encodes responses, so
		// handlers work with types; the embedded spec lets middleware check
		// requests against it.
		cfg.Generate = codegen.GenerateOptions{StdHTTPServer: true, Strict: true, Models: true, EmbeddedSpec: true}
		readme = goServerReadme
	default:
		return nil, fmt.Errorf("codegen: %w", errUnknownTarget(opts.Target))
	}
	code, err := codegen.Generate(spec, cfg)
	if err != nil {
		return nil, fmt.Errorf("codegen: generate %s: %w", opts.Target, err)
	}
	if warnings == nil {
		warnings = []Warning{}
	}
	return &Result{
		Package: pkg,
		Files: []File{
			{Path: pkg + "/" + pkg + ".gen.go", Content: []byte(code)},
			{Path: pkg + "/README.md", Content: []byte(strings.ReplaceAll(readme, "{{package}}", pkg))},
		},
		Warnings: warnings,
	}, nil
}

func errUnknownTarget(t Target) error {
	_, err := ParseTarget(string(t))
	return err
}

// packageFrom makes a package name from a title: "Orders API" becomes
// orders, and "Payments v2" paymentsv2.
func packageFrom(title string) string {
	var b strings.Builder
	words := strings.FieldsFunc(strings.ToLower(title), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	for _, w := range words {
		if w == "api" || w == "service" {
			continue
		}
		for _, r := range w {
			if r < 128 && (unicode.IsLetter(r) || unicode.IsDigit(r)) {
				b.WriteRune(r)
			}
		}
	}
	name := strings.TrimLeftFunc(b.String(), unicode.IsDigit)
	if name == "" {
		return "api"
	}
	return name
}

const goClientReadme = `# {{package}}

A Go client generated by Signalbox with oapi-codegen. Don't edit
{{package}}.gen.go: generate it again when the API changes.

The generated code needs oapi-codegen's runtime:

    go get github.com/oapi-codegen/runtime

Create a client with the API's base URL, then call operations by name:

    client, err := {{package}}.NewClientWithResponses("https://api.example.com")
    if err != nil {
        return err
    }
    resp, err := client.GetOrderWithResponse(ctx, 42)

ClientWithResponses decodes each response into the type the API's
description gives for its status code, such as resp.JSON200. Use
WithRequestEditorFn to add headers, such as credentials, to every request.
`

const goServerReadme = `# {{package}}

A Go server stub generated by Signalbox with oapi-codegen. Don't edit
{{package}}.gen.go: generate it again when the API changes, and keep your
code in other files.

The generated code needs oapi-codegen's runtime:

    go get github.com/oapi-codegen/runtime

Implement StrictServerInterface, with a method per operation that takes a
typed request and returns a typed response, then serve it:

    type server struct{}

    func (server) GetOrder(ctx context.Context, req {{package}}.GetOrderRequestObject) ({{package}}.GetOrderResponseObject, error) {
        return {{package}}.GetOrder200JSONResponse{Id: req.Id}, nil
    }

    handler := {{package}}.HandlerFromMux({{package}}.NewStrictHandler(server{}, nil), http.NewServeMux())
    log.Fatal(http.ListenAndServe(":8080", handler))

GetSwagger returns the API's description, so middleware such as
github.com/oapi-codegen/nethttp-middleware can check requests against it.
`
