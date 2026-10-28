// SPDX-License-Identifier: Apache-2.0

package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/CemTbi/Signalbox/internal/modules/apis"
	"github.com/CemTbi/Signalbox/internal/modules/runner"
	"github.com/CemTbi/Signalbox/internal/platform/id"
	"github.com/CemTbi/Signalbox/pkg/contract"
	"github.com/CemTbi/Signalbox/pkg/refdocs"
)

// ContractRef is the operation of a published spec version a saved
// request calls. Its responses are checked against the operation.
type ContractRef struct {
	VersionID uuid.UUID `json:"versionId"`
	Method    string    `json:"method"`
	Path      string    `json:"path"`
}

// ContractChecker checks responses against spec versions; the apis module
// is one.
type ContractChecker interface {
	CheckContract(ctx context.Context, projectID, versionID uuid.UUID, op contract.Operation, req *http.Request, resp *http.Response) []contract.Result
}

// ContractVersions gives published versions' documents; the apis module
// is one.
type ContractVersions interface {
	ContractVersion(ctx context.Context, userID, versionID uuid.UUID) (apis.ContractVersion, error)
}

// UseContracts lets sends and runs check responses against spec versions,
// and the module generate contract tests.
func (m *Module) UseContracts(checker ContractChecker, versions ContractVersions) {
	m.contracts, m.versions = checker, versions
}

// checkContract checks a response against the step's operation, as tests.
func (e *Engine) checkContract(ctx context.Context, projectID uuid.UUID, ref ContractRef, req runner.Request, resp *runner.Response) []ScriptTest {
	hreq, err := http.NewRequestWithContext(ctx, req.Method, req.URL, bytes.NewReader(req.Body))
	if err != nil {
		// The runner sent it, so this can't happen; say so if it does.
		return []ScriptTest{{From: contract.From, Name: "The response follows the spec", Error: "The request couldn't be read back: " + err.Error()}}
	}
	for _, h := range req.Headers {
		hreq.Header.Add(h.Key, h.Value)
	}
	hresp := &http.Response{StatusCode: resp.Status, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(resp.Body)), Request: hreq}
	for _, h := range resp.Headers {
		hresp.Header.Add(h.Key, h.Value)
	}
	var out []ScriptTest
	for _, r := range e.contracts.CheckContract(ctx, projectID, ref.VersionID, contract.Operation{Method: ref.Method, Path: ref.Path}, hreq, hresp) {
		out = append(out, ScriptTest{From: contract.From, Name: r.Name, Passed: r.Passed, Error: clip(r.Error)})
	}
	return out
}

// ErrNoContracts means this server can't generate contract tests.
var ErrNoContracts = errors.New("client: contract tests aren't available")

// ContractReport says what generating contract tests did.
type ContractReport struct {
	// Created says the collection is new.
	Created bool
	// Requests is how many requests were generated, one per operation.
	Requests int
	// Replaced is how many generated items the new ones replaced.
	Replaced int
}

// ContractOptions are how contract tests are generated.
type ContractOptions struct {
	// MockURL is the version's hosted mock, the base URL when the spec
	// names no server.
	MockURL string
}

