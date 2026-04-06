package cmd

import (
	"flag"

	"github.com/spf13/cobra"
)

func helpOnUsageError(cmd *cobra.Command) error {
	if err := cmd.Help(); err != nil {
		return err
	}
	return flag.ErrHelp
}

func requireExactArgs(expected int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) == expected {
			return nil
		}
		return helpOnUsageError(cmd)
	}
}
