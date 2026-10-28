// SPDX-License-Identifier: Apache-2.0

package convert

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// ErrNotPostman means the file isn't a Postman collection or environment
// Signalbox reads.
var ErrNotPostman = errors.New("convert: not a Postman file")

// The Postman Collection v2.1 schema, which exports name.
const postmanSchema = "https://schema.getpostman.com/json/collection/v2.1.0/collection.json"

// What Postman's JSON looks like. Postman writes several fields in more
// than one shape over its versions, so those are json.RawMessage.
type pmCollection struct {
	Info     pmInfo       `json:"info"`
	Item     []pmItem     `json:"item"`
	Event    []pmEvent    `json:"event"`
	Variable []pmVariable `json:"variable"`
	Auth     *pmAuth      `json:"auth"`
	// Requests marks the v1 format.
	Requests json.RawMessage `json:"requests"`
}

type pmInfo struct {
	PostmanID   string          `json:"_postman_id,omitempty"`
	Name        string          `json:"name"`
	Description json.RawMessage `json:"description,omitempty"`
	Schema      string          `json:"schema"`
}

type pmItem struct {
	Name        string          `json:"name"`
	Description json.RawMessage `json:"description,omitempty"`
	Item        *[]pmItem       `json:"item,omitempty"`
	Request     json.RawMessage `json:"request,omitempty"`
	Response    []any           `json:"response,omitempty"`
	Event       []pmEvent       `json:"event,omitempty"`
	Auth        *pmAuth         `json:"auth,omitempty"`
	Behavior    map[string]any  `json:"protocolProfileBehavior,omitempty"`
}

type pmRequest struct {
	Method      string          `json:"method"`
	Header      json.RawMessage `json:"header"`
	URL         json.RawMessage `json:"url"`
	Body        *pmBody         `json:"body"`
	Auth        *pmAuth         `json:"auth"`
	Description json.RawMessage `json:"description"`
}

type pmKeyValue struct {
	Key         string `json:"key"`
	Value       any    `json:"value"`
	Disabled    bool   `json:"disabled,omitempty"`
	Type        string `json:"type,omitempty"`
	Src         any    `json:"src,omitempty"`
	ContentType string `json:"contentType,omitempty"`
}

