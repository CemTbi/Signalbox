// SPDX-License-Identifier: Apache-2.0

package convert

import (
	"encoding/json"
	"strings"
)

// The shapes export writes: Postman Collection v2.1.
type exItem struct {
	Name        string     `json:"name"`
	Description string     `json:"description,omitempty"`
	Item        *[]exItem  `json:"item,omitempty"`
	Request     *exRequest `json:"request,omitempty"`
	Event       []exEvent  `json:"event,omitempty"`
	Response    []any      `json:"response,omitempty"`
}

type exRequest struct {
	Method string     `json:"method"`
	Header []exHeader `json:"header"`
	URL    exURL      `json:"url"`
	Body   *exBody    `json:"body,omitempty"`
	Auth   *pmAuth    `json:"auth,omitempty"`
}

type exHeader struct {
	Key      string `json:"key"`
	Value    string `json:"value"`
	Disabled bool   `json:"disabled,omitempty"`
}

type exURL struct {
	Raw string `json:"raw"`
}

type exBody struct {
	Mode    string `json:"mode"`
	Raw     string `json:"raw"`
	Options *struct {
		Raw struct {
			Language string `json:"language"`
		} `json:"raw"`
	} `json:"options,omitempty"`
}

type exEvent struct {
	Listen string `json:"listen"`
	Script struct {
		Type string   `json:"type"`
		Exec []string `json:"exec"`
	} `json:"script"`
}

type exVariable struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// ExportPostman writes a collection as Postman Collection v2.1, which
// Postman and Newman read. id is the collection's ID, which Postman keeps
// as _postman_id.
func ExportPostman(id string, c Collection) ([]byte, error) {
	vars := make([]exVariable, len(c.Variables))
	for i, v := range c.Variables {
		vars[i] = exVariable{Key: v.Key, Value: v.Value}
	}
	out := struct {
		Info struct {
			PostmanID   string `json:"_postman_id"`
			Name        string `json:"name"`
			Description string `json:"description,omitempty"`
			Schema      string `json:"schema"`
		} `json:"info"`
		Item     []exItem     `json:"item"`
		Event    []exEvent    `json:"event,omitempty"`
		Variable []exVariable `json:"variable,omitempty"`
	}{Item: exportItems(c.Items), Event: events(c.Scripts), Variable: vars}
	out.Info.PostmanID, out.Info.Name, out.Info.Description, out.Info.Schema = id, c.Name, c.Description, postmanSchema
	return json.MarshalIndent(out, "", "\t")
}

func exportItems(items []Item) []exItem {
	out := make([]exItem, len(items))
	for i, it := range items {
		e := exItem{Name: it.Name, Description: it.Description, Event: events(it.Scripts)}
		if it.Request == nil {
			children := exportItems(it.Items)
			e.Item = &children
		} else {
			e.Request = exportRequest(it.Request)
			e.Response = []any{}
		}
		out[i] = e
	}
	return out
}

func exportRequest(r *Request) *exRequest {
	out := &exRequest{Method: r.Method, URL: exURL{Raw: r.URL}, Header: make([]exHeader, len(r.Headers))}
	for i, h := range r.Headers {
		out.Header[i] = exHeader{Key: h.Key, Value: h.Value, Disabled: !h.Enabled}
	}
	if r.Body != "" {
		out.Body = &exBody{Mode: "raw", Raw: r.Body}
		for _, h := range r.Headers {
			if h.Enabled && strings.EqualFold(h.Key, "Content-Type") && strings.Contains(strings.ToLower(h.Value), "json") {
				out.Body.Options = &struct {
					Raw struct {
						Language string `json:"language"`
					} `json:"raw"`
				}{}
				out.Body.Options.Raw.Language = "json"
			}
		}
	}
	if a := r.Auth; a != nil {
		switch a.Type {
		case "basic":
			out.Auth = &pmAuth{Type: "basic", Params: map[string]string{"username": a.Username, "password": a.Password}}
		case "bearer":
			out.Auth = &pmAuth{Type: "bearer", Params: map[string]string{"token": a.Token}}
		case "apikey":
			out.Auth = &pmAuth{Type: "apikey", Params: map[string]string{"key": a.Key, "value": a.Value, "in": a.In}}
		}
	} else {
		// No auth, rather than inheriting the collection's, which Postman
		// would do for a request without any.
		out.Auth = &pmAuth{Type: "noauth"}
	}
	return out
}

func events(s Scripts) []exEvent {
	var out []exEvent
	for _, e := range []struct{ listen, code string }{{"prerequest", s.PreRequest}, {"test", s.Test}} {
		if e.code == "" {
			continue
		}
		ev := exEvent{Listen: e.listen}
		ev.Script.Type = "text/javascript"
		ev.Script.Exec = strings.Split(e.code, "\n")
		out = append(out, ev)
	}
	return out
}
