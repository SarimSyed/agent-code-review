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

	"github.com/alibaba/open-code-review/internal/delegation"
)

func TestPrepareRunAppliesCavemanEquallyToBothArms(t *testing.T) {
	repo, baseSHA, headSHA := benchmarkGitRepository(t)
	workspace := t.TempDir()
	manifest := Manifest{
		ProtocolVersion: BenchmarkProtocolVersion,
		Dataset:         DatasetMetadata{ID: "fixture", Version: "1"},
		Cases: []Case{{
			ID: "case-1", Repository: repo, PRURL: "https://github.com/example/project/pull/1",
			BaseSHA: baseSHA, HeadSHA: headSHA,
		}},
	}
	manifestPath := filepath.Join(workspace, "manifest.json")
	if err := SaveManifest(manifestPath, manifest); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}

	run, err := PrepareRun(context.Background(), workspace, PrepareRunOptions{
		DatasetPath: manifestPath, PRURL: manifest.Cases[0].PRURL,
		RepositoryOverrides: map[string]string{"case-1": repo},
		TokenEconomy:        delegation.TokenEconomy{Mode: delegation.TokenEconomyCaveman, Level: delegation.CavemanUltra},
	})
	if err != nil {
		t.Fatalf("PrepareRun: %v", err)
	}
	if run.TokenEconomy.Mode != delegation.TokenEconomyCaveman || run.TokenEconomy.Level != delegation.CavemanUltra {
		t.Fatalf("run token policy = %#v", run.TokenEconomy)
	}
	for _, task := range run.Tasks {
		prompt, readErr := os.ReadFile(task.PromptPath)
		if readErr != nil {
			t.Fatalf("read %s prompt: %v", task.Arm, readErr)
		}
		if !strings.Contains(string(prompt), "caveman") || !strings.Contains(string(prompt), delegation.CavemanUltra) {
			t.Fatalf("%s prompt omitted shared token policy:\n%s", task.Arm, prompt)
		}
		if task.Arm == ArmACR {
			request, loadErr := delegation.LoadRequest(task.CheckoutPath, task.ReviewSession)
			if loadErr != nil {
				t.Fatalf("LoadRequest: %v", loadErr)
			}
			if request.Instructions.TokenEconomy != run.TokenEconomy {
				t.Fatalf("ACR policy = %#v, run policy = %#v", request.Instructions.TokenEconomy, run.TokenEconomy)
			}
		}
	}
}

func TestSubmitTaskPersistsHostReportedUsage(t *testing.T) {
	workspace, run := benchmarkPreparedRun(t)
	task := taskByArm(t, run, ArmBaseline)
	submission := TaskSubmission{
		ProtocolVersion: BenchmarkProtocolVersion, RunID: run.ID, TaskID: task.ID,
		Executor: Executor{Host: "codex", Model: "sol", ContextID: "usage-context"},
		Usage:    &Usage{InputTokens: 1200, OutputTokens: 300, TotalTokens: 1500},
	}
	stored, err := SubmitTask(workspace, run.ID, task.ID, submission)
	if err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}
	if stored.Usage == nil || stored.Usage.TotalTokens != 1500 {
		t.Fatalf("stored usage = %#v", stored.Usage)
	}
}

func TestACRArmRejectsSubmissionBeforeValidatedReviewCompletes(t *testing.T) {
	workspace, run := benchmarkPreparedRun(t)
	task := taskByArm(t, run, ArmACR)
	_, err := SubmitTask(workspace, run.ID, task.ID, TaskSubmission{
		ProtocolVersion: BenchmarkProtocolVersion, RunID: run.ID, TaskID: task.ID,
		Executor: Executor{Host: "codex", Model: "sol", ContextID: "acr-context"}, Findings: []Finding{},
	})
	if err == nil || !strings.Contains(err.Error(), "ACR review is incomplete") {
		t.Fatalf("unphased ACR submission error = %v", err)
	}
}

