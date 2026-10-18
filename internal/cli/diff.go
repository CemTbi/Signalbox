// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/CemTbi/Signalbox/pkg/specdiff"
)

type diffOptions struct {
	format          string
	failOn          string
	minLevel        string
	allowRemoteRefs bool
}

func newDiffCommand() *cobra.Command {
	opts := diffOptions{}
	cmd := &cobra.Command{
		Use:   "diff <base> <head>",
		Short: "Report what changed between two versions of an API, and which changes break clients",
		Long: `Compare two versions of an OpenAPI or Swagger document and report what changed
for the API's clients. Breaking changes, such as a removed operation or a
parameter that became required, are errors. Changes that may break some
clients are warnings, and the rest are information.

base is the version clients use now, and head the new one. Use "-" to read
one of them from standard input.

Exit codes: 0 when nothing reaches the --fail-on level, 1 when something
does, 2 when a document can't be read or a flag is wrong.`,
		Example: `  signalbox diff v1.yaml v2.yaml
  # In CI, against the document on the main branch
  git show origin/main:openapi.yaml | signalbox diff --format github - openapi.yaml
  signalbox diff --format markdown --fail-on none old.yaml new.yaml > changes.md`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDiff(cmd, args[0], args[1], opts)
		},
	}
	f := cmd.Flags()
	f.StringVarP(&opts.format, "format", "f", "text", "output format: text, json, github or markdown")
	f.StringVar(&opts.failOn, "fail-on", "error", "exit with code 1 when a change is at least this severe: error, warning, info or none")
	f.StringVar(&opts.minLevel, "min-level", "info", "hide changes below this level: error, warning or info")
	f.BoolVar(&opts.allowRemoteRefs, "allow-remote-refs", false, "let $ref fetch http and https URLs")
	return cmd
}

func runDiff(cmd *cobra.Command, basePath, headPath string, opts diffOptions) error {
	usage := func(err error) error { return &ExitError{Code: ExitUsage, Err: err} }
	if basePath == "-" && headPath == "-" {
		return usage(errors.New("only one of the documents can come from standard input"))
	}
	failOn, failNever := specdiff.Level(""), strings.EqualFold(opts.failOn, "none")
	var err error
	if !failNever {
		if failOn, err = specdiff.ParseLevel(opts.failOn); err != nil {
			return usage(fmt.Errorf("--fail-on: %w", err))
		}
	}
	minLevel, err := specdiff.ParseLevel(opts.minLevel)
	if err != nil {
		return usage(fmt.Errorf("--min-level: %w", err))
	}
	printer, ok := diffPrinters[opts.format]
	if !ok {
		return usage(fmt.Errorf("unknown --format %q: use text, json, github or markdown", opts.format))
	}

	var docs [2]specdiff.Document
	for i, path := range []string{basePath, headPath} {
		data, name, dir, err := readInput(cmd.InOrStdin(), path)
		if err != nil {
			return usage(err)
		}
		docs[i] = specdiff.Document{Data: data, Name: name, Dir: dir}
	}
	report, err := specdiff.Compare(docs[0], docs[1], specdiff.Options{AllowRemoteReferences: opts.allowRemoteRefs})
	if err != nil {
		return usage(err)
	}

	shown := *report
	shown.Changes = nil
	for _, c := range report.Changes {
		if c.Level.AtLeast(minLevel) {
			shown.Changes = append(shown.Changes, c)
		}
	}
	if err := printer(cmd.OutOrStdout(), docs[0].Name, docs[1].Name, &shown); err != nil {
		return usage(err)
	}
	if !failNever {
		for _, c := range report.Changes {
			if c.Level.AtLeast(failOn) {
				return &ExitError{Code: ExitFindings}
			}
		}
	}
	return nil
}

type diffPrinter func(w io.Writer, base, head string, r *specdiff.Report) error

var diffPrinters = map[string]diffPrinter{
	"text":     printDiffText,
	"json":     printDiffJSON,
	"github":   printDiffGitHub,
	"markdown": printDiffMarkdown,
}

// where names the change's place in a document: the head's line, or the
// base's for something removed.
func where(base, head string, c specdiff.Change) (file string, line, col int) {
	switch {
	case c.Head != nil:
		return head, c.Head.Line, max(c.Head.Column, 1)
	case c.Base != nil:
		return base, c.Base.Line, max(c.Base.Column, 1)
	}
	return head, 1, 1
}

