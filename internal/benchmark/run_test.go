// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package benchmark

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPrepareRunCreatesDeterministicIsolatedPairedTasks(t *testing.T) {
	repo, baseSHA, headSHA := benchmarkGitRepository(t)
	workspace := t.TempDir()
	manifest := Manifest{
		ProtocolVersion: BenchmarkProtocolVersion,
		Dataset:         DatasetMetadata{ID: "fixture", Version: "1"},
		Cases: []Case{{
			ID: "case-1", Repository: repo, PRURL: "https://github.com/example/project/pull/1",
			BaseSHA: baseSHA, HeadSHA: headSHA,
			Expected: []Finding{{Title: "secret ground truth", File: "review.go", StartLine: 2, EndLine: 2}},
		}},
	}
	manifestPath := filepath.Join(workspace, "manifest.json")
	if err := SaveManifest(manifestPath, manifest); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}

	run, err := PrepareRun(context.Background(), workspace, PrepareRunOptions{
		DatasetPath: manifestPath, PRURL: manifest.Cases[0].PRURL, Trials: 1, Seed: 17,
		RepositoryOverrides: map[string]string{"case-1": repo},
	})
	if err != nil {
		t.Fatalf("PrepareRun: %v", err)
	}
	if len(run.Tasks) != 2 || run.Tasks[0].Arm == run.Tasks[1].Arm {
		t.Fatalf("expected paired tasks: %#v", run.Tasks)
	}
	if run.Tasks[0].CheckoutPath == run.Tasks[1].CheckoutPath {
		t.Fatalf("paired tasks share a checkout: %#v", run.Tasks)
	}
	for _, task := range run.Tasks {
		prompt, err := os.ReadFile(task.PromptPath)
		if err != nil {
			t.Fatalf("read prompt: %v", err)
		}
		if strings.Contains(string(prompt), "secret ground truth") {
			t.Fatalf("task %s leaks ground truth", task.ID)
		}
		if task.BaseSHA != baseSHA || task.HeadSHA != headSHA {
			t.Fatalf("task refs not pinned: %#v", task)
		}
		if task.SourceTreeSHA == "" {
			t.Fatalf("task source tree was not fingerprinted: %#v", task)
		}
	}

	reloaded, err := LoadRun(workspace, run.ID)
	if err != nil || len(reloaded.Tasks) != 2 {
		t.Fatalf("LoadRun() = %#v, %v", reloaded, err)
	}
}

func TestSubmitTaskRecordsMachineReadableRepairForInvalidFinding(t *testing.T) {
	workspace, run := benchmarkPreparedRun(t)
	task := taskByArm(t, run, ArmBaseline)
	_, err := SubmitTask(workspace, run.ID, task.ID, TaskSubmission{
		ProtocolVersion: BenchmarkProtocolVersion, RunID: run.ID, TaskID: task.ID,
		Executor: Executor{Host: "codex", Model: "sol", ContextID: "context-invalid"},
		Findings: []Finding{{Title: "Invalid", Explanation: "Outside checkout", File: "../secret.go", StartLine: 1, EndLine: 1}},
	})
	var repair *RepairError
	if !errors.As(err, &repair) || repair.Code != "unsafe_finding_path" || repair.TaskID != task.ID {
		t.Fatalf("invalid finding error = %#v, %v", repair, err)
	}
	loaded, loadErr := LoadRun(workspace, run.ID)
	if loadErr != nil {
		t.Fatalf("LoadRun: %v", loadErr)
	}
	stored := loaded.Tasks[findTask(loaded.Tasks, task.ID)]
	if len(stored.Rejections) != 1 || stored.Rejections[0].Code != repair.Code {
		t.Fatalf("repair was not persisted: %#v", stored.Rejections)
	}
}

