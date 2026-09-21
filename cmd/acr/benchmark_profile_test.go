// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alibaba/open-code-review/internal/benchmark"
)

func prepareStandardBenchmark(t *testing.T) (string, string) {
	t.Helper()
	repo := initCLITestRepo(t)
	base := strings.TrimSpace(runCLIGit(t, repo, "rev-parse", "HEAD"))
	if err := os.WriteFile(filepath.Join(repo, "app.go"), []byte("package app\nfunc Value() int { return 2 }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	runCLIGit(t, repo, "add", "app.go")
	runCLIGit(t, repo, "commit", "-m", "change")
	head := strings.TrimSpace(runCLIGit(t, repo, "rev-parse", "HEAD"))
	workspace := t.TempDir()
	manifest := filepath.Join(workspace, "manifest.json")
	if err := benchmark.SaveManifest(manifest, benchmark.Manifest{
		ProtocolVersion: benchmark.BenchmarkProtocolVersion,
		Dataset:         benchmark.DatasetMetadata{ID: "fixture", Version: "1"},
		Cases:           []benchmark.Case{{ID: "case-1", Repository: repo, PRURL: "https://github.com/example/project/pull/7", BaseSHA: base, HeadSHA: head}},
	}); err != nil {
		t.Fatal(err)
	}
	var prepared benchmarkPrepareOutput
	output := executeCLI(t, "benchmark", "prepare", "--workspace", workspace, "--dataset", manifest, "--limit", "1", "--repo", repo, "--profile", "standard")
	if err := json.Unmarshal(output, &prepared); err != nil {
		t.Fatal(err)
	}
	if prepared.Tasks != 2 || prepared.SetupFailures != 0 {
		t.Fatalf("prepare: %s", output)
	}
	return workspace, prepared.RunID
}

func completeStandardBenchmark(t *testing.T) (string, string) {
	t.Helper()
	workspace, runID := prepareStandardBenchmark(t)
	var output []byte
	for i := 0; i < 2; i++ {
		var next benchmarkNextOutput
		output = executeCLI(t, "benchmark", "next", "--workspace", workspace, "--run", runID, "--worker", "test")
		if err := json.Unmarshal(output, &next); err != nil {
			t.Fatal(err)
		}
		if next.Task.Arm == benchmark.ArmACR {
			if !strings.Contains(next.Prompt, "standard") {
				t.Fatalf("missing standard instructions: %s", next.Prompt)
			}
			completeCLIStandardNoFindings(t, next.Task)
		}
		submission := benchmark.TaskSubmission{ProtocolVersion: benchmark.BenchmarkProtocolVersion, RunID: runID, TaskID: next.Task.ID,
			Executor: benchmark.Executor{Host: "test", Model: "test", ContextID: next.Task.ID}, Findings: []benchmark.Finding{}}
		data, err := json.Marshal(submission)
		if err != nil {
			t.Fatal(err)
		}
		input := filepath.Join(workspace, next.Task.ID+".json")
		if err := os.WriteFile(input, data, 0600); err != nil {
			t.Fatal(err)
		}
		output = executeCLI(t, "benchmark", "submit", "--workspace", workspace, "--run", runID, "--task", next.Task.ID, "--input", input)
		if i == 1 && !strings.Contains(string(output), "# ACR Benchmark Report") {
			t.Fatalf("benchmark did not finish: %s", output)
		}
	}
	return workspace, runID
}

func TestBenchmarkStandardReviewCompletesWithoutPhases(t *testing.T) {
	workspace, runID := completeStandardBenchmark(t)
	output := executeCLI(t, "benchmark", "report", "--workspace", workspace, "--run", runID)
	if !strings.Contains(string(output), "ACR profile: **standard**") {
		t.Fatalf("missing profile: %s", output)
	}
}

func TestBenchmarkReportsElapsedTime(t *testing.T) {
	start := time.Now()
	workspace, runID := completeStandardBenchmark(t)
	elapsed := time.Since(start).Seconds()
	output := executeCLI(t, "benchmark", "report", "--workspace", workspace, "--run", runID, "--format", "json")
	var report struct {
		ReviewProfile   string `json:"review_profile"`
		BaselineElapsed struct {
			Tasks   int     `json:"tasks"`
			Seconds float64 `json:"seconds"`
		} `json:"baseline_elapsed"`
		ACRElapsed struct {
			Tasks   int     `json:"tasks"`
			Seconds float64 `json:"seconds"`
		} `json:"acr_elapsed"`
	}
	if err := json.Unmarshal(output, &report); err != nil {
		t.Fatal(err)
	}
	if report.ReviewProfile != "standard" || report.BaselineElapsed.Tasks != 1 || report.ACRElapsed.Tasks != 1 || report.BaselineElapsed.Seconds <= 0 || report.ACRElapsed.Seconds <= 0 || report.BaselineElapsed.Seconds+report.ACRElapsed.Seconds > elapsed {
		t.Fatalf("missing or invalid elapsed timing: %s", output)
	}
	rendered := executeCLI(t, "benchmark", "report", "--workspace", workspace, "--run", runID)
	if !strings.Contains(string(rendered), "## Elapsed review time") {
		t.Fatalf("missing elapsed report: %s", rendered)
	}
}

func TestBenchmarkElapsedSurvivesReclaimAndDuplicateSubmission(t *testing.T) {
	workspace, runID := prepareStandardBenchmark(t)
	var first, reclaimed benchmarkNextOutput
	output := executeCLI(t, "benchmark", "next", "--workspace", workspace, "--run", runID, "--worker", "first", "--lease", "1ns")
	if err := json.Unmarshal(output, &first); err != nil {
		t.Fatal(err)
	}
	output = executeCLI(t, "benchmark", "next", "--workspace", workspace, "--run", runID, "--worker", "replacement")
	if err := json.Unmarshal(output, &reclaimed); err != nil {
		t.Fatal(err)
	}
	if reclaimed.Task.ID != first.Task.ID || reclaimed.Task.StartedAt.IsZero() || !reclaimed.Task.StartedAt.Equal(first.Task.StartedAt) || !reclaimed.Task.ClaimedAt.After(first.Task.ClaimedAt) {
		t.Fatalf("reclaim reset elapsed start: first=%+v reclaimed=%+v", first.Task, reclaimed.Task)
	}
	if reclaimed.Task.Arm == benchmark.ArmACR {
		completeCLIStandardNoFindings(t, reclaimed.Task)
	}
	submission := benchmark.TaskSubmission{ProtocolVersion: benchmark.BenchmarkProtocolVersion, RunID: runID, TaskID: reclaimed.Task.ID,
		Executor: benchmark.Executor{Host: "test", Model: "test", ContextID: "baseline"}, Findings: []benchmark.Finding{}}
	data, err := json.Marshal(submission)
	if err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(workspace, "submission.json")
	if err := os.WriteFile(input, data, 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{"benchmark", "submit", "--workspace", workspace, "--run", runID, "--task", reclaimed.Task.ID, "--input", input}
	executeCLI(t, args...)
	before := executeCLI(t, "benchmark", "report", "--workspace", workspace, "--run", runID, "--format", "json")
	executeCLI(t, args...)
	after := executeCLI(t, "benchmark", "report", "--workspace", workspace, "--run", runID, "--format", "json")
	if string(before) != string(after) {
		t.Fatalf("duplicate submission changed report:\n%s\n%s", before, after)
	}
	var report benchmark.Report
	if err := json.Unmarshal(after, &report); err != nil {
		t.Fatal(err)
	}
	if report.BaselineElapsed.Tasks+report.ACRElapsed.Tasks != 1 {
		t.Fatalf("timed unsubmitted task: %s", after)
	}
}

func TestBenchmarkRejectsUnknownProfile(t *testing.T) {
	workspace, _ := prepareStandardBenchmark(t)
	cmd := newRootCommand()
	cmd.SetArgs([]string{"benchmark", "prepare", "--workspace", workspace, "--dataset", filepath.Join(workspace, "manifest.json"), "--limit", "1", "--profile", "typo"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "unsupported profile") {
		t.Fatalf("expected profile error, got %v", err)
	}
}

func completeCLIStandardNoFindings(t *testing.T, task benchmark.Task) {
	t.Helper()
	var draft draftOutput
	output := executeCLI(t, "review", "draft", "--repo", task.CheckoutPath, "--session", task.ReviewSession)
	if err := json.Unmarshal(output, &draft); err != nil {
		t.Fatal(err)
	}
	output = executeCLI(t, "review", "submit", "--repo", task.CheckoutPath, "--session", task.ReviewSession, "--input", draft.FindingsPath, "--render")
	if !strings.Contains(string(output), "0 rejected") {
		t.Fatalf("review rejected: %s", output)
	}
}
