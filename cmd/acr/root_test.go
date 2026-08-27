// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alibaba/open-code-review/internal/benchmark"
	"github.com/alibaba/open-code-review/internal/delegation"
)

func TestRootIsDelegationOnly(t *testing.T) {
	cmd := newRootCommand()
	if cmd.Use != "acr" {
		t.Fatalf("root Use = %q, want acr", cmd.Use)
	}
	for _, forbidden := range []string{"config", "llm", "scan"} {
		if child, _, err := cmd.Find([]string{forbidden}); err == nil && child != cmd {
			t.Fatalf("provider-dependent command %q must not be exposed", forbidden)
		}
	}
	if child, _, err := cmd.Find([]string{"mcp"}); err != nil || child == cmd {
		t.Fatalf("delegation MCP command is missing: child=%v err=%v", child, err)
	}
}

func TestReviewPrepareSubmitAndRender(t *testing.T) {
	repo := initCLITestRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "app.go"), []byte("package app\nfunc Value() int { return 2 }\n"), 0o644); err != nil {
		t.Fatalf("modify source: %v", err)
	}

	prepareOut := executeCLI(t, "review", "prepare", "--repo", repo)
	var prepared prepareOutput
	if err := json.Unmarshal(prepareOut, &prepared); err != nil {
		t.Fatalf("decode prepare output: %v\n%s", err, prepareOut)
	}
	if prepared.SessionID == "" || prepared.RequestPath == "" || prepared.ModelExecution != "host_agent" {
		t.Fatalf("unexpected prepare output: %#v", prepared)
	}
	if prepared.ReviewProfile != delegation.ReviewProfileDeep {
		t.Fatalf("unexpected review profile: %#v", prepared)
	}

	request, err := delegation.LoadRequest(repo, prepared.SessionID)
	if err != nil {
		t.Fatalf("load request: %v", err)
	}
	draftOut := executeCLI(t, "review", "draft", "--repo", repo, "--session", prepared.SessionID)
	var draft struct {
		FindingsPath    string `json:"findings_path"`
		ReviewQuestions int    `json:"review_questions"`
		NextStep        string `json:"next_step"`
	}
	if err := json.Unmarshal(draftOut, &draft); err != nil {
		t.Fatalf("decode draft output: %v\n%s", err, draftOut)
	}
	if draft.FindingsPath != filepath.Join(request.Repository.Root, ".acr", "sessions", prepared.SessionID, delegation.FindingsFileName) || !strings.Contains(draft.NextStep, "Fill") {
		t.Fatalf("unexpected draft output: %#v", draft)
	}
	findingPath := filepath.Join(t.TempDir(), "findings.json")
	submission := delegation.Submission{
		ProtocolVersion: delegation.ProtocolVersion,
		SessionID:       prepared.SessionID,
		Findings: []delegation.Finding{{
			UnitID: request.Units[0].ID, File: "app.go", StartLine: 2, EndLine: 2,
			Severity: "high", Category: "bug", Explanation: "A test finding.",
			Evidence: "The changed line demonstrates the issue.", Confidence: 0.9,
		}},
	}
	data, err := json.Marshal(submission)
	if err != nil {
		t.Fatalf("encode submission: %v", err)
	}
	if err := os.WriteFile(findingPath, data, 0o600); err != nil {
		t.Fatalf("write submission: %v", err)
	}

	submitOut := executeCLI(t, "review", "submit", "--repo", repo, "--session", prepared.SessionID, "--input", findingPath)
	var result delegation.Result
	if err := json.Unmarshal(submitOut, &result); err != nil {
		t.Fatalf("decode submit output: %v\n%s", err, submitOut)
	}
	if result.Summary.Accepted != 1 {
		t.Fatalf("unexpected submit result: %#v", result)
	}

	completed := string(executeCLI(t, "review", "submit", "--repo", repo, "--session", prepared.SessionID, "--input", findingPath, "--render"))
	if !strings.Contains(completed, "# Agent Code Review") || !strings.Contains(completed, "Copyable fix prompt") || !strings.Contains(completed, "Work on exactly one validated ACR finding") {
		t.Fatalf("submit --render did not return the completed report:\n%s", completed)
	}

	rendered := string(executeCLI(t, "review", "render", "--repo", repo, "--session", prepared.SessionID, "--format", "markdown"))
	if !strings.Contains(rendered, "A test finding.") || !strings.Contains(rendered, "app.go:2") || !strings.Contains(rendered, "Copyable fix prompt") {
		t.Fatalf("unexpected render output:\n%s", rendered)
	}
	withoutFixPrompt := string(executeCLI(t, "review", "render", "--repo", repo, "--session", prepared.SessionID, "--format", "markdown", "--fix-prompt", "none"))
	if strings.Contains(withoutFixPrompt, "Copyable fix prompt") {
		t.Fatalf("--fix-prompt none unexpectedly rendered a fix prompt:\n%s", withoutFixPrompt)
	}

	handoff := string(executeCLI(t, "review", "handoff", "--repo", repo, "--session", prepared.SessionID))
	if !strings.Contains(handoff, "independent reviewer") || !strings.Contains(handoff, prepared.SessionID) {
		t.Fatalf("unexpected handoff output:\n%s", handoff)
	}

	briefingOut := executeCLI(t, "review", "brief", "--repo", repo, "--session", prepared.SessionID)
	var briefing delegation.Briefing
	if err := json.Unmarshal(briefingOut, &briefing); err != nil {
		t.Fatalf("decode briefing: %v\n%s", err, briefingOut)
	}
	if briefing.SessionID != prepared.SessionID || len(briefing.Units) != 1 || len(briefing.Units[0].Files) != 1 {
		t.Fatalf("unexpected briefing: %#v", briefing)
	}
	if len(briefing.Units[0].Files[0].ChangedLineRanges) == 0 {
		t.Fatalf("briefing must retain changed-line anchors: %#v", briefing)
	}
	if strings.Contains(string(briefingOut), "func Value()") {
		t.Fatalf("briefing must not embed source snapshots:\n%s", briefingOut)
	}
}