func TestPrepareRunRequiresBoundedSelectionAndUsesDeterministicLimit(t *testing.T) {
	workspace := t.TempDir()
	manifest := Manifest{ProtocolVersion: BenchmarkProtocolVersion, Dataset: DatasetMetadata{ID: "fixture", Version: "1"}}
	for i := 0; i < 4; i++ {
		manifest.Cases = append(manifest.Cases, Case{ID: fmt.Sprintf("case-%d", i), Repository: "unused", PRURL: fmt.Sprintf("https://github.com/example/project/pull/%d", i+1), BaseSHA: "base", HeadSHA: "head"})
	}
	path := filepath.Join(workspace, "manifest.json")
	if err := SaveManifest(path, manifest); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}
	_, err := PrepareRun(context.Background(), workspace, PrepareRunOptions{DatasetPath: path})
	if err == nil || !strings.Contains(err.Error(), "select cases") {
		t.Fatalf("unbounded PrepareRun error = %v", err)
	}

	first := SelectCases(manifest.Cases, Selection{Limit: 2, Seed: 9})
	second := SelectCases(manifest.Cases, Selection{Limit: 2, Seed: 9})
	if len(first) != 2 || first[0].ID != second[0].ID || first[1].ID != second[1].ID {
		t.Fatalf("selection is not deterministic: %#v vs %#v", first, second)
	}
}

func TestPrepareRunDoesNotExposeHalfPreparedPairs(t *testing.T) {
	repo, baseSHA, _ := benchmarkGitRepository(t)
	workspace := t.TempDir()
	manifest := Manifest{
		ProtocolVersion: BenchmarkProtocolVersion,
		Dataset:         DatasetMetadata{ID: "fixture", Version: "1"},
		Cases: []Case{{
			ID: "case-1", Repository: repo, PRURL: "https://github.com/example/project/pull/1",
			BaseSHA: baseSHA, HeadSHA: baseSHA,
		}},
	}
	path := filepath.Join(workspace, "manifest.json")
	if err := SaveManifest(path, manifest); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}
	run, err := PrepareRun(context.Background(), workspace, PrepareRunOptions{
		DatasetPath: path, PRURL: manifest.Cases[0].PRURL,
		RepositoryOverrides: map[string]string{"case-1": repo},
	})
	if err != nil {
		t.Fatalf("PrepareRun: %v", err)
	}
	if len(run.Tasks) != 0 || len(run.SetupFailures) == 0 {
		t.Fatalf("half-prepared pair became claimable: tasks=%#v failures=%#v", run.Tasks, run.SetupFailures)
	}
}

func TestClaimTaskIsAtomicAndExpiredClaimsResume(t *testing.T) {
	workspace, run := benchmarkPreparedRun(t)
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	var claimed [2]*Task
	var errors [2]error
	var wait sync.WaitGroup
	wait.Add(2)
	for index := range claimed {
		go func(index int) {
			defer wait.Done()
			claimed[index], errors[index] = ClaimNextTask(workspace, run.ID, fmt.Sprintf("worker-%d", index), now, time.Hour)
		}(index)
	}
	wait.Wait()
	if errors[0] != nil || errors[1] != nil || claimed[0].ID == claimed[1].ID {
		t.Fatalf("concurrent claims = %#v, errors = %v", claimed, errors)
	}

	loaded, err := LoadRun(workspace, run.ID)
	if err != nil {
		t.Fatalf("LoadRun: %v", err)
	}
	for index := range loaded.Tasks {
		loaded.Tasks[index].State = TaskClaimed
		loaded.Tasks[index].Worker = "abandoned"
		loaded.Tasks[index].LeaseExpiresAt = now.Add(-time.Second)
	}
	if err := SaveRun(workspace, loaded); err != nil {
		t.Fatalf("SaveRun: %v", err)
	}
	resumed, err := ClaimNextTask(workspace, run.ID, "resumer", now, time.Hour)
	if err != nil || resumed.Worker != "resumer" {
		t.Fatalf("expired claim was not resumed: %#v, %v", resumed, err)
	}
}