func TestBenchmarkCommunicationPolicyAndUsageValidation(t *testing.T) {
	policy := delegation.TokenEconomy{Mode: delegation.TokenEconomyCaveman, Level: delegation.CavemanFull}
	tests := []struct {
		name          string
		communication *delegation.Communication
	}{
		{name: "missing"},
		{name: "wrong mode", communication: &delegation.Communication{Mode: delegation.TokenEconomyNormal, Level: delegation.CavemanFull, Backend: delegation.CommunicationSkill}},
		{name: "wrong level", communication: &delegation.Communication{Mode: delegation.TokenEconomyCaveman, Level: delegation.CavemanLite, Backend: delegation.CommunicationSkill}},
		{name: "wrong backend", communication: &delegation.Communication{Mode: delegation.TokenEconomyCaveman, Level: delegation.CavemanFull, Backend: delegation.CommunicationNormal}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := validateBenchmarkCommunication(policy, test.communication); err == nil {
				t.Fatal("invalid caveman communication should fail")
			}
		})
	}
	if err := validateBenchmarkCommunication(policy, &delegation.Communication{Mode: delegation.TokenEconomyCaveman, Level: delegation.CavemanFull, Backend: delegation.CommunicationFallback}); err != nil {
		t.Fatalf("valid compact fallback rejected: %v", err)
	}
	if err := validateBenchmarkCommunication(delegation.TokenEconomy{Mode: delegation.TokenEconomyNormal}, &delegation.Communication{Mode: delegation.TokenEconomyCaveman}); err == nil {
		t.Fatal("caveman metadata should not satisfy normal policy")
	}

	workspace, run := benchmarkPreparedRun(t)
	_ = workspace
	task := taskByArm(t, run, ArmBaseline)
	base := TaskSubmission{ProtocolVersion: BenchmarkProtocolVersion, RunID: run.ID, TaskID: task.ID, Executor: Executor{Host: "codex", Model: "sol", ContextID: "context"}}
	invalid := base
	invalid.Usage = &Usage{InputTokens: -1}
	if err := validateTaskSubmission(run, task, invalid); err == nil || !strings.Contains(err.Error(), "cannot be negative") {
		t.Fatalf("negative usage error = %v", err)
	}
	invalid.Usage = &Usage{InputTokens: 2, OutputTokens: 3, TotalTokens: 4}
	if err := validateTaskSubmission(run, task, invalid); err == nil || !strings.Contains(err.Error(), "must equal") {
		t.Fatalf("inconsistent usage error = %v", err)
	}
	invalid = base
	invalid.Judgments = []Judgment{{PairID: "pair"}}
	if err := validateTaskSubmission(run, task, invalid); err == nil || !strings.Contains(err.Error(), "findings, not judgments") {
		t.Fatalf("reviewer judgment error = %v", err)
	}
	invalidCases := []struct {
		name    string
		finding Finding
		message string
	}{
		{name: "missing explanation", finding: Finding{File: "review.go", StartLine: 1, EndLine: 1}, message: "requires an explanation"},
		{name: "unsafe path", finding: Finding{File: "../outside.go", StartLine: 1, EndLine: 1, Explanation: "bad"}, message: "unsafe finding path"},
		{name: "unreadable", finding: Finding{File: "missing.go", StartLine: 1, EndLine: 1, Explanation: "bad"}, message: "unreadable file"},
		{name: "line range", finding: Finding{File: "review.go", StartLine: 999, EndLine: 999, Explanation: "bad"}, message: "invalid line range"},
		{name: "confidence", finding: Finding{File: "review.go", StartLine: 1, EndLine: 1, Explanation: "bad", Confidence: 2}, message: "confidence"},
	}
	for _, test := range invalidCases {
		t.Run(test.name, func(t *testing.T) {
			invalid := base
			invalid.Findings = []Finding{test.finding}
			if err := validateTaskSubmission(run, task, invalid); err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("finding validation error = %v", err)
			}
		})
	}
	invalid = base
	invalid.ProtocolVersion = "wrong"
	if err := validateTaskSubmission(run, task, invalid); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("protocol mismatch error = %v", err)
	}
	invalid = base
	invalid.Executor = Executor{}
	if err := validateTaskSubmission(run, task, invalid); err == nil || !strings.Contains(err.Error(), "executor") {
		t.Fatalf("missing executor error = %v", err)
	}
}

