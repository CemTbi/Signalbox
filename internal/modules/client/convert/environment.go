// SPDX-License-Identifier: Apache-2.0

package convert

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// Environment is a Postman environment's variables.
type Environment struct {
	Name      string
	Variables []Variable
}

type pmEnvironment struct {
	Name   string `json:"name"`
	Scope  string `json:"_postman_variable_scope"`
	Values []struct {
		Key     string `json:"key"`
		Value   any    `json:"value"`
		Enabled *bool  `json:"enabled"`
		Type    string `json:"type"`
	} `json:"values"`
	// Info marks a collection, sent here by mistake.
	Info json.RawMessage `json:"info"`
}

// ReadPostmanEnvironment reads a Postman environment file. Secret
// variables, Postman's type secret, stay secret.
func ReadPostmanEnvironment(data []byte) (Environment, Report, error) {
	if len(data) > MaxFileSize {
		return Environment{}, Report{}, fmt.Errorf("%w: the file is larger than 25 MiB", ErrNotPostman)
	}
	var pe pmEnvironment
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&pe); err != nil {
		return Environment{}, Report{}, fmt.Errorf("%w: it isn't valid JSON: %w", ErrNotPostman, err)
	}
	switch {
	case len(pe.Info) > 0:
		return Environment{}, Report{}, fmt.Errorf("%w: it's a collection; import it under Collections instead", ErrNotPostman)
	case pe.Values == nil || (pe.Scope != "" && pe.Scope != "environment"):
		return Environment{}, Report{}, fmt.Errorf("%w: it isn't a Postman environment; export one from Postman's Environments", ErrNotPostman)
	}
	var r Report
	env := Environment{Name: strings.TrimSpace(pe.Name), Variables: []Variable{}}
	index := map[string]int{}
	for _, v := range pe.Values {
		key := strings.TrimSpace(v.Key)
		value := pmKeyValue{Value: v.Value}.value()
		switch {
		case key == "":
		case v.Enabled != nil && !*v.Enabled:
			r.add("", Info, "The disabled variable %q wasn't imported; Postman doesn't use it either.", key)
		case !ValidKey(key):
			r.add("", Omitted, "The variable %q wasn't imported: names can't hold spaces or braces, or start with $.", key)
		case len(value) > MaxValue:
			r.add("", Omitted, "The variable %q wasn't imported: its value is larger than 64 KiB.", key)
		default:
			if i, ok := index[key]; ok {
				env.Variables[i] = Variable{Key: key, Value: value, Secret: v.Type == "secret"}
				continue
			}
			index[key] = len(env.Variables)
			env.Variables = append(env.Variables, Variable{Key: key, Value: value, Secret: v.Type == "secret"})
		}
	}
	r.Variables = len(env.Variables)
	return env, r, nil
}
