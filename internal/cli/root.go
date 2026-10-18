// SPDX-License-Identifier: Apache-2.0

// Package cli implements the signalbox command-line tool.
package cli

import (
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

// Exit codes. CI systems rely on these, so they are part of the CLI's contract.
const (
	ExitOK       = 0 // nothing at or above the --fail-on severity
	ExitFindings = 1 // the document has findings at or above --fail-on
	ExitUsage    = 2 // bad flags, unreadable input or an internal failure
)

// ExitError carries the exit code a command wants the process to end with.
type ExitError struct {
	Code int
	Err  error
}

func (e *ExitError) Error() string {
	if e.Err == nil {
		return fmt.Sprintf("exit status %d", e.Code)
	}
	return e.Err.Error()
}

func (e *ExitError) Unwrap() error { return e.Err }

// ExitCode maps an error returned by Execute to a process exit code.
func ExitCode(err error) int {
	if err == nil {
		return ExitOK
	}
	var ee *ExitError
	if errors.As(err, &ee) {
		return ee.Code
	}
	return ExitUsage
}

// NewRootCommand builds the signalbox command tree. Output goes to the given
// writers so tests can capture it.
func NewRootCommand(stdin io.Reader, stdout, stderr io.Writer) *cobra.Command {
	root := &cobra.Command{
		Use:           "signalbox",
		Short:         "Design, validate and test HTTP APIs from your terminal",
		Long:          "signalbox is the command-line companion to the Signalbox platform.\nIt validates, lints and compares OpenAPI documents and generates code\nfrom them, locally and in CI.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetIn(stdin)
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.AddCommand(newLintCommand(), newDiffCommand(), newCodegenCommand(), newVersionCommand())
	return root
}
