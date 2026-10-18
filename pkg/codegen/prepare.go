// SPDX-License-Identifier: Apache-2.0

package codegen

import (
	"fmt"
	"sort"
	"strings"

	"github.com/pb33f/go-yaml"
)

// Warning is something in the document that the generated code can't
// express, at its line in the document.
type Warning struct {
	Line    int    `json:"line"`
	Column  int    `json:"column"`
	Message string `json:"message"`
}

// prepare rewrites what generators can't read into equivalents they can,
// and reports what they will ignore:
//   - a schema with several types, such as [string, integer], becomes a
//     oneOf of one schema per type;
//   - const becomes an enum with one value.
//
// Other JSON Schema keywords that OpenAPI 3.0 lacks, such as prefixItems or
// if, are left as they are and reported, as are operations whose methods
// generators don't know.
func prepare(data []byte) ([]byte, []Warning, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, nil, err
	}
	if len(root.Content) == 0 || root.Content[0].Kind != yaml.MappingNode {
		return data, nil, nil
	}
	p := &preparer{}
	doc := root.Content[0]
	// Swagger 2.0 has none of OpenAPI 3.1's keywords.
	if value(doc, "swagger") != nil {
		return data, nil, nil
	}
	p.document(doc)
	sort.SliceStable(p.warnings, func(i, j int) bool {
		a, b := p.warnings[i], p.warnings[j]
		return a.Line < b.Line || a.Line == b.Line && a.Column < b.Column
	})
	if !p.changed {
		return data, p.warnings, nil
	}
	out, err := yaml.Marshal(&root)
	if err != nil {
		return nil, nil, err
	}
	return out, p.warnings, nil
}

type preparer struct {
	warnings []Warning
	changed  bool
}

func (p *preparer) warn(n *yaml.Node, format string, args ...any) {
	p.warnings = append(p.warnings, Warning{Line: n.Line, Column: n.Column, Message: fmt.Sprintf(format, args...)})
}

func (p *preparer) document(doc *yaml.Node) {
	if paths := value(doc, "paths"); paths != nil && paths.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(paths.Content); i += 2 {
			item := paths.Content[i+1]
			if item.Kind != yaml.MappingNode {
				continue
			}
			for j := 0; j+1 < len(item.Content); j += 2 {
				key := item.Content[j]
				switch key.Value {
				case "query":
					p.warn(key, "The QUERY operation on %s isn't generated: generators don't support OpenAPI 3.2's QUERY method yet.", paths.Content[i].Value)
				case "additionalOperations":
					p.warn(key, "The additional operations on %s aren't generated: generators only know the standard HTTP methods.", paths.Content[i].Value)
				}
			}
		}
	}
	p.walk(doc)
}

// Keywords whose value is a schema, a map of schemas or a list of schemas.
var (
	schemaKeys     = setOf("items", "additionalProperties", "not", "if", "then", "else", "contains", "propertyNames", "unevaluatedItems", "unevaluatedProperties", "contentSchema")
	schemaMapKeys  = setOf("properties", "patternProperties", "$defs", "definitions", "dependentSchemas")
	schemaListKeys = setOf("allOf", "oneOf", "anyOf", "prefixItems")
	// Keys outside schemas whose values are data, not description.
	dataKeys = setOf("example", "examples", "default", "value", "enum", "const")
)

func setOf(keys ...string) map[string]bool {
	m := make(map[string]bool, len(keys))
	for _, k := range keys {
		m[k] = true
	}
	return m
}

// walk visits the document outside schemas, looking for the places schemas
// are: under schema keys, and in components' schemas.
func (p *preparer) walk(n *yaml.Node) {
	switch n.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			key, v := n.Content[i].Value, n.Content[i+1]
			switch {
			case strings.HasPrefix(key, "x-") || dataKeys[key]:
			case key == "schema":
				p.schema(v)
			case key == "schemas" && v.Kind == yaml.MappingNode:
				for j := 1; j < len(v.Content); j += 2 {
					p.schema(v.Content[j])
				}
			default:
				p.walk(v)
			}
		}
	case yaml.SequenceNode:
		for _, c := range n.Content {
			p.walk(c)
		}
	}
}