func TestSubmissionRepairsUseSpecificMachineCodes(t *testing.T) {
	tests := map[string]string{
		"unsafe finding path":                 "unsafe_finding_path",
		"invalid line range":                  "invalid_line_range",
		"references an unreadable file":       "unknown_finding_file",
		"requires a fresh context":            "context_not_isolated",
		"must use the same model":             "model_mismatch",
		"unsupported judgment":                "invalid_judgment",
		"ACR review is incomplete":            "acr_review_incomplete",
		"validated ACR findings do not match": "acr_result_mismatch",
		"communication mode does not match":   "communication_mismatch",
	}
	for message, want := range tests {
		if repair := repairForError("task-1", errors.New(message)); repair.Code != want || repair.TaskID != "task-1" {
			t.Errorf("repair for %q = %#v, want %s", message, repair, want)
		}
	}
}

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

func completeBenchmarkACRReview(t *testing.T, task Task, findings []Finding) {
	t.Helper()
	request, err := delegation.LoadRequest(task.CheckoutPath, task.ReviewSession)
	if err != nil {
		t.Fatalf("LoadRequest: %v", err)
	}
	primary := delegation.Executor{Host: "codex", Model: "sol", ContextID: "acr-primary-" + task.ID}
	communication := delegation.Communication{Mode: delegation.TokenEconomyNormal, Backend: delegation.CommunicationNormal}
	candidateIDs := map[string]string{}
	dispositions := make([]delegation.CandidateDisposition, 0, len(findings))
	for {
		phaseTask, claimErr := delegation.ClaimNextPhase(task.CheckoutPath, task.ReviewSession, "fake-agent", time.Now(), time.Minute)
		if claimErr != nil {
			workflow, loadErr := delegation.LoadWorkflow(task.CheckoutPath, task.ReviewSession)
			if loadErr != nil || workflow.State != delegation.WorkflowReady {
				t.Fatalf("claim phase: %v; workflow=%#v load=%v", claimErr, workflow, loadErr)
			}
			break
		}
		unit := requestUnitForBenchmarkTest(t, request, phaseTask.UnitID)
		evidence := delegation.EvidenceRef{File: unit.Files[0].Path, StartLine: 1, EndLine: 1}
		executor := primary
		var payload any
		switch phaseTask.Phase {
		case delegation.PhaseIntent:
			payload = delegation.IntentPayload{Coverage: "Inspected changed behavior.", Invariants: []delegation.EvidenceStatement{{ID: "invariant-" + phaseTask.UnitID, Summary: "Changed behavior must preserve caller expectations.", Evidence: []delegation.EvidenceRef{evidence}}}}
		case delegation.PhaseImpact:
			questions := make([]delegation.InvestigatedQuestion, 0)
			for _, question := range request.ReviewQuestions {
				if question.UnitID == phaseTask.UnitID {
					questions = append(questions, delegation.InvestigatedQuestion{QuestionID: question.ID, Conclusion: "Inspected for benchmark fixture.", Evidence: []delegation.EvidenceRef{evidence}})
				}
			}
			payload = delegation.ImpactPayload{Coverage: "Traced callers and contracts.", Traces: []delegation.ImpactTrace{{ID: "trace-" + phaseTask.UnitID, Kind: "contract", Summary: "Changed value reaches callers.", Evidence: []delegation.EvidenceRef{evidence}}}, Questions: questions}
		case delegation.PhaseCandidates:
			candidates := make([]delegation.Candidate, 0)
			for index, finding := range findings {
				if !unitContainsBenchmarkPath(unit, finding.File) {
					continue
				}
				id := fmt.Sprintf("candidate-%d", index+1)
				candidateIDs[findingKey(finding)] = id
				candidates = append(candidates, delegation.Candidate{ID: id, File: finding.File, StartLine: finding.StartLine, EndLine: finding.EndLine, Title: finding.Title, Trigger: "Execute the changed code path.", Impact: finding.Explanation, Evidence: []delegation.EvidenceRef{{File: finding.File, StartLine: finding.StartLine, EndLine: finding.EndLine}}, Confidence: 0.9, InvariantIDs: []string{"invariant-" + phaseTask.UnitID}})
			}
			payload = delegation.CandidatesPayload{Coverage: "Inspected every changed line.", Candidates: candidates}
		case delegation.PhaseCritique:
			candidates := candidatesForBenchmarkUnit(findings, candidateIDs, unit)
			if len(candidates) == 0 {
				payload = delegation.CritiquePayload{CriticMode: delegation.CriticNotRequired, Verdicts: []delegation.CritiqueVerdict{}}
			} else {
				executor.ContextID = "acr-critic-" + phaseTask.UnitID
				verdicts := make([]delegation.CritiqueVerdict, 0, len(candidates))
				for _, id := range candidates {
					verdicts = append(verdicts, delegation.CritiqueVerdict{CandidateID: id, Verdict: delegation.CritiqueSupported, Rationale: "Fixture finding is reachable.", Confidence: 0.9})
				}
				payload = delegation.CritiquePayload{CriticMode: delegation.CriticIndependent, Verdicts: verdicts}
			}
		case delegation.PhaseFinalize:
			unitDispositions := make([]delegation.CandidateDisposition, 0)
			for _, id := range candidatesForBenchmarkUnit(findings, candidateIDs, unit) {
				disposition := delegation.CandidateDisposition{CandidateID: id, Outcome: delegation.DispositionSubmit, Reason: "Critic confirmed fixture finding."}
				unitDispositions = append(unitDispositions, disposition)
				dispositions = append(dispositions, disposition)
			}
			payload = delegation.FinalizePayload{Coverage: "Resolved every candidate.", CandidateDispositions: unitDispositions}
		}
		raw, marshalErr := json.Marshal(payload)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		phaseResult, submitErr := delegation.SubmitPhase(task.CheckoutPath, task.ReviewSession, phaseTask.ID, delegation.PhaseSubmission{ProtocolVersion: delegation.ProtocolVersion, SessionID: task.ReviewSession, TaskID: phaseTask.ID, UnitID: phaseTask.UnitID, Phase: phaseTask.Phase, Executor: executor, Communication: communication, Payload: raw})
		if submitErr != nil || !phaseResult.Accepted {
			t.Fatalf("submit %s: %#v, %v", phaseTask.Phase, phaseResult, submitErr)
		}
	}
	finalFindings := make([]delegation.Finding, 0, len(findings))
	for _, finding := range findings {
		unitID := unitIDForBenchmarkPath(t, request, finding.File)
		finalFindings = append(finalFindings, delegation.Finding{CandidateID: candidateIDs[findingKey(finding)], UnitID: unitID, File: finding.File, StartLine: finding.StartLine, EndLine: finding.EndLine, Severity: "medium", Category: "bug", Explanation: finding.Explanation, Evidence: finding.Explanation, Confidence: 0.9})
	}
	resolutions := make([]delegation.QuestionResolution, 0, len(request.ReviewQuestions))
	for _, question := range request.ReviewQuestions {
		resolutions = append(resolutions, delegation.QuestionResolution{QuestionID: question.ID, Outcome: "no_finding", Evidence: "Investigated in fixture review."})
	}
	result, err := delegation.Submit(task.CheckoutPath, task.ReviewSession, delegation.Submission{ProtocolVersion: delegation.ProtocolVersion, SessionID: task.ReviewSession, QuestionResolutions: resolutions, CandidateDispositions: dispositions, Findings: finalFindings})
	if err != nil || len(result.Rejected) != 0 {
		t.Fatalf("final ACR submit: %#v, %v", result, err)
	}
}

