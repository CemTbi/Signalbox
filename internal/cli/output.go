// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/CemTbi/Signalbox/pkg/specengine"
)

type printer func(w io.Writer, name string, r specengine.Report) error

var printers = map[string]printer{
	"text":   printText,
	"json":   printJSON,
	"github": printGitHub,
}

// printText writes one line per finding in the file:line:col format that
// editors and terminals turn into links, then a summary.
func printText(w io.Writer, name string, r specengine.Report) error {
	var b strings.Builder
	for _, d := range r.Diagnostics {
		fmt.Fprintf(&b, "%s:%d:%d: %s %s: %s\n",
			name, d.Range.Start.Line, d.Range.Start.Column, d.Severity, d.Code, d.Message)
	}
	if len(r.Diagnostics) > 0 {
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "%s%s: %s\n", name, describeFormat(r), summarize(r.Counts))
	_, err := io.WriteString(w, b.String())
	return err
}

func describeFormat(r specengine.Report) string {
	switch {
	case r.Format == specengine.FormatSwagger20:
		return " (Swagger " + r.Version + ")"
	case r.Format != "":
		return " (OpenAPI " + r.Version + ")"
	}
	return ""
}

func summarize(c specengine.Counts) string {
	if c == (specengine.Counts{}) {
		return "no problems found"
	}
	return fmt.Sprintf("%s, %s, %s, %s",
		plural(c.Errors, "error"), plural(c.Warnings, "warning"), plural(c.Infos, "info"), plural(c.Hints, "hint"))
}

func plural(n int, noun string) string {
	if n == 1 || noun == "info" {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

type jsonReport struct {
	File string `json:"file"`
	specengine.Report
}

func printJSON(w io.Writer, name string, r specengine.Report) error {
	if r.Diagnostics == nil {
		r.Diagnostics = []specengine.Diagnostic{}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(jsonReport{File: name, Report: r})
}

// printGitHub writes workflow commands that GitHub Actions shows as
// annotations on the pull request diff.
// https://docs.github.com/actions/reference/workflow-commands-for-github-actions
func printGitHub(w io.Writer, name string, r specengine.Report) error {
	var b strings.Builder
	for _, d := range r.Diagnostics {
		level := "notice"
		switch d.Severity {
		case specengine.SeverityError:
			level = "error"
		case specengine.SeverityWarning:
			level = "warning"
		}
		fmt.Fprintf(&b, "::%s file=%s,line=%d,col=%d,title=%s::%s\n",
			level, escapeProperty(name), max(d.Range.Start.Line, 1), max(d.Range.Start.Column, 1),
			escapeProperty(d.Code), escapeData(d.Message))
	}
	fmt.Fprintf(&b, "%s: %s\n", name, summarize(r.Counts))
	_, err := io.WriteString(w, b.String())
	return err
}

var (
	dataEscaper     = strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A")
	propertyEscaper = strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A", ":", "%3A", ",", "%2C")
)

func escapeData(s string) string     { return dataEscaper.Replace(s) }
func escapeProperty(s string) string { return propertyEscaper.Replace(s) }
