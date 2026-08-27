// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package benchmark

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReviewerSubmissionsCreateBlindedJudgesAndTiebreaker(t *testing.T) {
	workspace, run := benchmarkPreparedRunWithExpected(t, Finding{
		Title: "Authorization bypass", Description: "The role check accepts guests.",
		File: "review.go", StartLine: 3, EndLine: 3,
	})
	for _, arm := range []string{ArmBaseline, ArmACR} {
		task := taskByArm(t, run, arm)
		submission := TaskSubmission{
			ProtocolVersion: BenchmarkProtocolVersion, RunID: run.ID, TaskID: task.ID,
			Executor: Executor{Host: "codex", Model: "sol", ContextID: "context-" + arm},
			Findings: []Finding{{Title: "Guest access", Explanation: "Guests pass the role check.", File: "review.go", StartLine: 3, EndLine: 3}},
		}
		if _, err := SubmitTask(workspace, run.ID, task.ID, submission); err != nil {
			t.Fatalf("SubmitTask %s: %v", arm, err)
		}
	}

	loaded, err := LoadRun(workspace, run.ID)
	if err != nil {
		t.Fatalf("LoadRun: %v", err)
	}
	judges := tasksByArm(loaded, ArmJudge)
	if len(judges) != 2 || len(loaded.Evaluations) != 2 {
		t.Fatalf("expected two initial judges and two evaluations: tasks=%#v evaluations=%#v", judges, loaded.Evaluations)
	}
	for _, judge := range judges {
		prompt, err := os.ReadFile(judge.PromptPath)
		if err != nil {
			t.Fatalf("read judge prompt: %v", err)
		}
		lower := strings.ToLower(string(prompt))
		if strings.Contains(lower, "baseline") || strings.Contains(lower, "arm_acr") || strings.Contains(lower, `"arm"`) {
			t.Fatalf("judge prompt reveals experiment arm:\n%s", prompt)
		}
	}

	firstPair := judges[0].PairIDs[0]
	for index, judge := range judges {
		judgments := make([]Judgment, 0, len(judge.PairIDs))
		for _, pairID := range judge.PairIDs {
			decision := DecisionMatch
			if index == 1 && pairID == firstPair {
				decision = DecisionNoMatch
			}
			judgments = append(judgments, Judgment{PairID: pairID, Decision: decision, Rationale: "blinded comparison", Confidence: 0.9})
		}
		submission := TaskSubmission{
			ProtocolVersion: BenchmarkProtocolVersion, RunID: run.ID, TaskID: judge.ID,
			Executor: Executor{Host: "codex", Model: "sol", ContextID: "judge-context-" + judge.ID}, Judgments: judgments,
		}
		if _, err := SubmitTask(workspace, run.ID, judge.ID, submission); err != nil {
			t.Fatalf("submit judge %s: %v", judge.ID, err)
		}
	}

	loaded, err = LoadRun(workspace, run.ID)
	if err != nil {
		t.Fatalf("LoadRun after judges: %v", err)
	}
	judges = tasksByArm(loaded, ArmJudge)
	if len(judges) != 3 || judges[2].JudgeRound != 3 || len(judges[2].PairIDs) != 1 || judges[2].PairIDs[0] != firstPair {
		t.Fatalf("disagreement did not create focused tiebreaker: %#v", judges)
	}
	tiebreaker := judges[2]
	if _, err := SubmitTask(workspace, run.ID, tiebreaker.ID, TaskSubmission{
		ProtocolVersion: BenchmarkProtocolVersion, RunID: run.ID, TaskID: tiebreaker.ID,
		Executor:  Executor{Host: "codex", Model: "sol", ContextID: "judge-context-3"},
		Judgments: []Judgment{{PairID: firstPair, Decision: DecisionMatch, Rationale: "same defect", Confidence: 0.95}},
	}); err != nil {
		t.Fatalf("submit tiebreaker: %v", err)
	}

	completed, err := LoadRun(workspace, run.ID)
	if err != nil {
		t.Fatalf("LoadRun completed: %v", err)
	}
	for _, evaluation := range completed.Evaluations {
		if !evaluation.Score.Complete || evaluation.Score.Matched != 1 {
			t.Fatalf("evaluation was not resolved: %#v", evaluation)
		}
	}
	if _, err := os.Stat(filepath.Join(RunDir(workspace, run.ID), "report.json")); err != nil {
		t.Fatalf("automatic JSON report missing: %v", err)
	}
	markdown, err := os.ReadFile(filepath.Join(RunDir(workspace, run.ID), "report.md"))
	if err != nil {
		t.Fatalf("automatic Markdown report missing: %v", err)
	}
	if !strings.Contains(string(markdown), "Result: Tie") || !strings.Contains(string(markdown), "Baseline") || !strings.Contains(string(markdown), "ACR") {
		t.Fatalf("unexpected report:\n%s", markdown)
	}
}

func TestStrongMatchesCompleteWithoutJudgeTasks(t *testing.T) {
	workspace, run := benchmarkPreparedRunWithExpected(t, Finding{
		Title: "Wrong return value", Description: "Value returns two instead of one.",
		File: "review.go", StartLine: 3, EndLine: 3,
	})
	for _, arm := range []string{ArmBaseline, ArmACR} {
		task := taskByArm(t, run, arm)
		_, err := SubmitTask(workspace, run.ID, task.ID, TaskSubmission{
			ProtocolVersion: BenchmarkProtocolVersion, RunID: run.ID, TaskID: task.ID,
			Executor: Executor{Host: "codex", Model: "sol", ContextID: "context-" + arm},
			Findings: []Finding{{Title: "Wrong return value", Explanation: "Value returns two instead of one.", File: "review.go", StartLine: 3, EndLine: 3}},
		})
		if err != nil {
			t.Fatalf("SubmitTask %s: %v", arm, err)
		}
	}
	completed, err := LoadRun(workspace, run.ID)
	if err != nil {
		t.Fatalf("LoadRun: %v", err)
	}
	if len(tasksByArm(completed, ArmJudge)) != 0 {
		t.Fatalf("strong matches should not create judges: %#v", completed.Tasks)
	}
	for _, evaluation := range completed.Evaluations {
		if evaluation.Score.Matched != 1 || !evaluation.Score.Complete {
			t.Fatalf("strong evaluation incomplete: %#v", evaluation)
		}
	}
}

func benchmarkPreparedRunWithExpected(t *testing.T, expected Finding) (string, *Run) {
	t.Helper()
	repo, baseSHA, headSHA := benchmarkGitRepository(t)
	workspace := t.TempDir()
	manifest := Manifest{
		ProtocolVersion: BenchmarkProtocolVersion,
		Dataset:         DatasetMetadata{ID: "fixture", Version: "1"},
		Cases: []Case{{
			ID: "case-1", Repository: repo, PRURL: "https://github.com/example/project/pull/1",
			BaseSHA: baseSHA, HeadSHA: headSHA, Expected: []Finding{expected},
		}},
	}
	path := filepath.Join(workspace, "manifest.json")
	if err := SaveManifest(path, manifest); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}
	run, err := PrepareRun(t.Context(), workspace, PrepareRunOptions{
		DatasetPath: path, PRURL: manifest.Cases[0].PRURL,
		RepositoryOverrides: map[string]string{"case-1": repo},
	})
	if err != nil {
		t.Fatalf("PrepareRun: %v", err)
	}
	return workspace, run
}

func tasksByArm(run *Run, arm string) []Task {
	result := make([]Task, 0)
	for _, task := range run.Tasks {
		if task.Arm == arm {
			result = append(result, task)
		}
	}
	return result
}
