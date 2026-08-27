// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package benchmark

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

func advanceRun(workspace string, run *Run, changedTaskID string) error {
	changedIndex := findTask(run.Tasks, changedTaskID)
	if changedIndex < 0 {
		return fmt.Errorf("changed benchmark task disappeared")
	}
	changed := run.Tasks[changedIndex]
	if changed.Arm == ArmJudge {
		if err := advanceJudgeBatch(workspace, run, changed.JudgeBatchID); err != nil {
			return err
		}
	} else if err := advanceReviewPair(workspace, run, changed.CaseID, changed.Trial); err != nil {
		return err
	}
	if runReadyForReport(run) {
		_, err := GenerateReport(workspace, run)
		return err
	}
	return nil
}

func advanceReviewPair(workspace string, run *Run, caseID string, trial int) error {
	reviewTasks := reviewPair(run, caseID, trial)
	if len(reviewTasks) != 2 || reviewTasks[0].SubmissionSHA == "" || reviewTasks[1].SubmissionSHA == "" {
		return nil
	}
	for _, evaluation := range run.Evaluations {
		if evaluation.CaseID == caseID && evaluation.Trial == trial {
			return nil
		}
	}
	benchmarkCase, ok := runCase(run, caseID)
	if !ok {
		return fmt.Errorf("benchmark case %q not found", caseID)
	}
	batchID := opaqueID("judge", fmt.Sprintf("%s:%d", caseID, trial))
	pairIDs := make([]string, 0)
	for _, task := range reviewTasks {
		submission, err := loadTaskSubmission(task.SubmissionPath)
		if err != nil {
			return err
		}
		analysis := AnalyzeFindings(benchmarkCase.Expected, submission.Findings)
		for index := range analysis.PendingPairs {
			analysis.PendingPairs[index].ID = opaqueID("candidate", task.ID+":"+analysis.PendingPairs[index].ID)
			pairIDs = append(pairIDs, analysis.PendingPairs[index].ID)
		}
		evaluation := Evaluation{
			ID: opaqueID("evaluation", task.ID), TaskID: task.ID, CaseID: caseID,
			Trial: trial, Arm: task.Arm, Analysis: analysis,
		}
		if len(analysis.PendingPairs) == 0 {
			evaluation.Score = ResolveAnalysis(analysis, nil)
		}
		run.Evaluations = append(run.Evaluations, evaluation)
	}
	if len(pairIDs) == 0 {
		setReviewPairState(run, caseID, trial, TaskScored)
		return nil
	}
	setReviewPairState(run, caseID, trial, TaskNeedsAdjudication)
	sort.Strings(pairIDs)
	for round := 1; round <= 2; round++ {
		task, err := createJudgeTask(workspace, run, benchmarkCase, trial, batchID, round, pairIDs, reviewTasks[0].CheckoutPath)
		if err != nil {
			return err
		}
		run.Tasks = append(run.Tasks, task)
	}
	return nil
}

func advanceJudgeBatch(workspace string, run *Run, batchID string) error {
	judges := judgeBatch(run, batchID)
	if len(judges) < 2 || judges[0].SubmissionSHA == "" || judges[1].SubmissionSHA == "" {
		return nil
	}
	first, err := loadTaskSubmission(judges[0].SubmissionPath)
	if err != nil {
		return err
	}
	second, err := loadTaskSubmission(judges[1].SubmissionPath)
	if err != nil {
		return err
	}
	disagreements := disagreementPairIDs(judges[0].PairIDs, first.Judgments, second.Judgments)
	if len(disagreements) > 0 {
		if len(judges) == 2 {
			benchmarkCase, ok := runCase(run, judges[0].CaseID)
			if !ok {
				return fmt.Errorf("benchmark case %q not found", judges[0].CaseID)
			}
			tiebreaker, err := createJudgeTask(workspace, run, benchmarkCase, judges[0].Trial, batchID, 3, disagreements, judges[0].CheckoutPath)
			if err != nil {
				return err
			}
			run.Tasks = append(run.Tasks, tiebreaker)
			return nil
		}
		if judges[2].SubmissionSHA == "" {
			return nil
		}
	}
	judgeSubmissions := make([][]Judgment, 0, len(judges))
	for _, judge := range judges {
		if judge.SubmissionSHA == "" {
			continue
		}
		submission, err := loadTaskSubmission(judge.SubmissionPath)
		if err != nil {
			return err
		}
		judgeSubmissions = append(judgeSubmissions, submission.Judgments)
	}
	caseID, trial := judges[0].CaseID, judges[0].Trial
	for index := range run.Evaluations {
		evaluation := &run.Evaluations[index]
		if evaluation.CaseID == caseID && evaluation.Trial == trial {
			evaluation.Score = ResolveAnalysis(evaluation.Analysis, judgeSubmissions)
			evaluation.Judgments = judgmentsForAnalysis(evaluation.Analysis, judgeSubmissions)
		}
	}
	setReviewPairState(run, caseID, trial, TaskScored)
	for index := range run.Tasks {
		if run.Tasks[index].JudgeBatchID == batchID {
			run.Tasks[index].State = TaskScored
		}
	}
	return nil
}