func TestBareReviewDefaultsToPrepare(t *testing.T) {
	repo := initCLITestRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "app.go"), []byte("package app\nfunc Value() int { return 3 }\n"), 0o644); err != nil {
		t.Fatalf("modify source: %v", err)
	}
	output := executeCLI(t, "review", "--repo", repo)
	var prepared prepareOutput
	if err := json.Unmarshal(output, &prepared); err != nil || prepared.SessionID == "" {
		t.Fatalf("bare review did not prepare a session: err=%v output=%s", err, output)
	}
}

func TestSubmitRenderReturnsRejectedFindingsAsMachineReadableJSON(t *testing.T) {
	repo := initCLITestRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "app.go"), []byte("package app\nfunc Value() int { return 2 }\n"), 0o644); err != nil {
		t.Fatalf("modify source: %v", err)
	}

	prepareOut := executeCLI(t, "review", "prepare", "--repo", repo)
	var prepared prepareOutput
	if err := json.Unmarshal(prepareOut, &prepared); err != nil {
		t.Fatalf("decode prepare output: %v\n%s", err, prepareOut)
	}
	request, err := delegation.LoadRequest(repo, prepared.SessionID)
	if err != nil {
		t.Fatalf("load request: %v", err)
	}

	findingPath := filepath.Join(t.TempDir(), "findings.json")
	data, err := json.Marshal(delegation.Submission{
		ProtocolVersion: delegation.ProtocolVersion,
		SessionID:       prepared.SessionID,
		Findings: []delegation.Finding{{
			UnitID: request.Units[0].ID, File: "app.go", StartLine: 999, EndLine: 999,
			Severity: "high", Category: "bug", Explanation: "Invalid line anchor.",
			Evidence: "This must be rejected.", Confidence: 0.9,
		}},
	})
	if err != nil {
		t.Fatalf("encode submission: %v", err)
	}
	if err := os.WriteFile(findingPath, data, 0o600); err != nil {
		t.Fatalf("write submission: %v", err)
	}

	output := executeCLI(t, "review", "submit", "--repo", repo, "--session", prepared.SessionID, "--input", findingPath, "--render")
	var result delegation.Result
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatalf("rejected submit --render must return JSON: %v\n%s", err, output)
	}
	if len(result.Rejected) == 0 || strings.Contains(string(output), "# Agent Code Review") {
		t.Fatalf("unexpected rejected submit output: %#v\n%s", result, output)
	}
}