func TestSubmitTaskIsIdempotentAndEnforcesPairedIsolation(t *testing.T) {
	workspace, run := benchmarkPreparedRun(t)
	baseline := taskByArm(t, run, ArmBaseline)
	acr := taskByArm(t, run, ArmACR)
	baselineSubmission := TaskSubmission{
		ProtocolVersion: BenchmarkProtocolVersion, RunID: run.ID, TaskID: baseline.ID,
		Executor: Executor{Host: "codex", Model: "sol", ContextID: "context-baseline"},
		Findings: []Finding{{Title: "Bug", File: "review.go", StartLine: 2, EndLine: 2, Explanation: "bad return"}},
	}
	if _, err := SubmitTask(workspace, run.ID, baseline.ID, baselineSubmission); err != nil {
		t.Fatalf("SubmitTask baseline: %v", err)
	}
	if _, err := SubmitTask(workspace, run.ID, baseline.ID, baselineSubmission); err != nil {
		t.Fatalf("identical resubmission should be idempotent: %v", err)
	}
	changed := baselineSubmission
	changed.Findings = append(changed.Findings, Finding{Title: "Different", Explanation: "Different report", File: "review.go", StartLine: 2, EndLine: 2})
	if _, err := SubmitTask(workspace, run.ID, baseline.ID, changed); err == nil || !strings.Contains(err.Error(), "conflicting submission") {
		t.Fatalf("conflicting resubmission error = %v", err)
	}

	acrSubmission := TaskSubmission{
		ProtocolVersion: BenchmarkProtocolVersion, RunID: run.ID, TaskID: acr.ID,
		Executor: Executor{Host: "codex", Model: "sol", ContextID: "context-baseline"}, Findings: []Finding{},
	}
	if _, err := SubmitTask(workspace, run.ID, acr.ID, acrSubmission); err == nil || !strings.Contains(err.Error(), "fresh context") {
		t.Fatalf("shared context error = %v", err)
	}
	acrSubmission.Executor.ContextID = "context-acr"
	acrSubmission.Executor.Model = "different-model"
	if _, err := SubmitTask(workspace, run.ID, acr.ID, acrSubmission); err == nil || !strings.Contains(err.Error(), "same model") {
		t.Fatalf("mismatched model error = %v", err)
	}
}

func benchmarkPreparedRun(t *testing.T) (string, *Run) {
	t.Helper()
	repo, baseSHA, headSHA := benchmarkGitRepository(t)
	workspace := t.TempDir()
	manifest := Manifest{
		ProtocolVersion: BenchmarkProtocolVersion,
		Dataset:         DatasetMetadata{ID: "fixture", Version: "1"},
		Cases:           []Case{{ID: "case-1", Repository: repo, PRURL: "https://github.com/example/project/pull/1", BaseSHA: baseSHA, HeadSHA: headSHA}},
	}
	path := filepath.Join(workspace, "manifest.json")
	if err := SaveManifest(path, manifest); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}
	run, err := PrepareRun(context.Background(), workspace, PrepareRunOptions{
		DatasetPath: path, PRURL: manifest.Cases[0].PRURL,
		RepositoryOverrides: map[string]string{"case-1": repo},
	})
	if err != nil {
		t.Fatalf("PrepareRun: %v", err)
	}
	return workspace, run
}

func benchmarkGitRepository(t *testing.T) (string, string, string) {
	t.Helper()
	repo := t.TempDir()
	runGit(t, repo, "init", "-b", "main")
	runGit(t, repo, "config", "user.email", "acr@example.test")
	runGit(t, repo, "config", "user.name", "ACR Test")
	if err := os.WriteFile(filepath.Join(repo, "review.go"), []byte("package review\n\nfunc Value() int { return 1 }\n"), 0o600); err != nil {
		t.Fatalf("write base: %v", err)
	}
	runGit(t, repo, "add", "review.go")
	runGit(t, repo, "commit", "-m", "base")
	base := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
	if err := os.WriteFile(filepath.Join(repo, "review.go"), []byte("package review\n\nfunc Value() int { return 2 }\n"), 0o600); err != nil {
		t.Fatalf("write head: %v", err)
	}
	runGit(t, repo, "add", "review.go")
	runGit(t, repo, "commit", "-m", "head")
	head := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
	return repo, base, head
}

func runGit(t *testing.T, repo string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", arguments...)
	command.Dir = repo
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(arguments, " "), err, output)
	}
	return string(output)
}

func taskByArm(t *testing.T, run *Run, arm string) Task {
	t.Helper()
	for _, task := range run.Tasks {
		if task.Arm == arm {
			return task
		}
	}
	t.Fatalf("task arm %s not found", arm)
	return Task{}
}

func writeSubmissionFile(t *testing.T, path string, submission TaskSubmission) {
	t.Helper()
	data, err := json.Marshal(submission)
	if err != nil {
		t.Fatalf("marshal submission: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write submission: %v", err)
	}
}
