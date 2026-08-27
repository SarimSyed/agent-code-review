// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package delegation

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPrepareCreatesProtocolV2WorkflowAndStandardBypassesPhases(t *testing.T) {
	repo, request := prepareDiffFixture(t)
	if request.ProtocolVersion != "2" {
		t.Fatalf("protocol = %q, want 2", request.ProtocolVersion)
	}
	workflow, err := LoadWorkflow(repo, request.SessionID)
	if err != nil {
		t.Fatalf("LoadWorkflow() error: %v", err)
	}
	if len(workflow.Tasks) != 5 || workflow.State != WorkflowActive {
		t.Fatalf("unexpected deep workflow: %#v", workflow)
	}

	standardRepo := t.TempDir()
	if err := os.WriteFile(filepath.Join(standardRepo, "app.go"), []byte("package app\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	standard, err := Prepare(standardRepo, PrepareInput{
		Mode: ModeScan, Profile: ReviewProfileStandard,
		Units: []PreparedUnit{{ID: "unit-1", Files: []PreparedFile{{Path: "app.go"}}}},
	})
	if err != nil {
		t.Fatalf("Prepare(standard) error: %v", err)
	}
	workflow, err = LoadWorkflow(standardRepo, standard.SessionID)
	if err != nil {
		t.Fatalf("LoadWorkflow(standard) error: %v", err)
	}
	if len(workflow.Tasks) != 0 || workflow.State != WorkflowReady {
		t.Fatalf("standard workflow should bypass phases: %#v", workflow)
	}
}

func TestLoadRequestAcceptsLegacyProtocolV1(t *testing.T) {
	repo, request := prepareDiffFixture(t)
	request.ProtocolVersion = LegacyProtocolVersion
	request.Instructions.RequiredPasses = nil
	request.Instructions.TokenEconomy = TokenEconomy{}
	if err := writeJSON(filepath.Join(SessionDir(repo, request.SessionID), RequestFileName), request); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(SessionDir(repo, request.SessionID), WorkflowFileName)); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadRequest(repo, request.SessionID)
	if err != nil {
		t.Fatalf("LoadRequest(v1) error: %v", err)
	}
	if loaded.ProtocolVersion != LegacyProtocolVersion {
		t.Fatalf("loaded protocol = %q", loaded.ProtocolVersion)
	}
}

func TestLoadWorkflowRejectsMalformedState(t *testing.T) {
	repo, request := prepareDiffFixture(t)
	workflow, err := LoadWorkflow(repo, request.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	workflow.State = "teleported"
	if err := saveWorkflow(repo, workflow); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadWorkflow(repo, request.SessionID); err == nil || !strings.Contains(err.Error(), "workflow state") {
		t.Fatalf("malformed workflow error = %v", err)
	}
}

func TestCavemanPolicyRequiresValidLevelAndPersists(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "app.go"), []byte("package app\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Prepare(repo, PrepareInput{
		Mode: ModeScan, TokenEconomy: TokenEconomy{Mode: TokenEconomyCaveman, Level: "maximum"},
		Units: []PreparedUnit{{ID: "unit-1", Files: []PreparedFile{{Path: "app.go"}}}},
	})
	if err == nil || !strings.Contains(err.Error(), "caveman level") {
		t.Fatalf("invalid level error = %v", err)
	}

	request, err := Prepare(repo, PrepareInput{
		Mode: ModeScan, TokenEconomy: TokenEconomy{Mode: TokenEconomyCaveman, Level: CavemanFull},
		Units: []PreparedUnit{{ID: "unit-1", Files: []PreparedFile{{Path: "app.go"}}}},
	})
	if err != nil {
		t.Fatalf("Prepare(caveman) error: %v", err)
	}
	if got := request.Instructions.TokenEconomy; got.Mode != TokenEconomyCaveman || got.Level != CavemanFull {
		t.Fatalf("token policy = %#v", got)
	}
}

func TestCavemanHandoffIsShorterWithoutDroppingWorkflowCommands(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "app.go"), []byte("package app\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	makeRequest := func(policy TokenEconomy) *Request {
		request, err := Prepare(repo, PrepareInput{
			Mode: ModeScan, TokenEconomy: policy,
			Units: []PreparedUnit{{ID: "unit-1", Files: []PreparedFile{{Path: "app.go"}}}},
		})
		if err != nil {
			t.Fatal(err)
		}
		return request
	}
	normal, err := HandoffPrompt(makeRequest(TokenEconomy{Mode: TokenEconomyNormal}))
	if err != nil {
		t.Fatal(err)
	}
	compact, err := HandoffPrompt(makeRequest(TokenEconomy{Mode: TokenEconomyCaveman, Level: CavemanUltra}))
	if err != nil {
		t.Fatal(err)
	}
	if len(compact) >= len(normal) {
		t.Fatalf("compact handoff is not shorter: compact=%d normal=%d", len(compact), len(normal))
	}
	for _, required := range []string{"acr review phase next", "acr review phase submit", "acr review submit", "Do not modify source"} {
		if !strings.Contains(compact, required) {
			t.Fatalf("compact handoff missing %q:\n%s", required, compact)
		}
	}
}

