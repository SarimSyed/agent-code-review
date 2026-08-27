// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package main

import (
	"fmt"

	"github.com/alibaba/open-code-review/internal/benchmark"
	"github.com/spf13/cobra"
)

func newBenchmarkCommand() *cobra.Command {
	benchmarkCommand := &cobra.Command{
		Use:   "benchmark",
		Short: "Score review findings against anchored benchmark ground truth",
		Args:  cobra.NoArgs,
	}
	benchmarkCommand.AddCommand(newBenchmarkScoreCommand())
	return benchmarkCommand
}

func newBenchmarkScoreCommand() *cobra.Command {
	var dataset, prURL, findings string
	cmd := &cobra.Command{
		Use:   "score",
		Short: "Score findings against one Qodo PR-Review-Bench case",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if dataset == "" || prURL == "" || findings == "" {
				return fmt.Errorf("--dataset, --pr, and --findings are required")
			}
			benchmarkCase, err := benchmark.LoadQodoCase(dataset, prURL)
			if err != nil {
				return err
			}
			actual, err := benchmark.LoadFindings(findings)
			if err != nil {
				return err
			}
			return writeCommandJSON(cmd, benchmark.ScoreFindings(benchmarkCase.Expected, actual))
		},
	}
	cmd.Flags().StringVar(&dataset, "dataset", "", "path to Qodo PR-Review-Bench JSONL metadata")
	cmd.Flags().StringVar(&prURL, "pr", "", "PR URL to score")
	cmd.Flags().StringVar(&findings, "findings", "", "path to ACR or agent findings JSON")
	return cmd
}