// GenerateContractTests makes a collection of contract tests from a
// published spec version: a request per operation, filled from its
// examples, whose responses are checked against it. The spec's contract
// tests are one collection per project: generating again, such as for a
// newer version, replaces the generated requests and folders, and keeps
// the items people added and the variables' values. Owners, admins and
// editors may.
func (m *Module) GenerateContractTests(ctx context.Context, userID, versionID uuid.UUID, opts ContractOptions) (Collection, ContractReport, error) {
	if m.versions == nil {
		return Collection{}, ContractReport{}, ErrNoContracts
	}
	v, err := m.versions.ContractVersion(ctx, userID, versionID)
	if errors.Is(err, apis.ErrNotFound) {
		return Collection{}, ContractReport{}, ErrNotFound
	}
	if err != nil {
		return Collection{}, ContractReport{}, err
	}
	if err := m.authorizeRole(ctx, userID, v.ProjectID, edit); err != nil {
		return Collection{}, ContractReport{}, err
	}
	ref, err := refdocs.Build(v.Content)
	if err != nil {
		return Collection{}, ContractReport{}, &ContractError{Err: err}
	}
	gen := generate(ref, v, opts)
	report := ContractReport{Requests: gen.requests}
	var c Collection
	err = pgx.BeginFunc(ctx, m.db, func(tx pgx.Tx) error {
		existing, err := readCollection(tx.QueryRow(ctx, `SELECT `+collectionColumns+` FROM collections WHERE project_id = $1 AND contract_spec_id = $2 FOR UPDATE`, v.ProjectID, v.SpecID))
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			report.Created = true
			variables, err := json.Marshal(gen.variables)
			if err != nil {
				return err
			}
			if c, err = readCollection(tx.QueryRow(ctx, `
				INSERT INTO collections (id, project_id, name, description, variables, created_by, contract_spec_id, contract_version_id)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING `+collectionColumns,
				id.New(), v.ProjectID, gen.name, gen.description, variables, userID, v.SpecID, v.VersionID)); err != nil {
				return err
			}
		case err != nil:
			return err
		default:
			// The variables people set keep their values; new ones are added.
			vars := existing.Variables
			have := map[string]bool{}
			for _, cv := range vars {
				have[cv.Key] = true
			}
			for _, gv := range gen.variables {
				if !have[gv.Key] && len(vars) < maxVariables {
					vars = append(vars, gv)
				}
			}
			variables, err := json.Marshal(vars)
			if err != nil {
				return err
			}
			if c, err = readCollection(tx.QueryRow(ctx, `
				UPDATE collections SET contract_version_id = $2, description = $3, variables = $4, version = version + 1, updated_at = now()
				WHERE id = $1 RETURNING `+collectionColumns, existing.ID, v.VersionID, gen.description, variables)); err != nil {
				return err
			}
			tag, err := tx.Exec(ctx, `DELETE FROM collection_items WHERE collection_id = $1 AND generated`, c.ID)
			if err != nil {
				return err
			}
			report.Replaced = int(tag.RowsAffected())
		}
		// Generated items go after what people added at the top.
		var last string
		if err := tx.QueryRow(ctx, `SELECT coalesce(max(rank), '') FROM collection_items WHERE collection_id = $1 AND parent_id IS NULL`, c.ID).Scan(&last); err != nil {
			return err
		}
		var rows [][]any
		if err := generatedRows(&rows, c.ID, uuid.Nil, last, gen.items); err != nil {
			return err
		}
		_, err = tx.CopyFrom(ctx, pgx.Identifier{"collection_items"},
			[]string{"id", "collection_id", "parent_id", "rank", "kind", "name", "description", "request", "generated"}, pgx.CopyFromRows(rows))
		return err
	})
	if err != nil {
		return Collection{}, ContractReport{}, fmt.Errorf("generate contract tests: %w", err)
	}
	m.logger.InfoContext(ctx, "generated contract tests", id.LogAttr("collection_id", id.Collection, c.ID), id.LogAttr("user_id", id.User, userID))
	return c, report, nil
}

// ContractError means a version's document can't make contract tests.
type ContractError struct{ Err error }

func (e *ContractError) Error() string { return "client: " + e.Err.Error() }
func (e *ContractError) Unwrap() error { return e.Err }

// genItem is a generated folder, with items, or request.
type genItem struct {
	name        string
	description string
	request     *SavedRequest
	items       []genItem
}

type generated struct {
	name, description string
	variables         []CollectionVariable
	items             []genItem
	requests          int
}

// generate makes the items of a version's contract tests: a folder per
// tag, and a request per operation.
func generate(ref *refdocs.Reference, v apis.ContractVersion, opts ContractOptions) generated {
	g := generated{
		name: truncate(v.SpecName+" contract tests", 100),
		description: truncate(fmt.Sprintf("Generated from %s %s. Each request checks that its response has a documented status and matches the schema. "+
			"Generating again replaces the generated requests and folders, and keeps what you add and the variables' values.", v.SpecName, v.Name), 2000),
	}
	base := opts.MockURL
	if len(ref.Servers) > 0 {
		base = ref.Servers[0].URL
	}
	g.variables = append(g.variables, CollectionVariable{Key: "baseUrl", Value: strings.TrimRight(base, "/")})
	seen := map[string]bool{"baseUrl": true}
	schemes := map[string]refdocs.SecurityScheme{}
	for _, s := range ref.SecuritySchemes {
		schemes[s.Name] = s
	}
	for _, grp := range ref.Groups {
		var reqs []genItem
		for _, op := range grp.Operations {
			if len(g.variables) >= maxVariables || g.requests >= maxGenerated {
				break
			}
			reqs = append(reqs, g.request(op, v.VersionID, schemes, seen))
		}
		if len(reqs) == 0 {
			continue
		}
		if grp.Name == "" {
			g.items = append(g.items, reqs...)
			continue
		}
		g.items = append(g.items, genItem{name: truncate(grp.Name, 200), description: truncate(grp.Summary, 10_000), items: reqs})
	}
	return g
}

// maxGenerated bounds the requests one version generates.
const maxGenerated = 2000