func (kv pmKeyValue) value() string {
	switch v := kv.Value.(type) {
	case nil:
		return ""
	case string:
		return v
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}

type pmURL struct {
	Raw      string          `json:"raw"`
	Protocol string          `json:"protocol"`
	Host     json.RawMessage `json:"host"`
	Port     string          `json:"port"`
	Path     json.RawMessage `json:"path"`
	Query    []pmKeyValue    `json:"query"`
	Variable []pmKeyValue    `json:"variable"`
}

type pmBody struct {
	Mode       string       `json:"mode"`
	Raw        string       `json:"raw"`
	URLEncoded []pmKeyValue `json:"urlencoded"`
	FormData   []pmKeyValue `json:"formdata"`
	GraphQL    *struct {
		Query     string `json:"query"`
		Variables string `json:"variables"`
	} `json:"graphql"`
	Options struct {
		Raw struct {
			Language string `json:"language"`
		} `json:"raw"`
	} `json:"options"`
	Disabled bool `json:"disabled"`
}

type pmEvent struct {
	Listen   string `json:"listen"`
	Disabled bool   `json:"disabled,omitempty"`
	Script   struct {
		Exec json.RawMessage `json:"exec"`
		Type string          `json:"type,omitempty"`
	} `json:"script"`
}

type pmVariable struct {
	Key      string `json:"key"`
	Value    any    `json:"value"`
	Type     string `json:"type,omitempty"`
	Disabled bool   `json:"disabled,omitempty"`
}

// pmAuth is an auth with its type's parameters, which v2.1 writes as a
// list of key-value pairs and v2.0 as an object.
type pmAuth struct {
	Type   string
	Params map[string]string
}

func (a *pmAuth) UnmarshalJSON(b []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	a.Params = map[string]string{}
	if t, ok := raw["type"]; ok {
		if err := json.Unmarshal(t, &a.Type); err != nil {
			return err
		}
	}
	params, ok := raw[a.Type]
	if !ok {
		return nil
	}
	var list []pmKeyValue
	if json.Unmarshal(params, &list) == nil {
		for _, kv := range list {
			a.Params[kv.Key] = kv.value()
		}
		return nil
	}
	var obj map[string]any
	if json.Unmarshal(params, &obj) == nil {
		for k, v := range obj {
			a.Params[k] = pmKeyValue{Value: v}.value()
		}
	}
	return nil
}

func (a *pmAuth) MarshalJSON() ([]byte, error) {
	keys := make([]string, 0, len(a.Params))
	for k := range a.Params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	params := make([]map[string]string, len(keys))
	for i, k := range keys {
		params[i] = map[string]string{"key": k, "value": a.Params[k], "type": "string"}
	}
	out := map[string]any{"type": a.Type}
	if len(params) > 0 {
		out[a.Type] = params
	}
	return json.Marshal(out)
}

// text reads a description, which is a string or {content}.
func text(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var d struct {
		Content string `json:"content"`
	}
	_ = json.Unmarshal(raw, &d)
	return d.Content
}

// lines reads a script, which is a list of lines or one string.
func lines(raw json.RawMessage) string {
	var list []string
	if json.Unmarshal(raw, &list) == nil {
		return strings.Join(list, "\n")
	}
	var s string
	_ = json.Unmarshal(raw, &s)
	return s
}

// parts reads a URL's host or path, which is a list or a string.
func parts(raw json.RawMessage, sep string) string {
	if len(raw) == 0 {
		return ""
	}
	var list []any
	if json.Unmarshal(raw, &list) == nil {
		ss := make([]string, 0, len(list))
		for _, p := range list {
			switch v := p.(type) {
			case string:
				ss = append(ss, v)
			case map[string]any:
				if s, ok := v["value"].(string); ok {
					ss = append(ss, s)
				}
			}
		}
		return strings.Join(ss, sep)
	}
	var s string
	_ = json.Unmarshal(raw, &s)
	return s
}

// importer carries a Postman import's state.
type importer struct {
	report   Report
	count    int
	varBytes int
}

// ReadPostmanCollection reads a Postman Collection v2.1 or v2.0 file.
func ReadPostmanCollection(data []byte) (Collection, Report, error) {
	if len(data) > MaxFileSize {
		return Collection{}, Report{}, fmt.Errorf("%w: the file is larger than 25 MiB", ErrNotPostman)
	}
	var pc pmCollection
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&pc); err != nil {
		return Collection{}, Report{}, fmt.Errorf("%w: it isn't valid JSON: %w", ErrNotPostman, err)
	}
	switch {
	case len(pc.Requests) > 0:
		return Collection{}, Report{}, fmt.Errorf("%w: it's a Postman v1 collection; export it from Postman again as Collection v2.1", ErrNotPostman)
	case !strings.Contains(pc.Info.Schema, "/collection/v2.1") && !strings.Contains(pc.Info.Schema, "/collection/v2.0"):
		return Collection{}, Report{}, fmt.Errorf("%w: it has no Postman collection schema; export the collection from Postman as Collection v2.1", ErrNotPostman)
	}
	im := &importer{}
	c := Collection{Variables: []Variable{}}
	c.Name = im.name("", pc.Info.Name, "Imported collection", MaxCollectionName)
	c.Description = im.description("", text(pc.Info.Description), MaxCollectionDescription)
	c.Scripts = im.scripts("", pc.Event)
	for _, v := range pc.Variable {
		im.collectionVariable(&c, v)
	}
	im.report.Variables = len(c.Variables)
	auth := effectiveAuth(nil, pc.Auth)
	items, err := im.items(pc.Item, "", 1, auth)
	if err != nil {
		return Collection{}, Report{}, err
	}
	c.Items = items
	if c.Scripts.PreRequest != "" || c.Scripts.Test != "" || im.hasScripts(items) {
		im.report.add("", Info, "Scripts are kept with their collection, folders and requests. Sending from the Requests page doesn't run them yet.")
	}
	return c, im.report, nil
}

