// SPDX-License-Identifier: Apache-2.0

package client

import (
	"encoding/base64"
	"net/url"
	"strings"

	"github.com/CemTbi/Signalbox/internal/modules/runner"
	"github.com/CemTbi/Signalbox/internal/platform/validate"
)

// Auth types.
const (
	AuthNone   = "none"
	AuthBasic  = "basic"
	AuthBearer = "bearer"
	AuthAPIKey = "apikey"
)

// Where an API key goes.
const (
	InHeader = "header"
	InQuery  = "query"
)

// maxAuthField bounds each of an auth's fields.
const maxAuthField = 65536

// Auth is how a request proves who sends it. Its fields may hold
// {{variables}}, which are filled in before it is applied, so a basic
// password can be a secret.
type Auth struct {
	Type string `json:"type"`
	// Username and Password are for basic auth.
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	// Token is for bearer auth.
	Token string `json:"token,omitempty"`
	// Key, Value and In are for an API key: a header, or a query
	// parameter.
	Key   string `json:"key,omitempty"`
	Value string `json:"value,omitempty"`
	In    string `json:"in,omitempty"`
}

// checkAuth validates an auth at pointer and normalizes it: no auth is
// nil, and only the fields its type uses are kept.
func checkAuth(errs *validate.Errors, pointer string, a *Auth) *Auth {
	if a == nil {
		return nil
	}
	out := Auth{Type: strings.ToLower(strings.TrimSpace(a.Type))}
	switch out.Type {
	case "", AuthNone:
		return nil
	case AuthBasic:
		out.Username, out.Password = a.Username, a.Password
	case AuthBearer:
		out.Token = a.Token
	case AuthAPIKey:
		out.Key, out.Value, out.In = strings.TrimSpace(a.Key), a.Value, strings.ToLower(strings.TrimSpace(a.In))
		if out.In == "" {
			out.In = InHeader
		}
		if out.Key == "" {
			errs.Add(pointer+"/key", "required", "Name the header or query parameter the key goes in, such as X-Api-Key.")
		}
		if out.In != InHeader && out.In != InQuery {
			errs.Add(pointer+"/in", "enum", "Send the key in a header or in the query.")
		}
	default:
		errs.Add(pointer+"/type", "enum", "Use none, basic, bearer or apikey.")
		return nil
	}
	for name, v := range map[string]string{"username": out.Username, "password": out.Password, "token": out.Token, "key": out.Key, "value": out.Value} {
		if len(v) > maxAuthField {
			errs.Add(pointer+"/"+name, "maxLength", "Use at most 64 KiB.")
		}
	}
	return &out
}

// resolve fills in the auth's variables.
func (a *Auth) resolve(r *resolver) *Auth {
	if a == nil {
		return nil
	}
	out := *a
	for _, f := range []*string{&out.Username, &out.Password, &out.Token, &out.Key, &out.Value} {
		*f = r.expand(*f, 0)
	}
	return &out
}

// apply adds the auth to a request: basic and bearer as Authorization, an
// API key as its header or query parameter. It replaces headers of the
// same name the request has. It returns the values it sent, which history
// masks.
func (a *Auth) apply(req *runner.Request) []string {
	if a == nil {
		return nil
	}
	var name, value string
	var sent []string
	switch a.Type {
	case AuthBasic:
		name, value = "Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(a.Username+":"+a.Password))
		sent = []string{value[len("Basic "):], a.Password}
	case AuthBearer:
		name, value = "Authorization", "Bearer "+a.Token
		sent = []string{a.Token}
	case AuthAPIKey:
		sent = []string{a.Value}
		if a.In == InQuery {
			u, err := url.Parse(req.URL)
			if err != nil {
				// The runner refuses the URL and says why.
				return sent
			}
			q := url.QueryEscape(a.Key) + "=" + url.QueryEscape(a.Value)
			if u.RawQuery != "" {
				q = u.RawQuery + "&" + q
			}
			u.RawQuery = q
			req.URL = u.String()
			// The query carries it escaped.
			sent = append(sent, url.QueryEscape(a.Value))
			return sent
		}
		name, value = a.Key, a.Value
	default:
		return nil
	}
	headers := make([]runner.Header, 0, len(req.Headers)+1)
	for _, h := range req.Headers {
		if !strings.EqualFold(strings.TrimSpace(h.Key), name) {
			headers = append(headers, h)
		}
	}
	req.Headers = append(headers, runner.Header{Key: name, Value: value})
	return sent
}

// kept is what history keeps of an auth: its type and the fields that
// aren't credentials.
func (a *Auth) kept() *Auth {
	if a == nil {
		return nil
	}
	return &Auth{Type: a.Type, Username: a.Username, Key: a.Key, In: a.In}
}