func createJudgeTask(workspace string, run *Run, benchmarkCase Case, trial int, batchID string, round int, pairIDs []string, sourceCheckout string) (Task, error) {
	taskID := fmt.Sprintf("%s-j%d", batchID, round)
	taskRoot := filepath.Join(RunDir(workspace, run.ID), "tasks", taskID)
	checkout := filepath.Join(taskRoot, "checkout")
	if err := os.MkdirAll(taskRoot, 0o700); err != nil {
		return Task{}, fmt.Errorf("create judge task: %w", err)
	}
	if output, err := runGitCommand(context.Background(), sourceCheckout, "worktree", "add", "--detach", checkout, benchmarkCase.HeadSHA); err != nil {
		return Task{}, fmt.Errorf("create judge checkout: %w: %s", err, output)
	}
	prompt, err := judgePrompt(run, benchmarkCase.ID, trial, pairIDs, checkout)
	if err != nil {
		return Task{}, err
	}
	promptPath := filepath.Join(taskRoot, "prompt.md")
	if err := os.WriteFile(promptPath, []byte(prompt), 0o600); err != nil {
		return Task{}, fmt.Errorf("write judge prompt: %w", err)
	}
	tree, err := trackedSourceSHA256(context.Background(), checkout)
	if err != nil {
		return Task{}, fmt.Errorf("fingerprint judge checkout: %w", err)
	}
	return Task{
		ID: taskID, CaseID: benchmarkCase.ID, Trial: trial, Arm: ArmJudge, State: TaskQueued,
		BaseSHA: benchmarkCase.BaseSHA, HeadSHA: benchmarkCase.HeadSHA, CheckoutPath: checkout,
		PromptPath: promptPath, SourceTreeSHA: tree, JudgeBatchID: batchID, JudgeRound: round, PairIDs: append([]string(nil), pairIDs...),
	}, nil
}

func judgmentsForAnalysis(analysis FindingAnalysis, submissions [][]Judgment) []Judgment {
	requested := map[string]bool{}
	for _, pair := range analysis.PendingPairs {
		requested[pair.ID] = true
	}
	result := make([]Judgment, 0)
	for _, submission := range submissions {
		for _, judgment := range submission {
			if requested[judgment.PairID] {
				result = append(result, judgment)
			}
		}
	}
	return result
}

func judgePrompt(run *Run, caseID string, trial int, pairIDs []string, checkout string) (string, error) {
	type promptPair struct {
		PairID    string  `json:"pair_id"`
		Expected  Finding `json:"expected"`
		Candidate Finding `json:"candidate"`
	}
	pairs := make([]promptPair, 0, len(pairIDs))
	requested := map[string]bool{}
	for _, id := range pairIDs {
		requested[id] = true
	}
	for _, evaluation := range run.Evaluations {
		if evaluation.CaseID != caseID || evaluation.Trial != trial {
			continue
		}
		for _, pair := range evaluation.Analysis.PendingPairs {
			if requested[pair.ID] {
				pairs = append(pairs, promptPair{PairID: pair.ID, Expected: evaluation.Analysis.Expected[pair.ExpectedIndex], Candidate: evaluation.Analysis.Predicted[pair.PredictedIndex]})
			}
		}
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].PairID < pairs[j].PairID })
	payload, err := json.MarshalIndent(pairs, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode judge packet: %w", err)
	}
	return fmt.Sprintf(`# Blinded finding adjudication

Independently decide whether each candidate describes the same concrete defect as the expected issue. Inspect code at %s when needed. Do not infer which review process produced a candidate. Do not modify files.

Pairs:

%s

Return protocol version "1", the run and task IDs supplied by the orchestrator, fresh executor metadata, and one judgment per pair. A judgment contains pair_id, decision "match" or "no_match", a concrete rationale, and confidence from 0 to 1.
`, checkout, payload), nil
}

func disagreementPairIDs(pairIDs []string, first, second []Judgment) []string {
	firstDecisions := judgmentDecisions(first)
	secondDecisions := judgmentDecisions(second)
	result := make([]string, 0)
	for _, pairID := range pairIDs {
		if firstDecisions[pairID] != secondDecisions[pairID] {
			result = append(result, pairID)
		}
	}
	return result
}

func judgmentDecisions(judgments []Judgment) map[string]string {
	result := map[string]string{}
	for _, judgment := range judgments {
		result[judgment.PairID] = judgment.Decision
	}
	return result
}

func reviewPair(run *Run, caseID string, trial int) []Task {
	result := make([]Task, 0, 2)
	for _, task := range run.Tasks {
		if task.CaseID == caseID && task.Trial == trial && (task.Arm == ArmBaseline || task.Arm == ArmACR) {
			result = append(result, task)
		}
	}
	return result
}

func judgeBatch(run *Run, batchID string) []Task {
	result := make([]Task, 0, 3)
	for _, task := range run.Tasks {
		if task.JudgeBatchID == batchID {
			result = append(result, task)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].JudgeRound < result[j].JudgeRound })
	return result
}

func setReviewPairState(run *Run, caseID string, trial int, state string) {
	for index := range run.Tasks {
		if run.Tasks[index].CaseID == caseID && run.Tasks[index].Trial == trial && run.Tasks[index].Arm != ArmJudge {
			run.Tasks[index].State = state
		}
	}
}

func runCase(run *Run, caseID string) (Case, bool) {
	for _, benchmarkCase := range run.Cases {
		if benchmarkCase.ID == caseID {
			return benchmarkCase, true
		}
	}
	return Case{}, false
}

func loadTaskSubmission(path string) (TaskSubmission, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return TaskSubmission{}, fmt.Errorf("read benchmark submission: %w", err)
	}
	var submission TaskSubmission
	if err := json.Unmarshal(data, &submission); err != nil {
		return TaskSubmission{}, fmt.Errorf("decode benchmark submission: %w", err)
	}
	return submission, nil
}

func opaqueID(prefix, value string) string {
	digest := sha256.Sum256([]byte(value))
	return prefix + "-" + hex.EncodeToString(digest[:8])
}

func runReadyForReport(run *Run) bool {
	reviewTasks := 0
	for _, task := range run.Tasks {
		if task.Arm == ArmBaseline || task.Arm == ArmACR {
			reviewTasks++
			if task.State != TaskScored && task.State != TaskFailed {
				return false
			}
		}
	}
	return reviewTasks > 0
}