func TestSubmitResolvesGitRootFromSubdirectory(t *testing.T) {
	repo := initCLITestRepo(t)
	subdir := filepath.Join(repo, "nested")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "app.go"), []byte("package app\nfunc Value() int { return 4 }\n"), 0o644); err != nil {
		t.Fatalf("modify source: %v", err)
	}
	prepareOut := executeCLI(t, "review", "prepare", "--repo", subdir)
	var prepared prepareOutput
	if err := json.Unmarshal(prepareOut, &prepared); err != nil {
		t.Fatalf("decode prepare: %v", err)
	}
	request, err := delegation.LoadRequest(repo, prepared.SessionID)
	if err != nil {
		t.Fatalf("load request: %v", err)
	}
	submission := delegation.Submission{
		ProtocolVersion: delegation.ProtocolVersion,
		SessionID:       prepared.SessionID,
		Findings: []delegation.Finding{{
			UnitID: request.Units[0].ID, File: "app.go", StartLine: 2, EndLine: 2,
			Severity: "high", Category: "bug", Explanation: "Subdirectory finding.",
			Evidence: "The changed line is reachable.", Confidence: 0.9,
		}},
	}
	data, err := json.Marshal(submission)
	if err != nil {
		t.Fatalf("marshal submission: %v", err)
	}
	path := filepath.Join(t.TempDir(), "findings.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write submission: %v", err)
	}
	executeCLI(t, "review", "submit", "--repo", subdir, "--session", prepared.SessionID, "--input", path)
}

func TestDoctorReportsNoAPIKeyRequired(t *testing.T) {
	repo := initCLITestRepo(t)
	output := executeCLI(t, "doctor", "--repo", repo, "--format", "json")
	var report doctorReport
	if err := json.Unmarshal(output, &report); err != nil {
		t.Fatalf("decode doctor output: %v\n%s", err, output)
	}
	if !report.NoAPIKeyRequired || !report.Git.Available || !report.Repository.Available || !report.ProtocolRoundTrip.Available {
		t.Fatalf("unexpected doctor report: %#v", report)
	}
}

func TestBenchmarkScoreEvaluatesQodoCase(t *testing.T) {
	dir := t.TempDir()
	dataset := filepath.Join(dir, "dataset.jsonl")
	if err := os.WriteFile(dataset, []byte("{\"repo\":\"example\",\"pr_url_to_review\":\"https://example.test/pr/1\",\"issues\":[{\"title\":\"bad branch\",\"description\":\"logic inverted\",\"file_path\":\"src/a.go\",\"start_line\":4,\"end_line\":4}]}\n"), 0o600); err != nil {
		t.Fatalf("write dataset: %v", err)
	}
	findings := filepath.Join(dir, "findings.json")
	if err := os.WriteFile(findings, []byte("{\"findings\":[{\"file\":\"src/a.go\",\"start_line\":4,\"end_line\":4,\"explanation\":\"branch is inverted\"}]}"), 0o600); err != nil {
		t.Fatalf("write findings: %v", err)
	}

	output := executeCLI(t, "benchmark", "score", "--dataset", dataset, "--pr", "https://example.test/pr/1", "--findings", findings)
	var score benchmark.Score
	if err := json.Unmarshal(output, &score); err != nil {
		t.Fatalf("decode score: %v\n%s", err, output)
	}
	if score.Matched != 1 || score.Precision != 1 || score.Recall != 1 {
		t.Fatalf("unexpected score: %#v", score)
	}
}

