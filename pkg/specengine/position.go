// SPDX-License-Identifier: Apache-2.0

package specengine

import (
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"

	"github.com/pb33f/go-yaml"
)

var (
	// "yaml: line 8: ..." and "line 8, column 3"
	errorLinePattern = regexp.MustCompile(`line (\d+)(?:, column (\d+))?`)
	// "go-yaml load error in scanner at L8.C19: ..."
	goYAMLErrorPattern = regexp.MustCompile(`go-yaml (?:\w+ )?error in \w+ at L(\d+)\.C(\d+): `)
)

// positionFromError recovers the position a YAML or JSON parser reported in
// its error, or returns an unknown position.
func positionFromError(err error, data []byte) Range {
	if err == nil {
		return pointAt(0, 0)
	}
	var syn *json.SyntaxError
	if errors.As(err, &syn) {
		line, col := offsetToLineColumn(data, syn.Offset)
		return pointAt(line, col)
	}
	msg := err.Error()
	if m := goYAMLErrorPattern.FindStringSubmatch(msg); m != nil {
		line, _ := strconv.Atoi(m[1])
		col, _ := strconv.Atoi(m[2])
		return pointAt(line, col)
	}
	if m := errorLinePattern.FindStringSubmatch(msg); m != nil {
		line, _ := strconv.Atoi(m[1])
		col := 0
		if m[2] != "" {
			col, _ = strconv.Atoi(m[2])
		}
		return pointAt(line, col)
	}
	return pointAt(0, 0)
}

// offsetToLineColumn converts a byte offset into a 1-based line and column.
func offsetToLineColumn(data []byte, offset int64) (int, int) {
	offset = min(max(offset, 0), int64(len(data)))
	line, col := 1, 1
	for _, b := range data[:offset] {
		if b == '\n' {
			line++
			col = 1
			continue
		}
		col++
	}
	return line, col
}

// syntaxMessage turns a parser error into a sentence a person can act on.
func syntaxMessage(msg string) string {
	msg = strings.TrimPrefix(msg, "unable to parse specification: ")
	switch {
	case goYAMLErrorPattern.MatchString(msg):
		loc := goYAMLErrorPattern.FindStringIndex(msg)
		return "Invalid YAML: " + strings.TrimSuffix(msg[loc[1]:], ".") + "."
	case strings.HasPrefix(msg, "yaml: "):
		rest := strings.TrimPrefix(msg, "yaml: ")
		if loc := errorLinePattern.FindStringIndex(rest); loc != nil && loc[0] == 0 {
			rest = strings.TrimLeft(rest[loc[1]:], ": ")
		}
		return "Invalid YAML: " + rest + "."
	case strings.HasPrefix(msg, "failed to decode YAML to JSON: "):
		return "The YAML can't be represented as JSON: " + strings.TrimPrefix(msg, "failed to decode YAML to JSON: ") + "."
	case strings.HasPrefix(msg, "failed to unmarshal JSON: "):
		return "Invalid JSON: " + strings.TrimPrefix(msg, "failed to unmarshal JSON: ") + "."
	}
	return msg
}

// versionRange finds the openapi or swagger field so an unsupported version
// can be pointed at.
func versionRange(root *yaml.Node) Range {
	if root == nil || len(root.Content) == 0 {
		return pointAt(0, 0)
	}
	m := root
	if m.Kind == yaml.DocumentNode {
		m = m.Content[0]
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if k := m.Content[i]; k.Value == "openapi" || k.Value == "swagger" {
			v := m.Content[i+1]
			return pointAt(v.Line, v.Column)
		}
	}
	return pointAt(0, 0)
}
