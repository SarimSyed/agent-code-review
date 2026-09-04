// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/alibaba/open-code-review/internal/benchmark"
	"github.com/alibaba/open-code-review/internal/delegation"
	"github.com/spf13/cobra"
)

type benchmarkCLIOptions struct {
	workspace string
}

type benchmarkPrepareOutput struct {
	ProtocolVersion string `json:"protocol_version"`
	RunID           string `json:"run_id"`
	Tasks           int    `json:"tasks"`
	SetupFailures   int    `json:"setup_failures"`
	RunPath         string `json:"run_path"`
	NextStep        string `json:"next_step"`
}

type benchmarkNextOutput struct {
	ProtocolVersion string         `json:"protocol_version"`
	RunID           string         `json:"run_id"`
	Task            benchmark.Task `json:"task"`
	Prompt          string         `json:"prompt"`
}

type benchmarkStatusOutput struct {
	ProtocolVersion string `json:"protocol_version"`
	RunID           string `json:"run_id"`
	Queued          int    `json:"queued"`
	Claimed         int    `json:"claimed"`
	Submitted       int    `json:"submitted"`
	NeedsJudgment   int    `json:"needs_adjudication"`
	Scored          int    `json:"scored"`
	Failed          int    `json:"failed"`
	SetupFailures   int    `json:"setup_failures"`
	Evaluations     int    `json:"evaluations"`
	ReportReady     bool   `json:"report_ready"`
}

func newBenchmarkCommand() *cobra.Command {
	options := &benchmarkCLIOptions{}
	command := &cobra.Command{Use: "benchmark", Short: "Run provider-free paired code-review experiments", Args: cobra.NoArgs}
	command.PersistentFlags().StringVar(&options.workspace, "workspace", "", "directory containing .acr benchmark state (default: current directory)")
	command.AddCommand(newBenchmarkDatasetCommand())
	command.AddCommand(newBenchmarkPrepareCommand(options))
	command.AddCommand(newBenchmarkNextCommand(options))
	command.AddCommand(newBenchmarkSubmitCommand(options))
	command.AddCommand(newBenchmarkStatusCommand(options))
	command.AddCommand(newBenchmarkReportCommand(options))
	command.AddCommand(newBenchmarkScoreCommand())
	return command
}

func newBenchmarkDatasetCommand() *cobra.Command {
	dataset := &cobra.Command{Use: "dataset", Short: "Manage benchmark datasets", Args: cobra.NoArgs}
	fetch := &cobra.Command{Use: "fetch", Short: "Fetch a pinned public benchmark corpus", Args: cobra.NoArgs}
	var output string
	qodo := &cobra.Command{
		Use:   "qodo",
		Short: "Fetch the pinned Qodo PR-Review-Bench corpus",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if output == "" {
				output = "qodo-pr-review-bench.json"
			}
			manifest, err := benchmark.FetchQodo(cmd.Context(), output)
			if err != nil {
				return err
			}
			absolute, err := filepath.Abs(output)
			if err != nil {
				return err
			}
			return writeCommandJSON(cmd, map[string]any{
				"protocol_version": manifest.ProtocolVersion,
				"dataset":          manifest.Dataset,
				"cases":            len(manifest.Cases),
				"manifest_path":    absolute,
				"next_step":        "Run acr benchmark prepare with --dataset and a bounded selector.",
			})
		},
	}
	qodo.Flags().StringVar(&output, "output", "", "canonical manifest output path")
	fetch.AddCommand(qodo)
	dataset.AddCommand(fetch)
	return dataset
}