func (im *importer) hasScripts(items []Item) bool {
	for _, it := range items {
		if it.Scripts.PreRequest != "" || it.Scripts.Test != "" || im.hasScripts(it.Items) {
			return true
		}
	}
	return false
}

func (im *importer) collectionVariable(c *Collection, v pmVariable) {
	key := strings.TrimSpace(v.Key)
	value := pmKeyValue{Value: v.Value}.value()
	switch {
	case v.Disabled:
		im.report.add("", Info, "The disabled variable %q wasn't imported; Postman doesn't use it either.", key)
	case !ValidKey(key):
		im.report.add("", Omitted, "The variable %q wasn't imported: names can't be empty, hold spaces or braces, or start with $.", key)
	case len(value) > MaxValue:
		im.report.add("", Omitted, "The variable %q wasn't imported: its value is larger than 64 KiB.", key)
	case len(c.Variables) >= MaxVariables:
		im.report.add("", Omitted, "The variable %q wasn't imported: a collection keeps at most %d variables.", key, MaxVariables)
	case im.varBytes+len(key)+len(value) > MaxCollectionVariables:
		im.report.add("", Omitted, "The variable %q wasn't imported: a collection's variables may hold at most 2 MiB together.", key)
	default:
		for i, have := range c.Variables {
			if have.Key == key {
				// Postman uses the last one.
				im.varBytes += len(value) - len(have.Value)
				c.Variables[i].Value = value
				return
			}
		}
		im.varBytes += len(key) + len(value)
		c.Variables = append(c.Variables, Variable{Key: key, Value: value})
	}
}

func (im *importer) name(item, name, fallback string, limit int) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return fallback
	}
	short, cut := shorten(name, limit)
	if cut {
		im.report.add(item, Info, "The name was cut to %d characters.", limit)
	}
	return short
}

func (im *importer) description(item, d string, limit int) string {
	short, cut := shorten(strings.TrimSpace(d), limit)
	if cut {
		im.report.add(item, Info, "The description was cut to %d characters.", limit)
	}
	return short
}

// effectiveAuth is the auth an item uses: its own, or what it inherits.
func effectiveAuth(inherited, own *pmAuth) *pmAuth {
	if own == nil || own.Type == "" || own.Type == "inherit" {
		return inherited
	}
	return own
}

func (im *importer) items(list []pmItem, path string, depth int, auth *pmAuth) ([]Item, error) {
	out := make([]Item, 0, len(list))
	for _, pi := range list {
		im.count++
		if im.count > MaxItems {
			return nil, fmt.Errorf("%w: the collection has more than %d folders and requests", ErrNotPostman, MaxItems)
		}
		isFolder := pi.Item != nil
		name := im.name(join(path, pi.Name), pi.Name, map[bool]string{true: "Untitled folder", false: "Untitled request"}[isFolder], MaxNameLength)
		where := join(path, name)
		it := Item{Name: name, Description: im.description(where, text(pi.Description), MaxDescription), Scripts: im.scripts(where, pi.Event)}
		if isFolder {
			if depth > MaxDepth {
				im.report.add(where, Omitted, "The folder wasn't imported: folders nest at most %d deep.", MaxDepth)
				continue
			}
			im.report.Folders++
			children, err := im.items(*pi.Item, where, depth+1, effectiveAuth(auth, pi.Auth))
			if err != nil {
				return nil, err
			}
			it.Items = children
			out = append(out, it)
			continue
		}
		req, ok := im.request(where, pi, auth)
		if !ok {
			continue
		}
		it.Request = req
		if it.Description == "" {
			var pr pmRequest
			if json.Unmarshal(pi.Request, &pr) == nil {
				it.Description = im.description(where, text(pr.Description), MaxDescription)
			}
		}
		if n := len(pi.Response); n > 0 {
			im.report.add(where, Info, "%d saved example responses weren't imported.", n)
		}
		im.report.Requests++
		out = append(out, it)
	}
	return out, nil
}

func join(path, name string) string {
	if path == "" {
		return name
	}
	return path + " / " + name
}

