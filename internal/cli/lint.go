// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/CemTbi/Signalbox/pkg/specengine"
)

// maxSpecSize bounds how much the CLI reads, so a wrong path such as a
// disk image fails fast instead of exhausting memory.
const maxSpecSize = 64 << 20

type lintOptions struct {
	format          string
	ruleSets        string
	failOn          string
	minSeverity     string
	allowRemoteRefs bool
}

func newLintCommand() *cobra.Command {
	opts := lintOptions{}
	cmd := &cobra.Command{
		Use:   "lint <file>",
		Short: "Validate an OpenAPI document and check it against lint rules",
		Long: `Validate an OpenAPI or Swagger document and check it against lint rules.

Validation reports what makes the document invalid: syntax errors, fields the
OpenAPI version doesn't allow, and $refs that point nowhere. Lint rules report
quality and security issues. Use "-" to read the document from standard input.

Exit codes: 0 when nothing reaches the --fail-on severity, 1 when something
does, 2 when the document can't be read or a flag is wrong.`,
		Example: `  signalbox lint openapi.yaml
  signalbox lint --ruleset recommended,owasp --fail-on warning openapi.yaml
  signalbox lint --format github openapi.yaml   # annotations in GitHub Actions
  cat openapi.json | signalbox lint -`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLint(cmd.Context(), cmd, args[0], opts)
		},
	}
	f := cmd.Flags()
	f.StringVarP(&opts.format, "format", "f", "text", "output format: text, json or github")
	f.StringVarP(&opts.ruleSets, "ruleset", "r", "recommended", "lint rule sets: recommended, owasp, all or none, comma-separated")
	f.StringVar(&opts.failOn, "fail-on", "error", "exit with code 1 when a finding is at least this severe: error, warning, info, hint or none")
	f.StringVar(&opts.minSeverity, "min-severity", "hint", "hide findings below this severity: error, warning, info or hint")
	f.BoolVar(&opts.allowRemoteRefs, "allow-remote-refs", false, "let $ref fetch http and https URLs")
	return cmd
}

func runLint(ctx context.Context, cmd *cobra.Command, path string, opts lintOptions) error {
	if ctx == nil {
		ctx = context.Background()
	}
	usage := func(err error) error { return &ExitError{Code: ExitUsage, Err: err} }

	sets, err := specengine.ParseRuleSets(opts.ruleSets)
	if err != nil {
		return usage(err)
	}
	failOn, failNever := specengine.Severity(""), strings.EqualFold(opts.failOn, "none")
	if !failNever {
		if failOn, err = specengine.ParseSeverity(opts.failOn); err != nil {
			return usage(fmt.Errorf("--fail-on: %w", err))
		}
	}
	minSeverity, err := specengine.ParseSeverity(opts.minSeverity)
	if err != nil {
		return usage(fmt.Errorf("--min-severity: %w", err))
	}
	printer, ok := printers[opts.format]
	if !ok {
		return usage(fmt.Errorf("unknown --format %q: use text, json or github", opts.format))
	}

	data, name, baseDir, err := readInput(cmd.InOrStdin(), path)
	if err != nil {
		return usage(err)
	}
	report, err := specengine.Check(ctx, data, specengine.Options{
		SourceName:            name,
		BaseDir:               baseDir,
		AllowRemoteReferences: opts.allowRemoteRefs,
	}, sets...)
	if err != nil {
		return usage(fmt.Errorf("checking %s: %w", name, err))
	}

	shown := report
	shown.Diagnostics = nil
	for _, d := range report.Diagnostics {
		if d.Severity.AtLeast(minSeverity) {
			shown.Diagnostics = append(shown.Diagnostics, d)
		}
	}
	if err := printer(cmd.OutOrStdout(), name, shown); err != nil {
		return usage(err)
	}

	if !failNever {
		for _, d := range report.Diagnostics {
			if d.Severity.AtLeast(failOn) {
				return &ExitError{Code: ExitFindings}
			}
		}
	}
	return nil
}

// readInput reads the document and works out the name to report it under and
// the directory its relative file references resolve from.
func readInput(stdin io.Reader, path string) (data []byte, name, baseDir string, err error) {
	if path == "-" {
		data, err = io.ReadAll(io.LimitReader(stdin, maxSpecSize+1))
		name = "stdin"
		baseDir, _ = os.Getwd()
	} else {
		var f *os.File
		if f, err = os.Open(path); err != nil {
			return nil, "", "", fmt.Errorf("can't read %s: %w", path, err)
		}
		defer f.Close()
		data, err = io.ReadAll(io.LimitReader(f, maxSpecSize+1))
		name = path
		if abs, absErr := filepath.Abs(path); absErr == nil {
			baseDir = filepath.Dir(abs)
		}
	}
	if err != nil {
		return nil, "", "", fmt.Errorf("can't read %s: %w", name, err)
	}
	if len(data) > maxSpecSize {
		return nil, "", "", fmt.Errorf("%s is larger than %d MiB, which is more than any OpenAPI document should be", name, maxSpecSize>>20)
	}
	return data, name, baseDir, nil
}