func operation(c specdiff.Change) string {
	if c.Method == "" && c.Path == "" {
		return ""
	}
	return strings.TrimSpace(c.Method+" "+c.Path) + ": "
}

func printDiffText(w io.Writer, base, head string, r *specdiff.Report) error {
	var b strings.Builder
	for _, c := range r.Changes {
		file, line, col := where(base, head, c)
		fmt.Fprintf(&b, "%s:%d:%d: %s %s: %s%s\n", file, line, col, c.Level, c.ID, operation(c), c.Message)
		if c.Comment != "" {
			fmt.Fprintf(&b, "    %s\n", c.Comment)
		}
	}
	if len(r.Changes) > 0 {
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "%s → %s: %s\n", base, head, summarizeDiff(r.Counts))
	_, err := io.WriteString(w, b.String())
	return err
}

func summarizeDiff(c specdiff.Counts) string {
	if c == (specdiff.Counts{}) {
		return "no changes for clients"
	}
	breaking := fmt.Sprintf("%d breaking changes", c.Errors)
	if c.Errors == 1 {
		breaking = "1 breaking change"
	}
	return fmt.Sprintf("%s, %s, %d info", breaking, plural(c.Warnings, "warning"), c.Infos)
}

func printDiffJSON(w io.Writer, base, head string, r *specdiff.Report) error {
	if r.Changes == nil {
		r.Changes = []specdiff.Change{}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(struct {
		Base string `json:"base"`
		Head string `json:"head"`
		*specdiff.Report
	}{base, head, r})
}

// printDiffGitHub writes workflow commands that GitHub Actions shows as
// annotations on the pull request diff.
func printDiffGitHub(w io.Writer, base, head string, r *specdiff.Report) error {
	var b strings.Builder
	for _, c := range r.Changes {
		level := "notice"
		switch c.Level {
		case specdiff.LevelError:
			level = "error"
		case specdiff.LevelWarning:
			level = "warning"
		}
		file, line, col := where(base, head, c)
		msg := operation(c) + c.Message
		if c.Comment != "" {
			msg += "\n" + c.Comment
		}
		fmt.Fprintf(&b, "::%s file=%s,line=%d,col=%d,title=%s::%s\n",
			level, escapeProperty(file), line, col, escapeProperty(c.ID), escapeData(msg))
	}
	fmt.Fprintf(&b, "%s → %s: %s\n", base, head, summarizeDiff(r.Counts))
	_, err := io.WriteString(w, b.String())
	return err
}

// printDiffMarkdown writes a summary for a pull request comment or a job
// summary: breaking changes first.
func printDiffMarkdown(w io.Writer, base, head string, r *specdiff.Report) error {
	var b strings.Builder
	fmt.Fprintf(&b, "### API changes: %s\n\n", summarizeDiff(r.Counts))
	if len(r.Changes) == 0 {
		b.WriteString("Nothing changed for the API's clients.\n")
		_, err := io.WriteString(w, b.String())
		return err
	}
	b.WriteString("| | Operation | Change |\n|---|---|---|\n")
	for _, want := range []specdiff.Level{specdiff.LevelError, specdiff.LevelWarning, specdiff.LevelInfo} {
		for _, c := range r.Changes {
			if c.Level != want {
				continue
			}
			mark := map[specdiff.Level]string{specdiff.LevelError: "🔴 Breaking", specdiff.LevelWarning: "🟡 Warning", specdiff.LevelInfo: "Info"}[c.Level]
			op := strings.TrimSpace(c.Method + " " + c.Path)
			if op != "" {
				op = "`" + op + "`"
			}
			fmt.Fprintf(&b, "| %s | %s | %s |\n", mark, op, markdownCell(c.Message))
		}
	}
	fmt.Fprintf(&b, "\nCompared `%s` with `%s`.\n", base, head)
	_, err := io.WriteString(w, b.String())
	return err
}

var cellEscaper = strings.NewReplacer("|", "\\|", "\n", " ", "\r", "")

func markdownCell(s string) string { return cellEscaper.Replace(s) }
