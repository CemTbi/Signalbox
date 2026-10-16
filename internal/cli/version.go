// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/CemTbi/Signalbox/internal/buildinfo"
)

func newVersionCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print the version of signalbox",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			info := buildinfo.Get()
			if asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(info)
			}
			_, err := fmt.Fprintln(cmd.OutOrStdout(), "signalbox", info)
			return err
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the version as JSON")
	return cmd
}