func newBenchmarkPrepareCommand(options *benchmarkCLIOptions) *cobra.Command {
	var datasetPath, prURL, repository, checkoutMap, cacheDirectory, acrProfile string
	var cavemanLevel string
	var limit, trials int
	var seed int64
	var allCases, caveman bool
	command := &cobra.Command{
		Use:   "prepare",
		Short: "Prepare paired baseline and ACR benchmark tasks",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if cavemanLevel != "" && !caveman {
				return fmt.Errorf("--caveman-level requires --caveman")
			}
			tokenEconomy := delegation.TokenEconomy{Mode: delegation.TokenEconomyNormal}
			if caveman {
				tokenEconomy = delegation.TokenEconomy{Mode: delegation.TokenEconomyCaveman, Level: cavemanLevel}
			}
			selectors := 0
			if prURL != "" {
				selectors++
			}
			if limit > 0 {
				selectors++
			}
			if allCases {
				selectors++
			}
			if selectors != 1 {
				return fmt.Errorf("exactly one of --pr, --limit, or --all is required")
			}
			workspace, err := benchmarkWorkspace(options.workspace)
			if err != nil {
				return err
			}
			overrides, err := readCheckoutMap(checkoutMap)
			if err != nil {
				return err
			}
			run, err := benchmark.PrepareRun(cmd.Context(), workspace, benchmark.PrepareRunOptions{
				DatasetPath: datasetPath, PRURL: prURL, Limit: limit, All: allCases,
				Trials: trials, Seed: seed, Repository: repository,
				RepositoryOverrides: overrides, CacheDir: cacheDirectory, TokenEconomy: tokenEconomy,
				ACRProfile: acrProfile,
			})
			if err != nil {
				return err
			}
			return writeCommandJSON(cmd, benchmarkPrepareOutput{
				ProtocolVersion: benchmark.BenchmarkProtocolVersion, RunID: run.ID,
				Tasks: len(run.Tasks), SetupFailures: len(run.SetupFailures),
				RunPath:  benchmark.RunDir(workspace, run.ID),
				NextStep: "Run acr benchmark next --run " + run.ID + " --worker <fresh-worker>, execute the returned prompt in a fresh active-model context, and submit its JSON result.",
			})
		},
	}
	command.Flags().StringVar(&datasetPath, "dataset", "", "canonical benchmark manifest")
	command.Flags().StringVar(&prURL, "pr", "", "select one PR URL")
	command.Flags().IntVar(&limit, "limit", 0, "select a deterministic bounded sample")
	command.Flags().BoolVar(&allCases, "all", false, "explicitly select every case")
	command.Flags().IntVar(&trials, "trials", 1, "paired trials per selected case")
	command.Flags().Int64Var(&seed, "seed", 1, "deterministic selection and ordering seed")
	command.Flags().StringVar(&repository, "repo", "", "existing checkout override for a one-case run")
	command.Flags().StringVar(&checkoutMap, "checkout-map", "", "JSON object mapping case IDs to existing checkouts")
	command.Flags().StringVar(&cacheDirectory, "cache-dir", "", "managed Git mirror cache")
	command.Flags().StringVar(&acrProfile, "acr-profile", delegation.ReviewProfileDeep, "ACR arm profile: deep or adaptive")
	command.Flags().BoolVar(&caveman, "caveman", false, "use token-economical agent communication for both arms")
	command.Flags().StringVar(&cavemanLevel, "caveman-level", "", "Caveman intensity: lite, full, or ultra (default: full)")
	_ = command.MarkFlagRequired("dataset")
	return command
}

func newBenchmarkNextCommand(options *benchmarkCLIOptions) *cobra.Command {
	var runID, worker, format string
	var lease time.Duration
	command := &cobra.Command{
		Use:   "next",
		Short: "Atomically claim the next benchmark task",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if runID == "" || worker == "" {
				return fmt.Errorf("--run and --worker are required")
			}
			workspace, err := benchmarkWorkspace(options.workspace)
			if err != nil {
				return err
			}
			task, err := benchmark.ClaimNextTask(workspace, runID, worker, time.Now().UTC(), lease)
			if err != nil {
				return err
			}
			prompt, err := os.ReadFile(task.PromptPath)
			if err != nil {
				return fmt.Errorf("read benchmark task prompt: %w", err)
			}
			switch strings.ToLower(format) {
			case "json":
				return writeCommandJSON(cmd, benchmarkNextOutput{ProtocolVersion: benchmark.BenchmarkProtocolVersion, RunID: runID, Task: *task, Prompt: string(prompt)})
			case "prompt":
				_, err := fmt.Fprintf(cmd.OutOrStdout(), "Benchmark run: %s\nBenchmark task: %s\n\n%s", runID, task.ID, prompt)
				return err
			default:
				return fmt.Errorf("unsupported format %q; use json or prompt", format)
			}
		},
	}
	command.Flags().StringVar(&runID, "run", "", "benchmark run id")
	command.Flags().StringVar(&worker, "worker", "", "worker or host context identifier")
	command.Flags().StringVar(&format, "format", "json", "output format: json or prompt")
	command.Flags().DurationVar(&lease, "lease", time.Hour, "claim lease before an interrupted task can resume")
	return command
}

