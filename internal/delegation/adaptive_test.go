// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package delegation

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAdaptivePrepareCreatesProtocolV3AnalysisTasks(t *testing.T) {
	repo := t.TempDir()
	for _, name := range []string{"one.go", "two.go"} {
		if err := os.WriteFile(filepath.Join(repo, name), []byte("package example\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	request, err := Prepare(repo, PrepareInput{
		Mode: ModeScan, Profile: ReviewProfileAdaptive,
		Units: []PreparedUnit{
			{ID: "unit-1", Files: []PreparedFile{{Path: "one.go"}}},
			{ID: "unit-2", Files: []PreparedFile{{Path: "two.go"}}},
		},
	})
	if err != nil {
		t.Fatalf("Prepare(adaptive): %v", err)
	}
	if request.ProtocolVersion != AdaptiveProtocolVersion {
		t.Fatalf("protocol = %q, want %q", request.ProtocolVersion, AdaptiveProtocolVersion)
	}
	workflow, err := LoadWorkflow(repo, request.SessionID)
	if err != nil {
		t.Fatalf("LoadWorkflow(adaptive): %v", err)
	}
	if workflow.ProtocolVersion != AdaptiveProtocolVersion || workflow.State != WorkflowActive || len(workflow.Tasks) != 2 {
		t.Fatalf("adaptive workflow = %#v", workflow)
	}
	for _, task := range workflow.Tasks {
		if task.Phase != PhaseAnalysis || task.State != PhaseQueued {
			t.Fatalf("adaptive task = %#v", task)
		}
	}
}

func TestAdaptiveBatchSubmissionPartiallyAcceptsAndCompletesSafeEmptyReview(t *testing.T) {
	repo, request := prepareAdaptiveFixture(t, false)
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	tasks, err := ClaimReadyPhases(repo, request.SessionID, "primary", now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	primary := Executor{Host: "codex", Model: "gpt-5.6-terra", ContextID: "primary-terra"}
	submissions := make([]PhaseSubmission, 0, len(tasks))
	for _, task := range tasks {
		submissions = append(submissions, adaptiveAnalysisSubmission(t, request, task, primary, RiskLow))
	}
	var invalid AdaptiveAnalysisPayload
	if err := json.Unmarshal(submissions[1].Payload, &invalid); err != nil {
		t.Fatal(err)
	}
	invalid.Risk.Rationale = ""
	submissions[1].Payload = mustPhasePayload(t, invalid)

	result, err := submitPhaseBatchAt(repo, request.SessionID, PhaseBatchSubmission{
		ProtocolVersion: AdaptiveProtocolVersion, SessionID: request.SessionID, Submissions: submissions,
	}, now.Add(2*time.Second))
	if err != nil {
		t.Fatalf("SubmitPhaseBatch: %v", err)
	}
	if len(result.Results) != 2 || !result.Results[0].Accepted || result.Results[1].Accepted || !hasPhaseRejection(result.Results[1].Rejections, "missing_risk_rationale") {
		t.Fatalf("partial batch result = %#v", result)
	}
	workflow, err := LoadWorkflow(repo, request.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if workflow.State != WorkflowActive || workflow.Tasks[0].State != PhaseSubmitted || workflow.Tasks[1].State != PhaseClaimed {
		t.Fatalf("partial workflow = %#v", workflow)
	}

	submissions[1] = adaptiveAnalysisSubmission(t, request, tasks[1], primary, RiskLow)
	result, err = submitPhaseBatchAt(repo, request.SessionID, PhaseBatchSubmission{
		ProtocolVersion: AdaptiveProtocolVersion, SessionID: request.SessionID, Submissions: []PhaseSubmission{submissions[1]},
	}, now.Add(3*time.Second))
	if err != nil || len(result.Results) != 1 || !result.Results[0].Accepted || result.WorkflowState != WorkflowReady {
		t.Fatalf("completed batch = %#v, %v", result, err)
	}
	workflow, err = LoadWorkflow(repo, request.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if workflow.BatchCalls != 2 || workflow.ValidationRejections != 1 {
		t.Fatalf("workflow counters = %#v", workflow)
	}
	for _, task := range workflow.Tasks {
		if task.SubmittedAt.IsZero() || task.DurationMS <= 0 {
			t.Fatalf("missing task timing: %#v", task)
		}
	}
}

func TestAdaptiveAnalysisQueuesCritiqueForCandidatesOrRiskQuestions(t *testing.T) {
	for _, test := range []struct {
		name      string
		risky     bool
		candidate bool
	}{
		{name: "candidate", candidate: true},
		{name: "risk question", risky: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo, request := prepareAdaptiveFixture(t, test.risky)
			now := time.Date(2026, 9, 4, 13, 0, 0, 0, time.UTC)
			tasks, err := ClaimReadyPhases(repo, request.SessionID, "primary", now, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			primary := Executor{Host: "codex", Model: "gpt-5.6-luna", ContextID: "primary-luna"}
			submissions := make([]PhaseSubmission, 0, len(tasks))
			for index, task := range tasks {
				submission := adaptiveAnalysisSubmission(t, request, task, primary, RiskLow)
				if test.candidate && index == 0 {
					var payload AdaptiveAnalysisPayload
					if err := json.Unmarshal(submission.Payload, &payload); err != nil {
						t.Fatal(err)
					}
					payload.Candidates = []Candidate{adaptiveCandidate(task.UnitID, request.Units[index].Files[0].Path)}
					submission.Payload = mustPhasePayload(t, payload)
				}
				submissions = append(submissions, submission)
			}
			result, err := submitPhaseBatchAt(repo, request.SessionID, PhaseBatchSubmission{
				ProtocolVersion: AdaptiveProtocolVersion, SessionID: request.SessionID, Submissions: submissions,
			}, now.Add(time.Second))
			if err != nil || result.WorkflowState != WorkflowActive {
				t.Fatalf("analysis batch = %#v, %v", result, err)
			}
			critics, err := ClaimReadyPhases(repo, request.SessionID, "critic", now.Add(2*time.Second), time.Minute)
			if err != nil {
				t.Fatalf("claim critique: %v", err)
			}
			want := 1
			if test.risky {
				want = len(request.Units)
				for _, unit := range request.Units {
					if len(questionsForUnit(request, unit.ID)) == 0 {
						want--
					}
				}
			}
			if len(critics) != want {
				t.Fatalf("critique tasks = %#v, want %d", critics, want)
			}
			for _, task := range critics {
				if task.Phase != PhaseCritique {
					t.Fatalf("queued task = %#v", task)
				}
			}
		})
	}
}

func TestAdaptiveSupportedCritiqueFinalizesAndEnforcesFreshSameModelContext(t *testing.T) {
	repo, request, primary, critiqueTask := prepareAdaptiveCandidateForCritique(t, "local-model-x")
	bad := adaptiveCritiqueSubmission(t, request, critiqueTask, Executor{Host: primary.Host, Model: "other-model", ContextID: "critic"}, CritiqueSupported)
	result, err := SubmitPhase(repo, request.SessionID, critiqueTask.ID, bad)
	if err != nil || result.Accepted || !hasPhaseRejection(result.Rejections, "critic_model_mismatch") {
		t.Fatalf("wrong-model critique = %#v, %v", result, err)
	}
	bad.Executor = Executor{Host: primary.Host, Model: primary.Model, ContextID: primary.ContextID}
	result, err = SubmitPhase(repo, request.SessionID, critiqueTask.ID, bad)
	if err != nil || result.Accepted || !hasPhaseRejection(result.Rejections, "critic_context_not_fresh") {
		t.Fatalf("same-context independent critique = %#v, %v", result, err)
	}
	good := adaptiveCritiqueSubmission(t, request, critiqueTask, Executor{Host: primary.Host, Model: primary.Model, ContextID: "fresh-critic"}, CritiqueSupported)
	result, err = SubmitPhase(repo, request.SessionID, critiqueTask.ID, good)
	if err != nil || !result.Accepted || result.WorkflowState != WorkflowReady {
		t.Fatalf("supported critique = %#v, %v", result, err)
	}
	workflow, err := LoadWorkflow(repo, request.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if workflow.CriticMode != CriticIndependent || hasTasksOfPhase(workflow.Tasks, PhaseResolve) {
		t.Fatalf("supported workflow = %#v", workflow)
	}
}

func TestAdaptiveCriticDisagreementOrDiscoveryQueuesPrimaryResolution(t *testing.T) {
	for _, test := range []struct {
		name       string
		verdict    string
		discovered bool
	}{
		{name: "unsupported", verdict: CritiqueUnsupported},
		{name: "revised", verdict: CritiqueRevise},
		{name: "discovered", verdict: CritiqueSupported, discovered: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo, request, primary, critiqueTask := prepareAdaptiveCandidateForCritique(t, "gpt-5.6-sol")
			submission := adaptiveCritiqueSubmission(t, request, critiqueTask, Executor{Host: primary.Host, Model: primary.Model, ContextID: "critic-sol"}, test.verdict)
			var payload CritiquePayload
			if err := json.Unmarshal(submission.Payload, &payload); err != nil {
				t.Fatal(err)
			}
			if test.verdict == CritiqueRevise {
				replacement := adaptiveCandidate(critiqueTask.UnitID, request.Units[0].Files[0].Path)
				replacement.ID = "critic-revision"
				payload.Verdicts[0].Replacement = &replacement
			}
			if test.discovered {
				candidate := adaptiveCandidate(critiqueTask.UnitID, request.Units[0].Files[0].Path)
				candidate.ID = "critic-discovery"
				payload.NewCandidates = []Candidate{candidate}
			}
			submission.Payload = mustPhasePayload(t, payload)
			result, err := SubmitPhase(repo, request.SessionID, critiqueTask.ID, submission)
			if err != nil || !result.Accepted || result.WorkflowState != WorkflowActive {
				t.Fatalf("critique = %#v, %v", result, err)
			}
			resolve, err := ClaimReadyPhases(repo, request.SessionID, "primary", time.Now().UTC(), time.Minute)
			if err != nil || len(resolve) != 1 || resolve[0].Phase != PhaseResolve {
				t.Fatalf("resolve tasks = %#v, %v", resolve, err)
			}
		})
	}
}

func TestAdaptiveResolveRequiresPrimaryOverrideAndDraftsRenderReadyFinding(t *testing.T) {
	repo, request, primary, critiqueTask := prepareAdaptiveCandidateForCritique(t, "gpt-5.6-terra")
	critique := adaptiveCritiqueSubmission(t, request, critiqueTask, Executor{Host: primary.Host, Model: primary.Model, ContextID: "critic"}, CritiqueUnsupported)
	if result, err := SubmitPhase(repo, request.SessionID, critiqueTask.ID, critique); err != nil || !result.Accepted {
		t.Fatalf("submit critique = %#v, %v", result, err)
	}
	resolveTasks, err := ClaimReadyPhases(repo, request.SessionID, "primary", time.Now().UTC(), time.Minute)
	if err != nil || len(resolveTasks) != 1 {
		t.Fatalf("claim resolve = %#v, %v", resolveTasks, err)
	}
	disposition := CandidateDisposition{CandidateID: "candidate-" + resolveTasks[0].UnitID, Outcome: DispositionSubmit, Reason: "Primary rechecked the contract."}
	submission := PhaseSubmission{
		ProtocolVersion: AdaptiveProtocolVersion, SessionID: request.SessionID, TaskID: resolveTasks[0].ID, UnitID: resolveTasks[0].UnitID, Phase: PhaseResolve,
		Executor: primary, Communication: Communication{Mode: TokenEconomyNormal, Backend: CommunicationNormal},
		Payload: mustPhasePayload(t, ResolvePayload{Coverage: "Rechecked the critic disagreement.", CandidateDispositions: []CandidateDisposition{disposition}}),
	}
	result, err := SubmitPhase(repo, request.SessionID, resolveTasks[0].ID, submission)
	if err != nil || result.Accepted || !hasPhaseRejection(result.Rejections, "critic_override_required") {
		t.Fatalf("missing override = %#v, %v", result, err)
	}
	disposition.OverrideReason = "The critic missed the caller contract."
	disposition.AdditionalEvidence = []EvidenceRef{{File: request.Units[0].Files[0].Path, StartLine: 2, EndLine: 2}}
	submission.Payload = mustPhasePayload(t, ResolvePayload{Coverage: "Rechecked the critic disagreement.", CandidateDispositions: []CandidateDisposition{disposition}})
	result, err = SubmitPhase(repo, request.SessionID, resolveTasks[0].ID, submission)
	if err != nil || !result.Accepted || result.WorkflowState != WorkflowReady {
		t.Fatalf("resolved override = %#v, %v", result, err)
	}
	draft, _, err := CreateSubmissionDraft(repo, request.SessionID)
	if err != nil {
		t.Fatalf("CreateSubmissionDraft: %v", err)
	}
	if len(draft.Findings) != 1 || len(draft.CandidateDispositions) != 1 {
		t.Fatalf("adaptive draft = %#v", draft)
	}
	finding := draft.Findings[0]
	if finding.CandidateID != disposition.CandidateID || finding.File != request.Units[0].Files[0].Path || finding.StartLine != 2 || finding.Severity != "high" || finding.Category != "bug" || finding.Evidence == "" {
		t.Fatalf("generated finding = %#v", finding)
	}
	if draft.CandidateDispositions[0].OverrideReason == "" || len(draft.CandidateDispositions[0].AdditionalEvidence) == 0 {
		t.Fatalf("generated disposition = %#v", draft.CandidateDispositions[0])
	}
	final, err := Submit(repo, request.SessionID, *draft)
	if err != nil || len(final.Rejected) != 0 || len(final.Findings) != 1 || final.Assurance == nil || final.Assurance.WorkflowState != WorkflowComplete {
		t.Fatalf("adaptive final result = %#v, %v", final, err)
	}
}

func TestAdaptiveAssuranceAggregatesBatchedStageWindowsWithoutDoubleCounting(t *testing.T) {
	base := time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC)
	workflow := &Workflow{ProtocolVersion: AdaptiveProtocolVersion, State: WorkflowReady, Tasks: []PhaseTask{
		{Phase: PhaseAnalysis, State: PhaseSubmitted, ClaimedAt: base, SubmittedAt: base.Add(3 * time.Second)},
		{Phase: PhaseAnalysis, State: PhaseSubmitted, ClaimedAt: base.Add(time.Second), SubmittedAt: base.Add(4 * time.Second)},
		{Phase: PhaseCritique, State: PhaseSubmitted, ClaimedAt: base.Add(5 * time.Second), SubmittedAt: base.Add(7 * time.Second)},
		{Phase: PhaseResolve, State: PhaseSubmitted, ClaimedAt: base.Add(8 * time.Second), SubmittedAt: base.Add(10 * time.Second)},
	}}
	assurance := buildReviewAssurance(&Request{Instructions: Instructions{TokenEconomy: TokenEconomy{Mode: TokenEconomyNormal}}}, workflow, Submission{})
	if assurance.AnalysisMS != 4000 || assurance.CritiqueMS != 2000 || assurance.ResolutionMS != 2000 || assurance.TotalElapsedMS != 10000 {
		t.Fatalf("timing assurance = %#v", assurance)
	}
}

func prepareAdaptiveCandidateForCritique(t *testing.T, model string) (string, *Request, Executor, PhaseTask) {
	t.Helper()
	repo, request := prepareAdaptiveFixture(t, false)
	now := time.Date(2026, 9, 4, 14, 0, 0, 0, time.UTC)
	tasks, err := ClaimReadyPhases(repo, request.SessionID, "primary", now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	primary := Executor{Host: "codex", Model: model, ContextID: "primary"}
	submissions := make([]PhaseSubmission, 0, len(tasks))
	for index, task := range tasks {
		submission := adaptiveAnalysisSubmission(t, request, task, primary, RiskLow)
		if index == 0 {
			var payload AdaptiveAnalysisPayload
			if err := json.Unmarshal(submission.Payload, &payload); err != nil {
				t.Fatal(err)
			}
			payload.Candidates = []Candidate{adaptiveCandidate(task.UnitID, request.Units[index].Files[0].Path)}
			submission.Payload = mustPhasePayload(t, payload)
		}
		submissions = append(submissions, submission)
	}
	if _, err := submitPhaseBatchAt(repo, request.SessionID, PhaseBatchSubmission{ProtocolVersion: AdaptiveProtocolVersion, SessionID: request.SessionID, Submissions: submissions}, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	critics, err := ClaimReadyPhases(repo, request.SessionID, "critic", now.Add(2*time.Second), time.Minute)
	if err != nil || len(critics) != 1 {
		t.Fatalf("claim critic = %#v, %v", critics, err)
	}
	return repo, request, primary, critics[0]
}

func adaptiveCritiqueSubmission(t *testing.T, request *Request, task PhaseTask, executor Executor, verdict string) PhaseSubmission {
	t.Helper()
	payload := CritiquePayload{CriticMode: CriticIndependent, Verdicts: []CritiqueVerdict{{
		CandidateID: "candidate-" + task.UnitID, Verdict: verdict, Rationale: "Checked the changed contract and caller.", Confidence: 0.9,
	}}}
	return PhaseSubmission{
		ProtocolVersion: AdaptiveProtocolVersion, SessionID: request.SessionID, TaskID: task.ID, UnitID: task.UnitID, Phase: task.Phase,
		Executor: executor, Communication: Communication{Mode: TokenEconomyNormal, Backend: CommunicationNormal}, Payload: mustPhasePayload(t, payload),
	}
}

func adaptiveAnalysisSubmission(t *testing.T, request *Request, task PhaseTask, executor Executor, risk string) PhaseSubmission {
	t.Helper()
	unit, err := requestUnit(request, task.UnitID)
	if err != nil {
		t.Fatal(err)
	}
	evidence := EvidenceRef{File: unit.Files[0].Path, StartLine: 2, EndLine: 2}
	questions := make([]InvestigatedQuestion, 0)
	for _, question := range request.ReviewQuestions {
		if question.UnitID == task.UnitID {
			questions = append(questions, InvestigatedQuestion{QuestionID: question.ID, Conclusion: "The changed contract was inspected.", Evidence: []EvidenceRef{evidence}})
		}
	}
	payload := AdaptiveAnalysisPayload{
		Coverage:        "Inspected the changed behavior and its direct contract.",
		Risk:            RiskAssessment{Level: risk, Rationale: "No unresolved behavior remains."},
		BehaviorChanges: []EvidenceStatement{{ID: "behavior-" + task.UnitID, Summary: "The return behavior changed.", Evidence: []EvidenceRef{evidence}}},
		Invariants:      []EvidenceStatement{{ID: "invariant-" + task.UnitID, Summary: "Callers retain the return contract.", Evidence: []EvidenceRef{evidence}}},
		Traces:          []ImpactTrace{{ID: "trace-" + task.UnitID, Kind: "contract", Summary: "The return reaches its caller.", Evidence: []EvidenceRef{evidence}}},
		Questions:       questions,
		Candidates:      []Candidate{},
	}
	return PhaseSubmission{
		ProtocolVersion: AdaptiveProtocolVersion, SessionID: request.SessionID, TaskID: task.ID,
		UnitID: task.UnitID, Phase: PhaseAnalysis, Executor: executor,
		Communication: Communication{Mode: TokenEconomyNormal, Backend: CommunicationNormal},
		Payload:       mustPhasePayload(t, payload),
	}
}

func adaptiveCandidate(unitID, path string) Candidate {
	return Candidate{
		ID: "candidate-" + unitID, File: path, StartLine: 2, EndLine: 2,
		Title: "Changed return breaks callers", Trigger: "Call the changed function.", Impact: "The caller receives the wrong value.",
		Severity: "high", Category: "bug", Explanation: "The changed return violates the caller contract.", SuggestedFix: "Restore the required return value.",
		Evidence: []EvidenceRef{{File: path, StartLine: 2, EndLine: 2}}, Confidence: 0.9,
		InvariantIDs: []string{"invariant-" + unitID},
	}
}

func TestClaimReadyPhasesClaimsBatchAndReclaimsExpiredLeases(t *testing.T) {
	repo, request := prepareAdaptiveFixture(t, false)
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	tasks, err := ClaimReadyPhases(repo, request.SessionID, "primary", now, time.Minute)
	if err != nil {
		t.Fatalf("ClaimReadyPhases: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("claimed %d tasks, want 2: %#v", len(tasks), tasks)
	}
	for _, task := range tasks {
		if task.Phase != PhaseAnalysis || task.Worker != "primary" || !task.ClaimedAt.Equal(now) {
			t.Fatalf("claimed task = %#v", task)
		}
	}
	if _, err := ClaimReadyPhases(repo, request.SessionID, "other", now, time.Minute); err == nil {
		t.Fatal("claimed batch should not remain ready")
	}
	reclaimed, err := ClaimReadyPhases(repo, request.SessionID, "other", now.Add(2*time.Minute), time.Minute)
	if err != nil {
		t.Fatalf("reclaim expired batch: %v", err)
	}
	if len(reclaimed) != 2 {
		t.Fatalf("reclaimed %d tasks, want 2", len(reclaimed))
	}
	for _, task := range reclaimed {
		if task.Worker != "other" {
			t.Fatalf("reclaimed task = %#v", task)
		}
	}
}

func prepareAdaptiveFixture(t *testing.T, risky bool) (string, *Request) {
	t.Helper()
	repo := t.TempDir()
	units := make([]PreparedUnit, 0, 2)
	for index, name := range []string{"one.go", "two.go"} {
		content := "package example\nfunc value() int { return 1 }\n"
		diff := "@@ -1,2 +1,2 @@\n package example\n-func value() int { return 0 }\n+func value() int { return 1 }"
		if risky && index == 0 {
			content = "package example\nfunc boot() { service.start() }\n"
			diff = "@@ -1,3 +1,2 @@\n package example\n-func boot() { service.start() }\n-func stop() { service.stop() }\n+func boot() {}"
		}
		if err := os.WriteFile(filepath.Join(repo, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		units = append(units, PreparedUnit{ID: fmt.Sprintf("unit-%d", index+1), Files: []PreparedFile{{Path: name, Diff: diff}}})
	}
	request, err := Prepare(repo, PrepareInput{Mode: ModeDiff, Profile: ReviewProfileAdaptive, Units: units})
	if err != nil {
		t.Fatalf("Prepare(adaptive fixture): %v", err)
	}
	return repo, request
}
