// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/CemTbi/Signalbox/pkg/codegen"
	"github.com/CemTbi/Signalbox/pkg/specload"
)

type codegenOptions struct {
	target          string
	pkg             string
	out             string
	failOnWarning   bool
	allowRemoteRefs bool
}

func newCodegenCommand() *cobra.Command {
	opts := codegenOptions{}
	var targets strings.Builder
	for _, t := range codegen.Targets {
		fmt.Fprintf(&targets, "  %-10s %s\n", t.ID, t.Description)
	}
	cmd := &cobra.Command{
		Use:   "codegen --target <target> <file>",
		Short: "Generate a client or server stub from an OpenAPI document",
		Long: `Generate code from an OpenAPI or Swagger document into a package directory
under --out. Generated files end in .gen.go; generate them again when the API
changes instead of editing them. A README is written beside them once.

Targets:
` + targets.String() + `
What the generated code can't express, such as OpenAPI 3.1's prefixItems, is
reported as a warning with its line.

Exit codes: 0 when the code was generated, 1 with --fail-on-warning when
there were warnings, 2 when the document can't be read or a flag is wrong.`,
		Example: `  signalbox codegen --target go-client openapi.yaml
  signalbox codegen --target go-server --package orders --out internal openapi.yaml`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCodegen(cmd, args[0], opts)
		},
	}
	f := cmd.Flags()
	f.StringVarP(&opts.target, "target", "t", "", "what to generate: "+targetIDs())
	f.StringVarP(&opts.pkg, "package", "p", "", "the package's name (default: from the document's title)")
	f.StringVarP(&opts.out, "out", "o", ".", "the directory to write the package's directory in")
	f.BoolVar(&opts.failOnWarning, "fail-on-warning", false, "exit with code 1 when the generated code can't express part of the document")
	f.BoolVar(&opts.allowRemoteRefs, "allow-remote-refs", false, "let $ref fetch http and https URLs")
	_ = cmd.MarkFlagRequired("target")
	return cmd
}

func targetIDs() string {
	ids := make([]string, len(codegen.Targets))
	for i, t := range codegen.Targets {
		ids[i] = string(t.ID)
	}
	return strings.Join(ids, " or ")
}

func runCodegen(cmd *cobra.Command, path string, opts codegenOptions) error {
	usage := func(err error) error { return &ExitError{Code: ExitUsage, Err: err} }
	target, err := codegen.ParseTarget(opts.target)
	if err != nil {
		return usage(fmt.Errorf("--target: %w", err))
	}
	data, name, dir, err := readInput(cmd.InOrStdin(), path)
	if err != nil {
		return usage(err)
	}
	result, err := codegen.Generate(specload.Document{Data: data, Name: name, Dir: dir},
		codegen.Options{Target: target, Package: opts.pkg, AllowRemoteReferences: opts.allowRemoteRefs})
	if err != nil {
		return usage(err)
	}

	stderr := cmd.ErrOrStderr()
	for _, w := range result.Warnings {
		fmt.Fprintf(stderr, "%s:%d:%d: warning: %s\n", name, w.Line, w.Column, w.Message)
	}
	for _, f := range result.Files {
		dest := filepath.Join(opts.out, filepath.FromSlash(f.Path))
		// A README is people's to change; only generated code is replaced.
		if !strings.HasSuffix(f.Path, ".gen.go") {
			if _, err := os.Stat(dest); err == nil {
				continue
			} else if !errors.Is(err, fs.ErrNotExist) {
				return usage(err)
			}
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
			return usage(fmt.Errorf("can't create %s: %w", filepath.Dir(dest), err))
		}
		if err := os.WriteFile(dest, f.Content, 0o644); err != nil { //nolint:gosec // G306: generated source, readable like any other.
			return usage(fmt.Errorf("can't write %s: %w", dest, err))
		}
		fmt.Fprintf(cmd.OutOrStdout(), "wrote %s\n", dest)
	}
	if opts.failOnWarning && len(result.Warnings) > 0 {
		return &ExitError{Code: ExitFindings}
	}
	return nil
}