func TestBenchmarkPrepareNextSubmitAndReport(t *testing.T) {
	repo := initCLITestRepo(t)
	baseSHA := strings.TrimSpace(runCLIGit(t, repo, "rev-parse", "HEAD"))
	if err := os.WriteFile(filepath.Join(repo, "app.go"), []byte("package app\nfunc Value() int { return 2 }\n"), 0o644); err != nil {
		t.Fatalf("modify source: %v", err)
	}
	runCLIGit(t, repo, "add", "app.go")
	runCLIGit(t, repo, "commit", "-m", "change")
	headSHA := strings.TrimSpace(runCLIGit(t, repo, "rev-parse", "HEAD"))
	workspace := t.TempDir()
	prURL := "https://github.com/example/project/pull/7"
	manifestPath := filepath.Join(workspace, "manifest.json")
	if err := benchmark.SaveManifest(manifestPath, benchmark.Manifest{
		ProtocolVersion: benchmark.BenchmarkProtocolVersion,
		Dataset:         benchmark.DatasetMetadata{ID: "fixture", Version: "1"},
		Cases: []benchmark.Case{{
			ID: "case-1", Repository: repo, PRURL: prURL, BaseSHA: baseSHA, HeadSHA: headSHA,
			Expected: []benchmark.Finding{{Title: "Wrong return value", Description: "Value returns two instead of one.", File: "app.go", StartLine: 2, EndLine: 2}},
		}},
	}); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}

	prepareOutput := executeCLI(t, "benchmark", "prepare", "--workspace", workspace, "--dataset", manifestPath, "--pr", prURL, "--repo", repo)
	var prepared benchmarkPrepareOutput
	if err := json.Unmarshal(prepareOutput, &prepared); err != nil {
		t.Fatalf("decode benchmark prepare: %v\n%s", err, prepareOutput)
	}
	if prepared.RunID == "" || prepared.Tasks != 2 || !strings.Contains(prepared.NextStep, "benchmark next") {
		t.Fatalf("unexpected benchmark prepare output: %#v", prepared)
	}

	for index := 0; index < 2; index++ {
		nextOutput := executeCLI(t, "benchmark", "next", "--workspace", workspace, "--run", prepared.RunID, "--worker", "test-worker")
		var claimed benchmarkNextOutput
		if err := json.Unmarshal(nextOutput, &claimed); err != nil {
			t.Fatalf("decode benchmark next: %v\n%s", err, nextOutput)
		}
		if claimed.Task.ID == "" || claimed.Prompt == "" || claimed.Task.State != benchmark.TaskClaimed {
			t.Fatalf("unexpected claimed task: %#v", claimed)
		}
		submission := benchmark.TaskSubmission{
			ProtocolVersion: benchmark.BenchmarkProtocolVersion, RunID: prepared.RunID, TaskID: claimed.Task.ID,
			Executor: benchmark.Executor{Host: "codex", Model: "sol", ContextID: "cli-context-" + claimed.Task.Arm},
			Findings: []benchmark.Finding{{Title: "Wrong return value", Explanation: "Value returns two instead of one.", File: "app.go", StartLine: 2, EndLine: 2}},
		}
		input := filepath.Join(workspace, claimed.Task.ID+".json")
		data, err := json.Marshal(submission)
		if err != nil {
			t.Fatalf("marshal task submission: %v", err)
		}
		if err := os.WriteFile(input, data, 0o600); err != nil {
			t.Fatalf("write task submission: %v", err)
		}
		output := executeCLI(t, "benchmark", "submit", "--workspace", workspace, "--run", prepared.RunID, "--task", claimed.Task.ID, "--input", input)
		if index == 1 && !strings.Contains(string(output), "# ACR Benchmark Report") {
			t.Fatalf("final submission did not return the report:\n%s", output)
		}
	}

	statusOutput := executeCLI(t, "benchmark", "status", "--workspace", workspace, "--run", prepared.RunID)
	if !strings.Contains(string(statusOutput), `"scored": 2`) {
		t.Fatalf("unexpected benchmark status: %s", statusOutput)
	}
	rendered := executeCLI(t, "benchmark", "report", "--workspace", workspace, "--run", prepared.RunID)
	if !strings.Contains(string(rendered), "**Result: Tie**") {
		t.Fatalf("unexpected benchmark report:\n%s", rendered)
	}
}

func TestBenchmarkPrepareRejectsUnboundedRuns(t *testing.T) {
	workspace := t.TempDir()
	manifestPath := filepath.Join(workspace, "manifest.json")
	if err := benchmark.SaveManifest(manifestPath, benchmark.Manifest{
		ProtocolVersion: benchmark.BenchmarkProtocolVersion,
		Dataset:         benchmark.DatasetMetadata{ID: "fixture", Version: "1"},
		Cases:           []benchmark.Case{{ID: "case-1", Repository: "repo", PRURL: "https://github.com/example/project/pull/1"}},
	}); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}
	cmd := newRootCommand()
	cmd.SetArgs([]string{"benchmark", "prepare", "--workspace", workspace, "--dataset", manifestPath})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "--pr, --limit, or --all") {
		t.Fatalf("unbounded prepare error = %v", err)
	}
}

func executeCLI(t *testing.T, args ...string) []byte {
	t.Helper()
	cmd := newRootCommand()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("acr %s: %v\nstderr: %s", strings.Join(args, " "), err, stderr.String())
	}
	return stdout.Bytes()
}

func initCLITestRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	for _, args := range [][]string{{"init"}, {"config", "user.email", "acr@example.test"}, {"config", "user.name", "ACR Test"}} {
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "app.go"), []byte("package app\nfunc Value() int { return 1 }\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	for _, args := range [][]string{{"add", "app.go"}, {"commit", "-m", "initial"}} {
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	return repo
}

func runCLIGit(t *testing.T, repo string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", repo}, arguments...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(arguments, " "), err, output)
	}
	return string(output)
}