func TestCavemanBackendCannotChangeMidWorkflow(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "app.go"), []byte("package app\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	request, err := Prepare(repo, PrepareInput{
		Mode: ModeScan, TokenEconomy: TokenEconomy{Mode: TokenEconomyCaveman},
		Units: []PreparedUnit{{ID: "unit-1", Files: []PreparedFile{{Path: "app.go"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	executor := Executor{Host: "codex", Model: "sol", ContextID: "primary"}
	submitClaimedPhase(t, repo, request, executor, Communication{Mode: TokenEconomyCaveman, Level: CavemanFull, Backend: CommunicationFallback}, IntentPayload{
		Coverage:        "Inspected file.",
		BehaviorChanges: []EvidenceStatement{{ID: "behavior-1", Summary: "File reviewed.", Evidence: []EvidenceRef{{File: "app.go", StartLine: 1, EndLine: 1}}}},
	})
	task, err := ClaimNextPhase(repo, request.SessionID, "worker", time.Now(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	result, err := SubmitPhase(repo, request.SessionID, task.ID, PhaseSubmission{
		ProtocolVersion: ProtocolVersion, SessionID: request.SessionID, TaskID: task.ID,
		UnitID: task.UnitID, Phase: task.Phase, Executor: executor,
		Communication: Communication{Mode: TokenEconomyCaveman, Level: CavemanFull, Backend: CommunicationSkill},
		Payload:       mustPhasePayload(t, ImpactPayload{Coverage: "Inspected impact."}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Accepted || !hasPhaseRejection(result.Rejections, "communication_backend_mismatch") {
		t.Fatalf("backend changed without rejection: %#v", result)
	}
}

func TestNoCandidateCriticModeIsRecorded(t *testing.T) {
	repo, request := prepareDiffFixture(t)
	executor := Executor{Host: "codex", Model: "sol", ContextID: "primary"}
	communication := Communication{Mode: TokenEconomyNormal, Backend: CommunicationNormal}
	submitClaimedPhase(t, repo, request, executor, communication, IntentPayload{Coverage: "Inspected behavior.", Invariants: []EvidenceStatement{{ID: "invariant-1", Summary: "Return contract inspected.", Evidence: []EvidenceRef{{File: "app.go", StartLine: 2, EndLine: 2}}}}})
	submitClaimedPhase(t, repo, request, executor, communication, ImpactPayload{Coverage: "Inspected impact.", Traces: []ImpactTrace{{ID: "trace-1", Kind: "contract", Summary: "Caller inspected.", Evidence: []EvidenceRef{{File: "app.go", StartLine: 2, EndLine: 2}}}}})
	submitClaimedPhase(t, repo, request, executor, communication, CandidatesPayload{Coverage: "No concrete defect survived.", Candidates: []Candidate{}})
	submitClaimedPhase(t, repo, request, executor, communication, CritiquePayload{CriticMode: CriticNotRequired, Verdicts: []CritiqueVerdict{}})
	workflow, err := LoadWorkflow(repo, request.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if workflow.CriticMode != CriticNotRequired {
		t.Fatalf("critic mode = %q", workflow.CriticMode)
	}
}

func TestPhaseDraftsAndPromptsCoverEveryBarrier(t *testing.T) {
	repo, request := prepareDiffFixture(t)
	for _, phase := range phaseOrder {
		taskID := phase + "-unit-0001"
		if phase != PhaseCritique && phase != PhaseFinalize {
			prompt, err := PhasePrompt(repo, request.SessionID, taskID)
			if err != nil {
				t.Fatalf("PhasePrompt(%s): %v", phase, err)
			}
			if strings.TrimSpace(prompt) == "" {
				t.Fatalf("PhasePrompt(%s) is empty", phase)
			}
		}
		draft, path, err := CreatePhaseDraft(repo, request.SessionID, taskID)
		if err != nil {
			t.Fatalf("CreatePhaseDraft(%s): %v", phase, err)
		}
		if draft.Phase != phase || draft.TaskID != taskID || len(draft.Payload) == 0 {
			t.Fatalf("unexpected %s draft: %#v", phase, draft)
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("phase draft %s missing: %v", phase, err)
		}
		again, againPath, err := CreatePhaseDraft(repo, request.SessionID, taskID)
		if err != nil || againPath != path || again.TaskID != taskID {
			t.Fatalf("reopen phase draft %s = %#v, %q, %v", phase, again, againPath, err)
		}
	}
	if _, _, err := CreatePhaseDraft(repo, request.SessionID, "missing-task"); err == nil {
		t.Fatal("missing phase task should fail")
	}
	if _, err := PhasePrompt(repo, request.SessionID, "missing-task"); err == nil {
		t.Fatal("missing phase prompt should fail")
	}
	if got := string(emptyPhasePayload("unknown")); got != "{}" {
		t.Fatalf("unknown phase payload = %q", got)
	}
	executor := Executor{Host: "codex", Model: "sol", ContextID: "primary"}
	communication := Communication{Mode: TokenEconomyNormal, Backend: CommunicationNormal}
	evidence := EvidenceRef{File: "app.go", StartLine: 2, EndLine: 2}
	submitClaimedPhase(t, repo, request, executor, communication, IntentPayload{Coverage: "Inspected intent.", Invariants: []EvidenceStatement{{ID: "invariant-1", Summary: "Return contract inspected.", Evidence: []EvidenceRef{evidence}}}})
	submitClaimedPhase(t, repo, request, executor, communication, ImpactPayload{Coverage: "Inspected impact.", Traces: []ImpactTrace{{ID: "trace-1", Kind: "caller", Summary: "Caller inspected.", Evidence: []EvidenceRef{evidence}}}})
	submitClaimedPhase(t, repo, request, executor, communication, CandidatesPayload{Coverage: "No defect survived.", Candidates: []Candidate{}})
	criticPrompt, err := PhasePrompt(repo, request.SessionID, PhaseCritique+"-unit-0001")
	if err != nil || !strings.Contains(criticPrompt, "Blindly challenge") {
		t.Fatalf("critic prompt = %q, %v", criticPrompt, err)
	}
	submitClaimedPhase(t, repo, request, executor, communication, CritiquePayload{CriticMode: CriticNotRequired, Verdicts: []CritiqueVerdict{}})
	finalizePrompt, err := PhasePrompt(repo, request.SessionID, PhaseFinalize+"-unit-0001")
	if err != nil || !strings.Contains(finalizePrompt, "submit or drop") {
		t.Fatalf("finalize prompt = %q, %v", finalizePrompt, err)
	}
}

func TestWorkflowValidationRejectsInconsistentTasks(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Workflow)
	}{
		{name: "duplicate task", mutate: func(workflow *Workflow) { workflow.Tasks[1].ID = workflow.Tasks[0].ID }},
		{name: "unknown phase", mutate: func(workflow *Workflow) { workflow.Tasks[0].Phase = "guess" }},
		{name: "unknown task state", mutate: func(workflow *Workflow) { workflow.Tasks[0].State = "paused" }},
		{name: "ready with unfinished tasks", mutate: func(workflow *Workflow) { workflow.State = WorkflowReady }},
		{name: "active with submitted tasks", mutate: func(workflow *Workflow) {
			for index := range workflow.Tasks {
				workflow.Tasks[index].State = PhaseSubmitted
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo, request := prepareDiffFixture(t)
			workflow, err := LoadWorkflow(repo, request.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			test.mutate(workflow)
			if err := saveWorkflow(repo, workflow); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadWorkflow(repo, request.SessionID); err == nil {
				t.Fatal("inconsistent workflow should fail")
			}
		})
	}
}

func TestStandardSessionArtifactsAndHandoff(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "app.go"), []byte("package app\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	request, err := Prepare(repo, PrepareInput{
		Mode: ModeScan, Profile: ReviewProfileStandard,
		Units: []PreparedUnit{{ID: "unit-1", Files: []PreparedFile{{Path: "app.go"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	prompt, err := HandoffPrompt(request)
	if err != nil || !strings.Contains(prompt, "acr review brief") || !strings.Contains(prompt, "standard profile") {
		t.Fatalf("standard handoff = %q, %v", prompt, err)
	}
	if _, err := HandoffPrompt(nil); err == nil {
		t.Fatal("nil handoff request should fail")
	}
	if _, err := HandoffPrompt(&Request{}); err == nil {
		t.Fatal("unidentified handoff request should fail")
	}

	draft, draftPath, err := CreateSubmissionDraft(repo, request.SessionID)
	if err != nil || draft.ProtocolVersion != ProtocolVersion {
		t.Fatalf("CreateSubmissionDraft = %#v, %q, %v", draft, draftPath, err)
	}
	if _, _, err := CreateSubmissionDraft(repo, request.SessionID); err == nil {
		t.Fatal("duplicate findings draft should fail")
	}
	result, err := Submit(repo, request.SessionID, *draft)
	if err != nil || len(result.Rejected) != 0 {
		t.Fatalf("Submit = %#v, %v", result, err)
	}
	loaded, err := LoadResult(repo, request.SessionID)
	if err != nil || loaded.SessionID != request.SessionID {
		t.Fatalf("LoadResult = %#v, %v", loaded, err)
	}
	if _, err := LoadResult(repo, "../unsafe"); err == nil {
		t.Fatal("unsafe result session should fail")
	}
	if err := os.MkdirAll(filepath.Join(repo, ".acr", "sessions", "broken"), 0o700); err != nil {
		t.Fatal(err)
	}
	sessions, err := ListSessions(repo)
	if err != nil || len(sessions) != 1 || sessions[0].State != "validated" {
		t.Fatalf("ListSessions = %#v, %v", sessions, err)
	}
	empty, err := ListSessions(t.TempDir())
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty ListSessions = %#v, %v", empty, err)
	}
}

func TestPhaseQueueUsesBarriersAndExpiredClaimsResume(t *testing.T) {
	repo, request := prepareDiffFixture(t)
	now := time.Date(2026, 8, 27, 20, 0, 0, 0, time.UTC)
	first, err := ClaimNextPhase(repo, request.SessionID, "worker-1", now, time.Minute)
	if err != nil {
		t.Fatalf("ClaimNextPhase() error: %v", err)
	}
	if first.Phase != PhaseIntent || first.UnitID != "unit-0001" {
		t.Fatalf("first phase task = %#v", first)
	}
	if _, err := ClaimNextPhase(repo, request.SessionID, "worker-2", now, time.Minute); err == nil {
		t.Fatal("barrier should leave no other ready task")
	}
	resumed, err := ClaimNextPhase(repo, request.SessionID, "worker-2", now.Add(2*time.Minute), time.Minute)
	if err != nil {
		t.Fatalf("expired claim did not resume: %v", err)
	}
	if resumed.ID != first.ID || resumed.Worker != "worker-2" {
		t.Fatalf("resumed task = %#v, want %s", resumed, first.ID)
	}
}

func TestSubmitPhaseAdvancesAndIsIdempotent(t *testing.T) {
	repo, request := prepareDiffFixture(t)
	now := time.Now().UTC()
	task, err := ClaimNextPhase(repo, request.SessionID, "worker", now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	submission := PhaseSubmission{
		ProtocolVersion: ProtocolVersion, SessionID: request.SessionID, TaskID: task.ID,
		UnitID: task.UnitID, Phase: task.Phase,
		Executor:      Executor{Host: "codex", Model: "sol", ContextID: "primary"},
		Communication: Communication{Mode: TokenEconomyNormal, Backend: CommunicationNormal},
		Payload: mustPhasePayload(t, IntentPayload{
			Coverage:        "Inspected changed function and neighboring contract.",
			BehaviorChanges: []EvidenceStatement{{ID: "behavior-1", Summary: "Return value changed.", Evidence: []EvidenceRef{{File: "app.go", StartLine: 2, EndLine: 2}}}},
			Invariants:      []EvidenceStatement{{ID: "invariant-1", Summary: "Caller expects successful result.", Evidence: []EvidenceRef{{File: "app.go", StartLine: 2, EndLine: 2}}}},
		}),
	}
	result, err := SubmitPhase(repo, request.SessionID, task.ID, submission)
	if err != nil || !result.Accepted {
		t.Fatalf("SubmitPhase() = %#v, %v", result, err)
	}
	again, err := SubmitPhase(repo, request.SessionID, task.ID, submission)
	if err != nil || !again.Accepted || !again.Idempotent {
		t.Fatalf("idempotent SubmitPhase() = %#v, %v", again, err)
	}
	next, err := ClaimNextPhase(repo, request.SessionID, "worker", now, time.Minute)
	if err != nil {
		t.Fatalf("next phase unavailable: %v", err)
	}
	if next.Phase != PhaseImpact {
		t.Fatalf("next phase = %q, want impact", next.Phase)
	}
}

func TestIntentPhaseRejectsUnsafeOrStaleEvidence(t *testing.T) {
	repo, request := prepareDiffFixture(t)
	task, err := ClaimNextPhase(repo, request.SessionID, "worker", time.Now(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	submission := PhaseSubmission{
		ProtocolVersion: ProtocolVersion, SessionID: request.SessionID, TaskID: task.ID,
		UnitID: task.UnitID, Phase: task.Phase,
		Executor:      Executor{Host: "codex", Model: "sol", ContextID: "primary"},
		Communication: Communication{Mode: TokenEconomyNormal, Backend: CommunicationNormal},
		Payload: mustPhasePayload(t, IntentPayload{
			Coverage:        "Inspected source.",
			BehaviorChanges: []EvidenceStatement{{ID: "behavior-1", Summary: "Changed.", Evidence: []EvidenceRef{{File: "../secret", StartLine: 1, EndLine: 1}}}},
		}),
	}
	result, err := SubmitPhase(repo, request.SessionID, task.ID, submission)
	if err != nil {
		t.Fatal(err)
	}
	if result.Accepted || !hasPhaseRejection(result.Rejections, "unsafe_evidence_path") {
		t.Fatalf("unsafe evidence result = %#v", result)
	}
}

func TestDeepWorkflowRequiresQuestionsFreshCriticAndCandidateLineage(t *testing.T) {
	repo, request := prepareDiffFixture(t)
	primary := Executor{Host: "codex", Model: "sol", ContextID: "primary"}
	communication := Communication{Mode: TokenEconomyNormal, Backend: CommunicationNormal}

	submitClaimedPhase(t, repo, request, primary, communication, IntentPayload{
		Coverage:        "Inspected changed behavior and contract.",
		BehaviorChanges: []EvidenceStatement{{ID: "behavior-1", Summary: "Return changes from true to false.", Evidence: []EvidenceRef{{File: "app.go", StartLine: 2, EndLine: 2}}}},
		Invariants:      []EvidenceStatement{{ID: "invariant-1", Summary: "Successful path must return true.", Evidence: []EvidenceRef{{File: "app.go", StartLine: 2, EndLine: 2}}}},
	})
	submitClaimedPhase(t, repo, request, primary, communication, ImpactPayload{
		Coverage: "Traced changed function and local caller contract.",
		Traces:   []ImpactTrace{{ID: "trace-1", Kind: "contract", Summary: "Changed return reaches caller.", Evidence: []EvidenceRef{{File: "app.go", StartLine: 2, EndLine: 2}}}},
	})
	submitClaimedPhase(t, repo, request, primary, communication, CandidatesPayload{
		Coverage: "Checked changed line for reachable defects.",
		Candidates: []Candidate{{
			ID: "candidate-1", File: "app.go", StartLine: 2, EndLine: 2,
			Title: "Successful path now always fails", Trigger: "Any call to changed", Impact: "Caller receives failure",
			Evidence: []EvidenceRef{{File: "app.go", StartLine: 2, EndLine: 2}}, Confidence: 0.97,
			InvariantIDs: []string{"invariant-1"},
		}},
	})

	criticTask, err := ClaimNextPhase(repo, request.SessionID, "critic", time.Now(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	prompt, err := PhasePrompt(repo, request.SessionID, criticTask.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "Successful path now always fails") || strings.Contains(prompt, "0.97") || strings.Contains(prompt, "primary") {
		t.Fatalf("critic prompt must show claim but hide confidence and primary identity:\n%s", prompt)
	}

	sameContext := PhaseSubmission{
		ProtocolVersion: ProtocolVersion, SessionID: request.SessionID, TaskID: criticTask.ID,
		UnitID: criticTask.UnitID, Phase: criticTask.Phase, Executor: primary, Communication: communication,
		Payload: mustPhasePayload(t, CritiquePayload{CriticMode: CriticIndependent, Verdicts: []CritiqueVerdict{{
			CandidateID: "candidate-1", Verdict: CritiqueSupported, Rationale: "Failure is reachable.", Confidence: 0.95,
		}}}),
	}
	rejected, err := SubmitPhase(repo, request.SessionID, criticTask.ID, sameContext)
	if err != nil {
		t.Fatal(err)
	}
	if rejected.Accepted || !hasPhaseRejection(rejected.Rejections, "critic_context_not_fresh") {
		t.Fatalf("same context accepted as independent critic: %#v", rejected)
	}

	sameContext.Executor.ContextID = "critic-fresh"
	accepted, err := SubmitPhase(repo, request.SessionID, criticTask.ID, sameContext)
	if err != nil || !accepted.Accepted || accepted.WorkflowState != WorkflowActive {
		t.Fatalf("fresh critic result = %#v, %v", accepted, err)
	}
	submitClaimedPhase(t, repo, request, primary, communication, FinalizePayload{
		Coverage:              "Resolved every candidate after critique.",
		CandidateDispositions: []CandidateDisposition{{CandidateID: "candidate-1", Outcome: DispositionSubmit, Reason: "Critic confirmed reachability."}},
	})

	result, err := Submit(repo, request.SessionID, Submission{
		ProtocolVersion: ProtocolVersion, SessionID: request.SessionID,
		CandidateDispositions: []CandidateDisposition{{CandidateID: "candidate-1", Outcome: DispositionSubmit, Reason: "Critic confirmed reachability."}},
		Findings: []Finding{{
			CandidateID: "candidate-1", UnitID: "unit-0001", File: "app.go", StartLine: 2, EndLine: 2,
			Severity: "high", Category: "bug", Explanation: "Successful path now always fails.",
			Evidence: "Changed function returns false on every call.", Confidence: 0.97,
		}},
	})
	if err != nil || len(result.Rejected) != 0 || len(result.Findings) != 1 {
		t.Fatalf("Submit(final) = %#v, %v", result, err)
	}
	markdown, err := RenderMarkdown(*result)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(markdown, "Review assurance") || !strings.Contains(markdown, "independent") {
		t.Fatalf("report missing workflow assurance:\n%s", markdown)
	}
}

func TestCriticRejectedCandidateRequiresEvidenceBackedOverride(t *testing.T) {
	repo, request := prepareDiffFixture(t)
	primary := Executor{Host: "codex", Model: "sol", ContextID: "primary"}
	critic := Executor{Host: "codex", Model: "sol", ContextID: "critic"}
	communication := Communication{Mode: TokenEconomyNormal, Backend: CommunicationNormal}
	evidence := EvidenceRef{File: "app.go", StartLine: 2, EndLine: 2}
	submitClaimedPhase(t, repo, request, primary, communication, IntentPayload{
		Coverage:   "Inspected contract.",
		Invariants: []EvidenceStatement{{ID: "invariant-1", Summary: "Successful path returns true.", Evidence: []EvidenceRef{evidence}}},
	})
	submitClaimedPhase(t, repo, request, primary, communication, ImpactPayload{
		Coverage: "Traced caller.",
		Traces:   []ImpactTrace{{ID: "trace-1", Kind: "caller", Summary: "Caller observes return.", Evidence: []EvidenceRef{evidence}}},
	})
	submitClaimedPhase(t, repo, request, primary, communication, CandidatesPayload{
		Coverage: "Inspected changed return.",
		Candidates: []Candidate{{
			ID: "candidate-1", File: "app.go", StartLine: 2, EndLine: 2,
			Title: "Return contract changed", Trigger: "Call function", Impact: "Caller sees failure",
			Evidence: []EvidenceRef{evidence}, Confidence: 0.9, InvariantIDs: []string{"invariant-1"},
		}},
	})
	submitClaimedPhase(t, repo, request, critic, communication, CritiquePayload{
		CriticMode: CriticIndependent,
		Verdicts:   []CritiqueVerdict{{CandidateID: "candidate-1", Verdict: CritiqueUnsupported, Rationale: "Intent may allow failure.", Confidence: 0.8, Evidence: []EvidenceRef{evidence}}},
	})

	task, err := ClaimNextPhase(repo, request.SessionID, "primary", time.Now(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	withoutOverride := PhaseSubmission{
		ProtocolVersion: ProtocolVersion, SessionID: request.SessionID, TaskID: task.ID, UnitID: task.UnitID, Phase: task.Phase,
		Executor: primary, Communication: communication,
		Payload: mustPhasePayload(t, FinalizePayload{Coverage: "Reconsidered candidate.", CandidateDispositions: []CandidateDisposition{{CandidateID: "candidate-1", Outcome: DispositionSubmit, Reason: "Primary still finds defect."}}}),
	}
	rejected, err := SubmitPhase(repo, request.SessionID, task.ID, withoutOverride)
	if err != nil {
		t.Fatal(err)
	}
	if rejected.Accepted || !hasPhaseRejection(rejected.Rejections, "critic_override_required") {
		t.Fatalf("unsupported candidate bypassed override: %#v", rejected)
	}
	disposition := CandidateDisposition{
		CandidateID: "candidate-1", Outcome: DispositionSubmit, Reason: "Primary verified caller contract.",
		OverrideReason: "Caller contract proves the failure is unintended.", AdditionalEvidence: []EvidenceRef{evidence},
	}
	withoutOverride.Payload = mustPhasePayload(t, FinalizePayload{Coverage: "Reconsidered candidate with caller evidence.", CandidateDispositions: []CandidateDisposition{disposition}})
	accepted, err := SubmitPhase(repo, request.SessionID, task.ID, withoutOverride)
	if err != nil || !accepted.Accepted {
		t.Fatalf("evidence-backed override = %#v, %v", accepted, err)
	}
	result, err := Submit(repo, request.SessionID, Submission{
		ProtocolVersion: ProtocolVersion, SessionID: request.SessionID,
		CandidateDispositions: []CandidateDisposition{disposition},
		Findings:              []Finding{{CandidateID: "candidate-1", UnitID: "unit-0001", File: "app.go", StartLine: 2, EndLine: 2, Severity: "high", Category: "bug", Explanation: "Return contract changed.", Evidence: "Caller requires success.", Confidence: 0.9}},
	})
	if err != nil || len(result.Rejected) != 0 || result.Assurance == nil || result.Assurance.Overrides != 1 {
		t.Fatalf("final override result = %#v, %v", result, err)
	}
}

func TestMalformedPhaseSubmissionReturnsRepairableRejections(t *testing.T) {
	repo, request := prepareDiffFixture(t)
	task, err := ClaimNextPhase(repo, request.SessionID, "worker", time.Now(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	result, err := SubmitPhase(repo, request.SessionID, task.ID, PhaseSubmission{
		ProtocolVersion: ProtocolVersion, SessionID: request.SessionID, TaskID: task.ID,
		UnitID: task.UnitID, Phase: task.Phase, Payload: json.RawMessage(`{"coverage":`),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{"missing_executor", "communication_backend_invalid", "malformed_phase_payload"} {
		if !hasPhaseRejection(result.Rejections, code) {
			t.Fatalf("missing %s rejection: %#v", code, result)
		}
	}
}

func TestPhasePayloadValidationRejectsIncompleteArtifacts(t *testing.T) {
	repo, request := prepareDiffFixture(t)
	workflow, err := LoadWorkflow(repo, request.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	for _, phase := range append(append([]string{}, phaseOrder...), "unknown") {
		task := &PhaseTask{Phase: phase, UnitID: "unit-0001"}
		_, rejections := validatePhasePayload(request, workflow, task, json.RawMessage(`{"broken":`))
		want := "malformed_phase_payload"
		if phase == "unknown" {
			want = "unsupported_phase"
		}
		if !hasPhaseRejection(rejections, want) {
			t.Fatalf("%s malformed payload missing %s: %#v", phase, want, rejections)
		}
	}

	_, intentRejections := validatePhasePayload(request, workflow, &PhaseTask{Phase: PhaseIntent, UnitID: "unit-0001"}, mustPhasePayload(t, IntentPayload{
		BehaviorChanges: []EvidenceStatement{{ID: "", Summary: "", Evidence: []EvidenceRef{}}},
	}))
	for _, code := range []string{"missing_coverage", "invalid_statement_id", "missing_statement_evidence"} {
		if !hasPhaseRejection(intentRejections, code) {
			t.Errorf("intent missing %s: %#v", code, intentRejections)
		}
	}

	_, impactRejections := validatePhasePayload(request, workflow, &PhaseTask{Phase: PhaseImpact, UnitID: "unit-0001"}, mustPhasePayload(t, ImpactPayload{
		Traces:    []ImpactTrace{{ID: "trace", Summary: "Inspected.", Evidence: []EvidenceRef{{File: "app.go", StartLine: 2, EndLine: 2}}}},
		Questions: []InvestigatedQuestion{{QuestionID: "unknown"}},
	}))
	for _, code := range []string{"missing_coverage", "missing_trace_kind", "invalid_question_investigation"} {
		if !hasPhaseRejection(impactRejections, code) {
			t.Errorf("impact missing %s: %#v", code, impactRejections)
		}
	}

	_, candidateRejections := validatePhasePayload(request, workflow, &PhaseTask{Phase: PhaseCandidates, UnitID: "unit-0001"}, mustPhasePayload(t, CandidatesPayload{
		Candidates: []Candidate{{ID: "", File: "missing.go", StartLine: 0, EndLine: 0, Confidence: 2, InvariantIDs: []string{"unknown"}, QuestionIDs: []string{"unknown"}}},
	}))
	for _, code := range []string{"missing_coverage", "invalid_candidate_id", "incomplete_candidate", "invalid_candidate_confidence", "candidate_target_invalid", "unknown_invariant_id", "unknown_question_id"} {
		if !hasPhaseRejection(candidateRejections, code) {
			t.Errorf("candidate missing %s: %#v", code, candidateRejections)
		}
	}
}

func TestQuestionResolutionValidationCoversInvalidOutcomes(t *testing.T) {
	index := 0
	request := &Request{ReviewQuestions: []ReviewQuestion{{ID: "question-1", UnitID: "unit-1", File: "app.go", Subject: "contract", Question: "Is it preserved?"}}}
	tests := []struct {
		name       string
		submission Submission
		code       string
	}{
		{name: "unknown", submission: Submission{QuestionResolutions: []QuestionResolution{{QuestionID: "unknown"}}}, code: "unknown_question"},
		{name: "duplicate", submission: Submission{QuestionResolutions: []QuestionResolution{{QuestionID: "question-1", Outcome: "no_finding", Evidence: "checked"}, {QuestionID: "question-1", Outcome: "no_finding", Evidence: "checked"}}}, code: "duplicate_question_resolution"},
		{name: "missing evidence", submission: Submission{QuestionResolutions: []QuestionResolution{{QuestionID: "question-1", Outcome: "no_finding"}}}, code: "missing_question_evidence"},
		{name: "unexpected finding", submission: Submission{QuestionResolutions: []QuestionResolution{{QuestionID: "question-1", Outcome: "no_finding", Evidence: "checked", FindingIndex: &index}}}, code: "unexpected_finding_index"},
		{name: "invalid finding", submission: Submission{QuestionResolutions: []QuestionResolution{{QuestionID: "question-1", Outcome: "finding", Evidence: "checked"}}}, code: "invalid_question_finding"},
		{name: "mismatched finding", submission: Submission{QuestionResolutions: []QuestionResolution{{QuestionID: "question-1", Outcome: "finding", Evidence: "checked", FindingIndex: &index}}, Findings: []Finding{{UnitID: "other", File: "other.go"}}}, code: "question_finding_mismatch"},
		{name: "invalid outcome", submission: Submission{QuestionResolutions: []QuestionResolution{{QuestionID: "question-1", Outcome: "maybe", Evidence: "checked"}}}, code: "invalid_question_outcome"},
		{name: "missing resolution", submission: Submission{}, code: "missing_question_resolution"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if rejections := validateQuestionResolutions(request, test.submission); !hasRejectionCode(rejections, test.code) {
				t.Fatalf("missing %s: %#v", test.code, rejections)
			}
		})
	}
	rejected := validateResolutionFindingIndexes(Submission{QuestionResolutions: []QuestionResolution{{QuestionID: "question-1", Outcome: "finding", FindingIndex: &index}}}, map[int]bool{})
	if !hasRejectionCode(rejected, "question_finding_rejected") {
		t.Fatalf("rejected finding index was accepted: %#v", rejected)
	}
}

func TestDeepFinalSubmissionRejectsIncompletePhases(t *testing.T) {
	repo, request := prepareDiffFixture(t)
	result, err := Submit(repo, request.SessionID, Submission{
		ProtocolVersion: ProtocolVersion, SessionID: request.SessionID,
		Findings: []Finding{{UnitID: "unit-0001", File: "app.go", StartLine: 2, EndLine: 2, Severity: "high", Category: "bug", Explanation: "Valid-looking but premature.", Evidence: "Changed line.", Confidence: 0.9}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasRejectionCode(result.Rejected, "phase_incomplete") {
		t.Fatalf("incomplete deep workflow accepted: %#v", result)
	}
	if len(result.Findings) != 0 || result.Summary.Accepted != 0 {
		t.Fatalf("incomplete workflow leaked accepted findings: %#v", result)
	}
}

func TestCandidateMustLinkKnownInvariantAndQuestionIDs(t *testing.T) {
	repo, request := prepareDiffFixture(t)
	primary := Executor{Host: "codex", Model: "sol", ContextID: "primary"}
	communication := Communication{Mode: TokenEconomyNormal, Backend: CommunicationNormal}
	submitClaimedPhase(t, repo, request, primary, communication, IntentPayload{
		Coverage:   "Inspected contract.",
		Invariants: []EvidenceStatement{{ID: "invariant-known", Summary: "Return preserves success.", Evidence: []EvidenceRef{{File: "app.go", StartLine: 2, EndLine: 2}}}},
	})
	submitClaimedPhase(t, repo, request, primary, communication, ImpactPayload{
		Coverage: "Inspected local impact.",
		Traces:   []ImpactTrace{{ID: "trace-1", Kind: "caller", Summary: "Return reaches caller.", Evidence: []EvidenceRef{{File: "app.go", StartLine: 2, EndLine: 2}}}},
	})
	task, err := ClaimNextPhase(repo, request.SessionID, "worker", time.Now(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	result, err := SubmitPhase(repo, request.SessionID, task.ID, PhaseSubmission{
		ProtocolVersion: ProtocolVersion, SessionID: request.SessionID, TaskID: task.ID,
		UnitID: task.UnitID, Phase: task.Phase, Executor: primary, Communication: communication,
		Payload: mustPhasePayload(t, CandidatesPayload{Coverage: "Inspected changed return.", Candidates: []Candidate{{
			ID: "candidate-1", File: "app.go", StartLine: 2, EndLine: 2, Title: "Bad return",
			Trigger: "Call changed function", Impact: "Failure", Confidence: 0.9,
			Evidence: []EvidenceRef{{File: "app.go", StartLine: 2, EndLine: 2}}, InvariantIDs: []string{"missing-invariant"},
		}}}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Accepted || !hasPhaseRejection(result.Rejections, "unknown_invariant_id") {
		t.Fatalf("unknown lineage accepted: %#v", result)
	}
}

func TestImpactRequiresEveryUnitRiskQuestion(t *testing.T) {
	repo, request := prepareRiskFixture(t)
	primary := Executor{Host: "codex", Model: "sol", ContextID: "primary"}
	communication := Communication{Mode: TokenEconomyNormal, Backend: CommunicationNormal}
	submitClaimedPhase(t, repo, request, primary, communication, IntentPayload{
		Coverage:        "Inspected boot behavior.",
		BehaviorChanges: []EvidenceStatement{{ID: "behavior-1", Summary: "Initialization order changed.", Evidence: []EvidenceRef{{File: "boot.js", StartLine: 2, EndLine: 4}}}},
	})
	task, err := ClaimNextPhase(repo, request.SessionID, "worker", time.Now(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	result, err := SubmitPhase(repo, request.SessionID, task.ID, PhaseSubmission{
		ProtocolVersion: ProtocolVersion, SessionID: request.SessionID, TaskID: task.ID,
		UnitID: task.UnitID, Phase: task.Phase, Executor: primary, Communication: communication,
		Payload: mustPhasePayload(t, ImpactPayload{Coverage: "Inspected changed boot path."}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Accepted || !hasPhaseRejection(result.Rejections, "missing_question_investigation") {
		t.Fatalf("unresolved deterministic questions accepted: %#v", result)
	}
}

func submitClaimedPhase(t *testing.T, repo string, request *Request, executor Executor, communication Communication, payload any) {
	t.Helper()
	task, err := ClaimNextPhase(repo, request.SessionID, "worker", time.Now(), time.Minute)
	if err != nil {
		t.Fatalf("claim phase: %v", err)
	}
	result, err := SubmitPhase(repo, request.SessionID, task.ID, PhaseSubmission{
		ProtocolVersion: ProtocolVersion, SessionID: request.SessionID, TaskID: task.ID,
		UnitID: task.UnitID, Phase: task.Phase, Executor: executor, Communication: communication,
		Payload: mustPhasePayload(t, payload),
	})
	if err != nil || !result.Accepted {
		t.Fatalf("submit %s: %#v, %v", task.Phase, result, err)
	}
}

func mustPhasePayload(t *testing.T, value any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func hasPhaseRejection(rejections []PhaseRejection, code string) bool {
	for _, rejection := range rejections {
		if rejection.Code == code {
			return true
		}
	}
	return false
}