// request converts a request. It returns false when the request can't be
// imported at all.
func (im *importer) request(where string, pi pmItem, inherited *pmAuth) (*Request, bool) {
	var pr pmRequest
	var s string
	if json.Unmarshal(pi.Request, &s) == nil {
		// A request may be just its URL.
		pr = pmRequest{Method: "GET", URL: pi.Request}
	} else if err := json.Unmarshal(pi.Request, &pr); err != nil {
		im.report.add(where, Omitted, "The request wasn't imported: it isn't a Postman request.")
		return nil, false
	}
	r := &Request{Method: strings.ToUpper(strings.TrimSpace(pr.Method)), Headers: []Header{}}
	if r.Method == "" {
		r.Method = "GET"
	}
	if len(r.Method) > 32 {
		im.report.add(where, Omitted, "The request wasn't imported: its method is longer than 32 characters.")
		return nil, false
	}
	r.URL = im.url(where, pr.URL)
	if len(r.URL) > MaxURL {
		im.report.add(where, Omitted, "The request wasn't imported: its URL is longer than 8,192 characters.")
		return nil, false
	}
	var headers []pmKeyValue
	var headerText string
	if json.Unmarshal(pr.Header, &headerText) == nil {
		headers = parseHeaderText(headerText)
	} else {
		_ = json.Unmarshal(pr.Header, &headers)
	}
	for _, h := range headers {
		key := strings.TrimSpace(h.Key)
		switch {
		case key == "":
		case len(r.Headers) >= MaxHeaders:
			im.report.add(where, Omitted, "The header %q wasn't imported: a request keeps at most %d headers.", key, MaxHeaders)
		case len(key) > 1000 || len(h.value()) > MaxValue:
			im.report.add(where, Omitted, "The header %q wasn't imported: it is too long.", key)
		default:
			r.Headers = append(r.Headers, Header{Key: key, Value: h.value(), Enabled: !h.Disabled})
		}
	}
	im.body(where, r, pr.Body)
	r.Auth = im.auth(where, effectiveAuth(inherited, pr.Auth))
	for key := range pi.Behavior {
		if key != "disableBodyPruning" {
			im.report.add(where, Differs, "Postman's %q setting isn't supported; the runner's defaults apply.", key)
		}
	}
	im.builtins(where, r)
	return r, true
}

// parseHeaderText reads headers written as "Key: value" lines, as v2.0
// sometimes does.
func parseHeaderText(s string) []pmKeyValue {
	var out []pmKeyValue
	for _, line := range strings.Split(s, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if ok {
			out = append(out, pmKeyValue{Key: strings.TrimSpace(k), Value: strings.TrimSpace(v)})
		}
	}
	return out
}

var pathVariable = regexp.MustCompile(`/:([A-Za-z0-9_.-]+)`)

// url reads a URL, a string or an object, and fills in its path
// variables, such as :id, which Postman fills in from the URL's
// variables.
func (im *importer) url(where string, raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s)
	}
	var u pmURL
	if json.Unmarshal(raw, &u) != nil {
		return ""
	}
	out := strings.TrimSpace(u.Raw)
	if out == "" {
		out = buildURL(u)
	}
	values := map[string]string{}
	for _, v := range u.Variable {
		values[v.Key] = v.value()
	}
	if len(values) > 0 {
		base, query, hasQuery := strings.Cut(out, "?")
		base = pathVariable.ReplaceAllStringFunc(base, func(m string) string {
			name := m[2:]
			v, ok := values[name]
			if !ok {
				return m
			}
			if v == "" {
				im.report.add(where, Differs, "The path variable :%s has no value, so the URL keeps :%s.", name, name)
				return m
			}
			return "/" + v
		})
		out = base
		if hasQuery {
			out += "?" + query
		}
	}
	return out
}

func buildURL(u pmURL) string {
	var b strings.Builder
	if u.Protocol != "" {
		b.WriteString(u.Protocol + "://")
	}
	b.WriteString(parts(u.Host, "."))
	if u.Port != "" {
		b.WriteString(":" + u.Port)
	}
	if p := parts(u.Path, "/"); p != "" {
		b.WriteString("/" + strings.TrimPrefix(p, "/"))
	}
	var q []string
	for _, kv := range u.Query {
		if kv.Disabled {
			continue
		}
		if kv.Value == nil {
			q = append(q, kv.Key)
		} else {
			q = append(q, kv.Key+"="+kv.value())
		}
	}
	if len(q) > 0 {
		b.WriteString("?" + strings.Join(q, "&"))
	}
	return b.String()
}

