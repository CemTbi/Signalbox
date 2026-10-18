// SPDX-License-Identifier: Apache-2.0

// Package specsize measures how large an OpenAPI document's schemas become
// with their references expanded.
//
// Libraries that prepare request checks, generate data or compare documents
// expand references in place. A schema that refers to another twice, at
// each of 30 levels, expands to billions of schemas, so callers measure
// first and refuse documents past a limit. Counting is memoized per
// reference, so it takes time linear in the document's size.
package specsize

import (
	"github.com/pb33f/libopenapi/datamodel/high/base"
	v3 "github.com/pb33f/libopenapi/datamodel/high/v3"
	"github.com/pb33f/libopenapi/orderedmap"
)

// Operations counts the schemas of every parameter, request body and
// response in a document, each with its references expanded: what a request
// validator prepares, or a comparison walks. It stops counting once the
// count passes limit.
func Operations(m *v3.Document, limit int) int {
	if m.Paths == nil || m.Paths.PathItems == nil {
		return 0
	}
	sizes, active := map[string]int{}, map[string]bool{}
	n := 0
	add := func(p *base.SchemaProxy) {
		if n <= limit {
			n += expanded(p, limit-n, sizes, active)
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

// Schema counts the schemas in proxy with its references expanded, such
// as the values a body generated from it would contain. It stops counting
// once the count passes limit.
func Schema(proxy *base.SchemaProxy, limit int) int {
	return expanded(proxy, limit, map[string]int{}, map[string]bool{})
}

// expanded counts the schemas in proxy with references expanded, stopping
// once the count passes limit. A reference to a schema already being
// expanded counts once, as libopenapi stops there too. sizes remembers
// each referenced schema's size; active holds the references being
// expanded.
func expanded(proxy *base.SchemaProxy, limit int, sizes map[string]int, active map[string]bool) int {
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
			n += expanded(p, limit-n, sizes, active)
		}
	}
	// Every keyword that holds a schema; libopenapi expands references in
	// all of them.
	for _, m := range []*orderedmap.Map[string, *base.SchemaProxy]{s.Properties, s.PatternProperties, s.DependentSchemas} {
		if m != nil {
			for _, p := range m.FromOldest() {
				add(p)
			}
		}
	}
	for _, d := range []*base.DynamicValue[*base.SchemaProxy, bool]{s.Items, s.AdditionalProperties, s.UnevaluatedProperties} {
		if d != nil && d.IsA() {
			add(d.A)
		}
	}
	for _, list := range [][]*base.SchemaProxy{s.AllOf, s.OneOf, s.AnyOf, s.PrefixItems} {
		for _, p := range list {
			add(p)
		}
	}
	for _, p := range []*base.SchemaProxy{s.Not, s.If, s.Then, s.Else, s.Contains, s.PropertyNames, s.UnevaluatedItems, s.ContentSchema} {
		add(p)
	}
	// A count cut short at the limit is only good for this limit.
	if ref != "" && n <= limit {
		sizes[ref] = n
	}
	return n
}