func requestUnitForBenchmarkTest(t *testing.T, request *delegation.Request, unitID string) delegation.ReviewUnit {
	t.Helper()
	for _, unit := range request.Units {
		if unit.ID == unitID {
			return unit
		}
	}
	t.Fatalf("unit %s not found", unitID)
	return delegation.ReviewUnit{}
}

func unitContainsBenchmarkPath(unit delegation.ReviewUnit, path string) bool {
	for _, file := range unit.Files {
		if file.Path == path {
			return true
		}
	}
	return false
}

func candidatesForBenchmarkUnit(findings []Finding, ids map[string]string, unit delegation.ReviewUnit) []string {
	result := make([]string, 0)
	for _, finding := range findings {
		if unitContainsBenchmarkPath(unit, finding.File) {
			result = append(result, ids[findingKey(finding)])
		}
	}
	return result
}

func unitIDForBenchmarkPath(t *testing.T, request *delegation.Request, path string) string {
	t.Helper()
	for _, unit := range request.Units {
		if unitContainsBenchmarkPath(unit, path) {
			return unit.ID
		}
	}
	t.Fatalf("path %s not found", path)
	return ""
}

func findingKey(finding Finding) string {
	return fmt.Sprintf("%s:%d:%d:%s", finding.File, finding.StartLine, finding.EndLine, finding.Explanation)
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