func newBenchmarkSubmitCommand(options *benchmarkCLIOptions) *cobra.Command {
	var runID, taskID, input string
	command := &cobra.Command{
		Use:   "submit",
		Short: "Submit reviewer or judge output to a benchmark task",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if runID == "" || taskID == "" || input == "" {
				return fmt.Errorf("--run, --task, and --input are required")
			}
			workspace, err := benchmarkWorkspace(options.workspace)
			if err != nil {
				return err
			}
			submission, err := readBenchmarkSubmission(input)
			if err != nil {
				return err
			}
			task, err := benchmark.SubmitTask(workspace, runID, taskID, submission)
			if err != nil {
				var repair *benchmark.RepairError
				if errors.As(err, &repair) {
					return writeCommandJSON(cmd, repair)
				}
				return err
			}
			reportPath := filepath.Join(benchmark.RunDir(workspace, runID), "report.md")
			if report, readErr := os.ReadFile(reportPath); readErr == nil {
				_, err = cmd.OutOrStdout().Write(report)
				return err
			}
			return writeCommandJSON(cmd, map[string]any{
				"protocol_version": benchmark.BenchmarkProtocolVersion,
				"run_id":           runID, "task": task,
				"next_step": "Run acr benchmark next with a fresh context; the final submission returns the Markdown report automatically.",
			})
		},
	}
	command.Flags().StringVar(&runID, "run", "", "benchmark run id")
	command.Flags().StringVar(&taskID, "task", "", "claimed task id")
	command.Flags().StringVar(&input, "input", "", "reviewer or judge submission JSON")
	return command
}

func newBenchmarkStatusCommand(options *benchmarkCLIOptions) *cobra.Command {
	var runID string
	command := &cobra.Command{
		Use:   "status",
		Short: "Show benchmark queue and report status",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if runID == "" {
				return fmt.Errorf("--run is required")
			}
			workspace, err := benchmarkWorkspace(options.workspace)
			if err != nil {
				return err
			}
			run, err := benchmark.LoadRun(workspace, runID)
			if err != nil {
				return err
			}
			status := benchmarkStatusOutput{ProtocolVersion: benchmark.BenchmarkProtocolVersion, RunID: runID, SetupFailures: len(run.SetupFailures), Evaluations: len(run.Evaluations)}
			for _, task := range run.Tasks {
				switch task.State {
				case benchmark.TaskQueued:
					status.Queued++
				case benchmark.TaskClaimed:
					status.Claimed++
				case benchmark.TaskSubmitted:
					status.Submitted++
				case benchmark.TaskNeedsAdjudication:
					status.NeedsJudgment++
				case benchmark.TaskScored:
					status.Scored++
				case benchmark.TaskFailed:
					status.Failed++
				}
			}
			_, statErr := os.Stat(filepath.Join(benchmark.RunDir(workspace, runID), "report.json"))
			status.ReportReady = statErr == nil
			return writeCommandJSON(cmd, status)
		},
	}
	command.Flags().StringVar(&runID, "run", "", "benchmark run id")
	return command
}

func newBenchmarkReportCommand(options *benchmarkCLIOptions) *cobra.Command {
	var runID, format string
	command := &cobra.Command{
		Use:   "report",
		Short: "Render a benchmark comparison report",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if runID == "" {
				return fmt.Errorf("--run is required")
			}
			workspace, err := benchmarkWorkspace(options.workspace)
			if err != nil {
				return err
			}
			run, err := benchmark.LoadRun(workspace, runID)
			if err != nil {
				return err
			}
			report := benchmark.BuildReport(run)
			switch strings.ToLower(format) {
			case "markdown", "md":
				_, err := io.WriteString(cmd.OutOrStdout(), benchmark.RenderReportMarkdown(report))
				return err
			case "json":
				return writeCommandJSON(cmd, report)
			default:
				return fmt.Errorf("unsupported format %q; use markdown or json", format)
			}
		},
	}
	command.Flags().StringVar(&runID, "run", "", "benchmark run id")
	command.Flags().StringVar(&format, "format", "markdown", "report format: markdown or json")
	return command
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

func benchmarkWorkspace(value string) (string, error) {
	if value == "" {
		value = "."
	}
	root, err := filepath.Abs(value)
	if err != nil {
		return "", fmt.Errorf("resolve benchmark workspace: %w", err)
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("benchmark workspace is not a readable directory: %s", root)
	}
	return root, nil
}

func readCheckoutMap(path string) (map[string]string, error) {
	if path == "" {
		return map[string]string{}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read checkout map: %w", err)
	}
	result := map[string]string{}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	if err := decoder.Decode(&result); err != nil {
		return nil, fmt.Errorf("decode checkout map: %w", err)
	}
	for caseID, repository := range result {
		absolute, err := filepath.Abs(repository)
		if err != nil {
			return nil, fmt.Errorf("resolve checkout for %s: %w", caseID, err)
		}
		result[caseID] = absolute
	}
	return result, nil
}

func readBenchmarkSubmission(path string) (benchmark.TaskSubmission, error) {
	file, err := os.Open(path)
	if err != nil {
		return benchmark.TaskSubmission{}, fmt.Errorf("open benchmark submission: %w", err)
	}
	defer file.Close()
	var submission benchmark.TaskSubmission
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&submission); err != nil {
		return benchmark.TaskSubmission{}, fmt.Errorf("decode benchmark submission: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return benchmark.TaskSubmission{}, fmt.Errorf("benchmark submission must contain one JSON object")
	}
	return submission, nil
}