func (g *generated) request(op refdocs.Operation, versionID uuid.UUID, schemes map[string]refdocs.SecurityScheme, seen map[string]bool) genItem {
	g.requests++
	name := op.Summary
	if name == "" {
		name = op.Method + " " + op.Path
	}
	path := op.Path
	var query []string
	r := &SavedRequest{Method: op.Method, Headers: []SavedHeader{}, Contract: &ContractRef{VersionID: versionID, Method: op.Method, Path: op.Path}}
	for _, p := range op.Parameters {
		value := exampleValue(p.Example, p.Schema)
		switch p.In {
		case "path":
			// Path parameters are variables, so a run can use real IDs.
			path = strings.ReplaceAll(path, "{"+p.Name+"}", "{{"+p.Name+"}}")
			if !seen[p.Name] && validVariable(p.Name) {
				seen[p.Name] = true
				g.variables = append(g.variables, CollectionVariable{Key: p.Name, Value: value})
			}
		case "query":
			if p.Required {
				query = append(query, url.QueryEscape(p.Name)+"="+url.QueryEscape(value))
			}
		case "header":
			switch strings.ToLower(p.Name) {
			case "accept", "content-type", "authorization":
			default:
				if p.Required {
					r.Headers = append(r.Headers, SavedHeader{Key: p.Name, Value: value, Enabled: true})
				}
			}
		}
	}
	r.URL = "{{baseUrl}}" + path
	if len(query) > 0 {
		r.URL += "?" + strings.Join(query, "&")
	}
	if b := op.RequestBody; b != nil && len(b.Content) > 0 {
		media := b.Content[0]
		for _, m := range b.Content {
			if strings.Contains(m.Type, "json") {
				media = m
				break
			}
		}
		r.Headers = append(r.Headers, SavedHeader{Key: "Content-Type", Value: media.Type, Enabled: true})
		if len(media.Examples) > 0 {
			r.Body = media.Examples[0].Value
		}
	}
	r.Auth = authFor(op.Security, schemes)
	return genItem{name: truncate(name, 200), description: truncate(op.Description, 10_000), request: r}
}

// authFor is the auth for an operation's first way of authorizing that
// needs a scheme, with its credentials as variables to set in an
// environment: {{token}}, {{username}} and {{password}}, or {{apiKey}}.
func authFor(security [][]refdocs.SecurityUse, schemes map[string]refdocs.SecurityScheme) *Auth {
	for _, way := range security {
		if len(way) == 0 {
			// Anonymous calls are allowed.
			return nil
		}
		s, ok := schemes[way[0].Scheme]
		if !ok {
			continue
		}
		switch {
		case s.Type == "http" && strings.EqualFold(s.Scheme, "basic"):
			return &Auth{Type: AuthBasic, Username: "{{username}}", Password: "{{password}}"}
		case s.Type == "apiKey" && (s.In == "header" || s.In == "query"):
			return &Auth{Type: AuthAPIKey, Key: s.ParameterName, Value: "{{apiKey}}", In: s.In}
		case s.Type == "http" || s.Type == "oauth2" || s.Type == "openIdConnect":
			return &Auth{Type: AuthBearer, Token: "{{token}}"}
		}
	}
	return nil
}

// exampleValue is a parameter's value for a generated request: its
// example, default or first enum value, or one of its type.
func exampleValue(example json.RawMessage, schema *refdocs.Schema) string {
	candidates := []json.RawMessage{example}
	if schema != nil {
		candidates = append(candidates, schema.Example, schema.Default)
		if len(schema.Enum) > 0 {
			candidates = append(candidates, schema.Enum[0])
		}
	}
	for _, c := range candidates {
		if len(c) == 0 || string(c) == "null" {
			continue
		}
		var s string
		if json.Unmarshal(c, &s) == nil {
			return s
		}
		return string(c)
	}
	if schema != nil && len(schema.Types) > 0 {
		switch schema.Types[0] {
		case "integer", "number":
			return "1"
		case "boolean":
			return "true"
		}
	}
	return "example"
}

func validVariable(name string) bool {
	return name != "" && len(name) <= 200 && !strings.HasPrefix(name, "$") && !strings.ContainsAny(name, "{} \t\r\n\f\v")
}

// generatedRows adds rows for generated items, parents before their
// children, ranked after rank.
func generatedRows(rows *[][]any, collectionID, parent uuid.UUID, rank string, items []genItem) error {
	for _, it := range items {
		rank = after(rank)
		itemID := id.New()
		kind, req := KindFolder, []byte(nil)
		if it.request != nil {
			kind = KindRequest
			var err error
			if req, err = json.Marshal(it.request); err != nil {
				return err
			}
		}
		*rows = append(*rows, []any{itemID, collectionID, nullID(parent), rank, kind, it.name, it.description, req, true})
		if err := generatedRows(rows, collectionID, itemID, "", it.items); err != nil {
			return err
		}
	}
	return nil
}