// schema rewrites and checks one schema and the schemas inside it.
func (p *preparer) schema(n *yaml.Node) {
	if n.Kind != yaml.MappingNode {
		return
	}
	p.multipleTypes(n)
	p.constant(n)
	for i := 0; i+1 < len(n.Content); i += 2 {
		key, v := n.Content[i], n.Content[i+1]
		switch k := key.Value; k {
		case "prefixItems":
			p.warn(key, "prefixItems isn't expressed: the generated type is a list of any values.")
		case "if":
			p.warn(key, "if, then and else aren't expressed: the generated code doesn't check the condition.")
		case "patternProperties":
			p.warn(key, "patternProperties isn't expressed: keys matching the patterns are values of any type.")
		case "dependentSchemas", "dependentRequired":
			p.warn(key, "%s isn't expressed: the generated code doesn't check it.", k)
		case "$dynamicRef":
			p.warn(key, "$dynamicRef isn't followed: the generated type is a value of any type.")
		}
		switch k := key.Value; {
		case schemaKeys[k]:
			p.schema(v)
		case schemaMapKeys[k] && v.Kind == yaml.MappingNode:
			for j := 1; j < len(v.Content); j += 2 {
				p.schema(v.Content[j])
			}
		case schemaListKeys[k] && v.Kind == yaml.SequenceNode:
			for _, c := range v.Content {
				p.schema(c)
			}
		}
	}
}

// multipleTypes turns type: [string, integer] into a oneOf of one schema
// per type. null stays as nullable.
func (p *preparer) multipleTypes(n *yaml.Node) {
	i := index(n, "type")
	if i < 0 || n.Content[i+1].Kind != yaml.SequenceNode {
		return
	}
	types := n.Content[i+1]
	var kept []*yaml.Node
	null := false
	for _, t := range types.Content {
		if t.Value == "null" {
			null = true
			continue
		}
		kept = append(kept, t)
	}
	if len(kept) < 2 {
		return
	}
	if index(n, "oneOf") >= 0 || index(n, "anyOf") >= 0 {
		p.warn(types, "This schema allows several types and also has oneOf or anyOf: generators can't express both, so the generated type is a value of any type.")
		n.Content = append(n.Content[:i], n.Content[i+2:]...)
		p.changed = true
		return
	}
	names := make([]string, len(kept))
	variants := &yaml.Node{Kind: yaml.SequenceNode}
	for j, t := range kept {
		names[j] = t.Value
		variants.Content = append(variants.Content, &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{
			scalar("type"), scalar(t.Value),
		}})
	}
	n.Content[i], n.Content[i+1] = scalar("oneOf"), variants
	if null && index(n, "nullable") < 0 {
		n.Content = append(n.Content, scalar("nullable"), &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "true"})
	}
	p.changed = true
}

// constant turns const: v into enum: [v], which generators read as a
// type with one value.
func (p *preparer) constant(n *yaml.Node) {
	i := index(n, "const")
	if i < 0 || index(n, "enum") >= 0 {
		return
	}
	v := n.Content[i+1]
	if v.Kind != yaml.ScalarNode {
		p.warn(n.Content[i], "const with an object or list value isn't expressed: the generated type is a value of any type.")
		return
	}
	n.Content[i] = scalar("enum")
	n.Content[i+1] = &yaml.Node{Kind: yaml.SequenceNode, Content: []*yaml.Node{v}}
	if index(n, "type") < 0 {
		var t string
		switch v.ShortTag() {
		case "!!str":
			t = "string"
		case "!!int":
			t = "integer"
		case "!!float":
			t = "number"
		case "!!bool":
			t = "boolean"
		}
		if t != "" {
			n.Content = append(n.Content, scalar("type"), scalar(t))
		}
	}
	p.changed = true
}

func scalar(s string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s} }

// index returns the position of key's node in a mapping, or -1.
func index(m *yaml.Node, key string) int {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return i
		}
	}
	return -1
}

func value(m *yaml.Node, key string) *yaml.Node {
	if i := index(m, key); i >= 0 {
		return m.Content[i+1]
	}
	return nil
}