// rawTypes are the Content-Types Postman sends with raw bodies, by the
// body's language, when the request sets none.
var rawTypes = map[string]string{
	"json": "application/json", "text": "text/plain", "javascript": "application/javascript",
	"html": "text/html", "xml": "application/xml",
}

func (im *importer) body(where string, r *Request, b *pmBody) {
	if b == nil || b.Disabled {
		return
	}
	var body, contentType string
	switch b.Mode {
	case "", "none":
		return
	case "raw":
		body, contentType = b.Raw, rawTypes[b.Options.Raw.Language]
	case "urlencoded":
		var pairs []string
		vars := false
		for _, kv := range b.URLEncoded {
			if kv.Disabled {
				continue
			}
			vars = vars || strings.Contains(kv.Key+kv.value(), "{{")
			pairs = append(pairs, formEscape(kv.Key)+"="+formEscape(kv.value()))
		}
		body, contentType = strings.Join(pairs, "&"), "application/x-www-form-urlencoded"
		if vars {
			im.report.add(where, Differs, "Variables in form fields are filled in as they are, without form encoding; Postman encodes their values.")
		}
	case "formdata":
		var fields []formField
		for _, kv := range b.FormData {
			if kv.Disabled {
				continue
			}
			if kv.Type == "file" {
				im.report.add(where, Omitted, "The file field %q wasn't imported; file fields aren't supported yet.", kv.Key)
				continue
			}
			fields = append(fields, formField{name: kv.Key, value: kv.value(), contentType: kv.ContentType})
		}
		body, contentType = multipartBody(fields)
	case "graphql":
		if b.GraphQL == nil {
			return
		}
		query, _ := json.Marshal(b.GraphQL.Query)
		vars := strings.TrimSpace(b.GraphQL.Variables)
		if vars == "" {
			vars = "{}"
		} else if !json.Valid([]byte(vars)) && !strings.Contains(vars, "{{") {
			im.report.add(where, Differs, "The GraphQL variables aren't valid JSON, so they are sent as written.")
		}
		body, contentType = `{"query":`+string(query)+`,"variables":`+vars+`}`, "application/json"
	case "file":
		im.report.add(where, Omitted, "The file body wasn't imported; file bodies aren't supported yet.")
		return
	default:
		im.report.add(where, Omitted, "The %q body wasn't imported; Signalbox doesn't know that body mode.", b.Mode)
		return
	}
	if len(body) > MaxBody {
		im.report.add(where, Omitted, "The body wasn't imported: it is larger than 10 MiB.")
		return
	}
	r.Body = body
	if contentType != "" && !hasHeader(r.Headers, "Content-Type") {
		r.Headers = append(r.Headers, Header{Key: "Content-Type", Value: contentType, Enabled: true})
	}
}

func hasHeader(hs []Header, name string) bool {
	for _, h := range hs {
		if h.Enabled && strings.EqualFold(h.Key, name) {
			return true
		}
	}
	return false
}

var variablePattern = regexp.MustCompile(`\{\{[^{}]*\}\}`)

// formEscape escapes a form value but keeps {{variables}} as they are, so
// they're filled in when the request is sent.
func formEscape(s string) string {
	var b strings.Builder
	last := 0
	for _, loc := range variablePattern.FindAllStringIndex(s, -1) {
		b.WriteString(url.QueryEscape(s[last:loc[0]]))
		b.WriteString(s[loc[0]:loc[1]])
		last = loc[1]
	}
	b.WriteString(url.QueryEscape(s[last:]))
	return b.String()
}

func (im *importer) auth(where string, a *pmAuth) *Auth {
	if a == nil {
		return nil
	}
	p := a.Params
	switch a.Type {
	case "noauth":
		return nil
	case "basic":
		return &Auth{Type: "basic", Username: p["username"], Password: p["password"]}
	case "bearer":
		return &Auth{Type: "bearer", Token: p["token"]}
	case "apikey":
		in := "header"
		if p["in"] == "query" {
			in = "query"
		}
		return &Auth{Type: "apikey", Key: p["key"], Value: p["value"], In: in}
	default:
		im.report.add(where, Omitted, "Postman's %s auth isn't supported yet, so the request is sent without it.", a.Type)
		return nil
	}
}

