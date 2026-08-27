// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package main

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

func newRootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:           "acr",
		Short:         "Agent-delegated code review",
		Long:          "Prepare deterministic code reviews for the active coding agent. No model API key is required.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newReviewCommand())
	root.AddCommand(newSessionCommand())
	root.AddCommand(newDoctorCommand())
	root.AddCommand(newMCPCommand())
	root.AddCommand(newBenchmarkCommand())
	root.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			fmt.Fprintf(cmd.OutOrStdout(), "acr %s", version)
			if gitCommit != "" {
				fmt.Fprintf(cmd.OutOrStdout(), " (%s)", gitCommit)
			}
			fmt.Fprintf(cmd.OutOrStdout(), " %s/%s\n", runtime.GOOS, runtime.GOARCH)
			if buildDate != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "built at: %s\n", buildDate)
			}
		},
	})
	return root
}
