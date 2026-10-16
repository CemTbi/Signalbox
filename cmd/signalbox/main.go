// SPDX-License-Identifier: Apache-2.0

// Command signalbox is the Signalbox command-line tool.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/CemTbi/Signalbox/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	err := cli.NewRootCommand(os.Stdin, os.Stdout, os.Stderr).ExecuteContext(ctx)
	stop()
	if err != nil && err.Error() != "" && cli.ExitCode(err) != cli.ExitFindings {
		fmt.Fprintln(os.Stderr, "signalbox:", err)
	}
	os.Exit(cli.ExitCode(err))
}