func (im *importer) builtins(where string, r *Request) {
	texts := []string{r.URL, r.Body}
	for _, h := range r.Headers {
		texts = append(texts, h.Key, h.Value)
	}
	if a := r.Auth; a != nil {
		texts = append(texts, a.Username, a.Password, a.Token, a.Key, a.Value)
	}
	seen := map[string]bool{}
	for _, t := range texts {
		for _, name := range unsupportedBuiltins(t) {
			if !seen[name] {
				seen[name] = true
				im.report.add(where, Differs, "{{%s}} isn't filled in, so it's sent as typed.", name)
			}
		}
	}
}

func (im *importer) scripts(where string, events []pmEvent) Scripts {
	var s Scripts
	for _, e := range events {
		code := lines(e.Script.Exec)
		if strings.TrimSpace(code) == "" {
			continue
		}
		if e.Disabled {
			im.report.add(where, Info, "The disabled %s script wasn't imported.", e.Listen)
			continue
		}
		switch e.Listen {
		case "prerequest":
			s.PreRequest = joinScript(s.PreRequest, code)
		case "test":
			s.Test = joinScript(s.Test, code)
		default:
			im.report.add(where, Omitted, "The %q script wasn't imported; Signalbox runs pre-request and test scripts.", e.Listen)
			continue
		}
		for _, problem := range checkScript(code) {
			im.report.add(where, Differs, "The %s script %s.", e.Listen, problem)
		}
	}
	if len(s.PreRequest) > 256<<10 || len(s.Test) > 256<<10 {
		im.report.add(where, Omitted, "The scripts weren't imported: each may be at most 256 KiB.")
		return Scripts{}
	}
	return s
}

func joinScript(a, b string) string {
	if a == "" {
		return b
	}
	return a + "\n" + b
}

// The sandbox's pm members and modules (docs/adr/0017-postman-scripts.md).
var (
	sandboxMembers = map[string]bool{
		"request": true, "response": true, "test": true, "expect": true, "environment": true, "collectionVariables": true,
		"globals": true, "iterationData": true, "variables": true, "info": true, "execution": true, "sendRequest": true,
	}
	sandboxModules = map[string]bool{
		"ajv": true, "atob": true, "btoa": true, "buffer": true, "chai": true, "crypto-js": true, "lodash": true, "moment": true,
		"postman-collection": true, "tv4": true, "url": true, "uuid": true, "xml2js": true,
	}
	pmMember      = regexp.MustCompile(`\bpm\.([A-Za-z_$][A-Za-z0-9_$]*)`)
	requireCall   = regexp.MustCompile(`\brequire\(\s*['"]([^'"]+)['"]\s*\)`)
	nextRequest   = regexp.MustCompile(`\bsetNextRequest\s*\(`)
	cheerioGlobal = regexp.MustCompile(`\bcheerio\b`)
)

// checkScript lists what a script uses that the sandbox lacks, by reading
// it, not running it.
func checkScript(code string) []string {
	var out []string
	seen := map[string]bool{}
	note := func(s string) {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, m := range pmMember.FindAllStringSubmatch(code, -1) {
		if !sandboxMembers[m[1]] {
			note(fmt.Sprintf("uses pm.%s, which Signalbox's sandbox doesn't have", m[1]))
		}
	}
	for _, m := range requireCall.FindAllStringSubmatch(code, -1) {
		if !sandboxModules[m[1]] {
			note(fmt.Sprintf("requires %q, which Signalbox's sandbox doesn't have", m[1]))
		}
	}
	if nextRequest.MatchString(code) {
		note("calls setNextRequest, which Signalbox's runs don't follow yet")
	}
	if cheerioGlobal.MatchString(code) && !strings.Contains(code, "require(") {
		note("uses cheerio, which Signalbox's sandbox doesn't have")
	}
	return out
}
