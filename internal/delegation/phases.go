// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package delegation

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	PhaseIntent     = "intent"
	PhaseImpact     = "impact"
	PhaseCandidates = "candidates"
	PhaseCritique   = "critique"
	PhaseFinalize   = "finalize"
	PhaseAnalysis   = "analysis"
	PhaseResolve    = "resolve"

	PhaseQueued    = "queued"
	PhaseClaimed   = "claimed"
	PhaseSubmitted = "submitted"

	WorkflowActive   = "active"
	WorkflowReady    = "ready"
	WorkflowComplete = "complete"

	CriticIndependent   = "independent"
	CriticSameContext   = "same_context"
	CriticNotRequired   = "not_required"
	CritiqueSupported   = "supported"
	CritiqueUnsupported = "unsupported"
	CritiqueRevise      = "revise"
	DispositionSubmit   = "submit"
	DispositionDrop     = "drop"
	RiskLow             = "low"
	RiskHigh            = "high"
)

var phaseOrder = []string{PhaseIntent, PhaseImpact, PhaseCandidates, PhaseCritique, PhaseFinalize}

type Workflow struct {
	ProtocolVersion      string                      `json:"protocol_version"`
	SessionID            string                      `json:"session_id"`
	State                string                      `json:"state"`
	Tasks                []PhaseTask                 `json:"tasks"`
	PrimaryExecutor      *Executor                   `json:"primary_executor,omitempty"`
	CriticMode           string                      `json:"critic_mode,omitempty"`
	CommunicationBackend string                      `json:"communication_backend,omitempty"`
	Evidence             map[string]EvidenceSnapshot `json:"evidence,omitempty"`
	BatchCalls           int                         `json:"batch_calls,omitempty"`
	ValidationRejections int                         `json:"validation_rejections,omitempty"`
}

type PhaseTask struct {
	ID             string    `json:"id"`
	UnitID         string    `json:"unit_id"`
	Phase          string    `json:"phase"`
	State          string    `json:"state"`
	Worker         string    `json:"worker,omitempty"`
	ClaimedAt      time.Time `json:"claimed_at,omitempty"`
	LeaseExpiresAt time.Time `json:"lease_expires_at,omitempty"`
	SubmittedAt    time.Time `json:"submitted_at,omitempty"`
	DurationMS     int64     `json:"claim_to_submit_ms,omitempty"`
	SubmissionPath string    `json:"submission_path,omitempty"`
	SubmissionSHA  string    `json:"submission_sha256,omitempty"`
}

type EvidenceSnapshot struct {
	File      string `json:"file"`
	SHA256    string `json:"sha256"`
	LineCount int    `json:"line_count"`
}

type EvidenceRef struct {
	File      string `json:"file"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	SHA256    string `json:"sha256,omitempty"`
}

type EvidenceStatement struct {
	ID       string        `json:"id"`
	Summary  string        `json:"summary"`
	Evidence []EvidenceRef `json:"evidence"`
}

type IntentPayload struct {
	Coverage        string              `json:"coverage"`
	BehaviorChanges []EvidenceStatement `json:"behavior_changes"`
	Invariants      []EvidenceStatement `json:"invariants"`
}

type ImpactTrace struct {
	ID       string        `json:"id"`
	Kind     string        `json:"kind"`
	Summary  string        `json:"summary"`
	Evidence []EvidenceRef `json:"evidence"`
}

type InvestigatedQuestion struct {
	QuestionID string        `json:"question_id"`
	Conclusion string        `json:"conclusion"`
	Evidence   []EvidenceRef `json:"evidence"`
}

type ImpactPayload struct {
	Coverage  string                 `json:"coverage"`
	Traces    []ImpactTrace          `json:"traces"`
	Questions []InvestigatedQuestion `json:"questions,omitempty"`
}

type RiskAssessment struct {
	Level     string `json:"level"`
	Rationale string `json:"rationale"`
}

type AdaptiveAnalysisPayload struct {
	Coverage        string                 `json:"coverage"`
	Risk            RiskAssessment         `json:"risk"`
	BehaviorChanges []EvidenceStatement    `json:"behavior_changes"`
	Invariants      []EvidenceStatement    `json:"invariants"`
	Traces          []ImpactTrace          `json:"traces"`
	Questions       []InvestigatedQuestion `json:"questions,omitempty"`
	Candidates      []Candidate            `json:"candidates"`
}

type Candidate struct {
	ID           string        `json:"id"`
	File         string        `json:"file"`
	StartLine    int           `json:"start_line"`
	EndLine      int           `json:"end_line"`
	Title        string        `json:"title"`
	Trigger      string        `json:"trigger"`
	Impact       string        `json:"impact"`
	Severity     string        `json:"severity,omitempty"`
	Category     string        `json:"category,omitempty"`
	Explanation  string        `json:"explanation,omitempty"`
	SuggestedFix string        `json:"suggested_fix,omitempty"`
	Evidence     []EvidenceRef `json:"evidence"`
	Confidence   float64       `json:"confidence"`
	InvariantIDs []string      `json:"invariant_ids,omitempty"`
	QuestionIDs  []string      `json:"question_ids,omitempty"`
}

type CandidatesPayload struct {
	Coverage   string      `json:"coverage"`
	Candidates []Candidate `json:"candidates"`
}

type CritiqueVerdict struct {
	CandidateID string        `json:"candidate_id"`
	Verdict     string        `json:"verdict"`
	Rationale   string        `json:"rationale"`
	Evidence    []EvidenceRef `json:"evidence,omitempty"`
	Confidence  float64       `json:"confidence"`
	Replacement *Candidate    `json:"replacement,omitempty"`
}

type CritiquePayload struct {
	CriticMode    string            `json:"critic_mode"`
	Verdicts      []CritiqueVerdict `json:"verdicts"`
	NewCandidates []Candidate       `json:"new_candidates,omitempty"`
}

type FinalizePayload struct {
	Coverage              string                 `json:"coverage"`
	CandidateDispositions []CandidateDisposition `json:"candidate_dispositions"`
}

type ResolvePayload struct {
	Coverage              string                 `json:"coverage"`
	CandidateDispositions []CandidateDisposition `json:"candidate_dispositions"`
}

type PhaseSubmission struct {
	ProtocolVersion string          `json:"protocol_version"`
	SessionID       string          `json:"session_id"`
	TaskID          string          `json:"task_id"`
	UnitID          string          `json:"unit_id"`
	Phase           string          `json:"phase"`
	Executor        Executor        `json:"executor"`
	Communication   Communication   `json:"communication"`
	Payload         json.RawMessage `json:"payload"`
}

type PhaseRejection struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type PhaseSubmitResult struct {
	ProtocolVersion string           `json:"protocol_version"`
	SessionID       string           `json:"session_id"`
	TaskID          string           `json:"task_id"`
	Accepted        bool             `json:"accepted"`
	Idempotent      bool             `json:"idempotent,omitempty"`
	WorkflowState   string           `json:"workflow_state"`
	Rejections      []PhaseRejection `json:"rejections,omitempty"`
}

type PhaseBatchSubmission struct {
	ProtocolVersion string            `json:"protocol_version"`
	SessionID       string            `json:"session_id"`
	Submissions     []PhaseSubmission `json:"submissions"`
}

type PhaseBatchSubmitResult struct {
	ProtocolVersion string              `json:"protocol_version"`
	SessionID       string              `json:"session_id"`
	Results         []PhaseSubmitResult `json:"results"`
	WorkflowState   string              `json:"workflow_state"`
}

func newWorkflow(request *Request) *Workflow {
	workflow := &Workflow{
		ProtocolVersion: request.ProtocolVersion,
		SessionID:       request.SessionID,
		State:           WorkflowReady,
		Evidence:        map[string]EvidenceSnapshot{},
	}
	if request.Instructions.ReviewProfile != ReviewProfileDeep {
		if request.Instructions.ReviewProfile == ReviewProfileAdaptive {
			workflow.State = WorkflowActive
			for _, unit := range request.Units {
				workflow.Tasks = append(workflow.Tasks, PhaseTask{
					ID: PhaseAnalysis + "-" + unit.ID, UnitID: unit.ID, Phase: PhaseAnalysis, State: PhaseQueued,
				})
			}
		}
		return workflow
	}
	workflow.State = WorkflowActive
	for _, phase := range phaseOrder {
		for _, unit := range request.Units {
			workflow.Tasks = append(workflow.Tasks, PhaseTask{
				ID: phase + "-" + unit.ID, UnitID: unit.ID, Phase: phase, State: PhaseQueued,
			})
		}
	}
	return workflow
}

func LoadWorkflow(repo, sessionID string) (*Workflow, error) {
	if !sessionIDPattern.MatchString(sessionID) {
		return nil, fmt.Errorf("invalid session id")
	}
	var workflow Workflow
	if err := readJSON(filepath.Join(SessionDir(repo, sessionID), WorkflowFileName), &workflow); err != nil {
		return nil, err
	}
	if (workflow.ProtocolVersion != ProtocolVersion && workflow.ProtocolVersion != AdaptiveProtocolVersion) || workflow.SessionID != sessionID {
		return nil, fmt.Errorf("invalid workflow protocol or session id")
	}
	if workflow.State != WorkflowActive && workflow.State != WorkflowReady && workflow.State != WorkflowComplete {
		return nil, fmt.Errorf("invalid workflow state %q", workflow.State)
	}
	seenTasks := map[string]bool{}
	allSubmitted := true
	for _, task := range workflow.Tasks {
		if task.ID == "" || task.UnitID == "" || seenTasks[task.ID] || indexOfWorkflowPhase(workflow.ProtocolVersion, task.Phase) < 0 {
			return nil, fmt.Errorf("invalid workflow task identity")
		}
		seenTasks[task.ID] = true
		if task.State != PhaseQueued && task.State != PhaseClaimed && task.State != PhaseSubmitted {
			return nil, fmt.Errorf("invalid workflow task state %q", task.State)
		}
		allSubmitted = allSubmitted && task.State == PhaseSubmitted
	}
	if (workflow.State == WorkflowReady || workflow.State == WorkflowComplete) && len(workflow.Tasks) > 0 && !allSubmitted {
		return nil, fmt.Errorf("invalid workflow state: unfinished tasks marked %s", workflow.State)
	}
	if workflow.State == WorkflowActive && len(workflow.Tasks) > 0 && allSubmitted {
		return nil, fmt.Errorf("invalid workflow state: submitted tasks remain active")
	}
	if workflow.Evidence == nil {
		workflow.Evidence = map[string]EvidenceSnapshot{}
	}
	return &workflow, nil
}

func indexOfWorkflowPhase(protocolVersion, phase string) int {
	if protocolVersion == AdaptiveProtocolVersion {
		for index, candidate := range []string{PhaseAnalysis, PhaseCritique, PhaseResolve} {
			if candidate == phase {
				return index
			}
		}
		return -1
	}
	return indexOfPhase(phase)
}

func saveWorkflow(repo string, workflow *Workflow) error {
	return writeJSON(filepath.Join(SessionDir(repo, workflow.SessionID), WorkflowFileName), workflow)
}

func ClaimNextPhase(repo, sessionID, worker string, now time.Time, lease time.Duration) (*PhaseTask, error) {
	if strings.TrimSpace(worker) == "" || lease <= 0 {
		return nil, fmt.Errorf("worker and positive lease are required")
	}
	var claimed *PhaseTask
	err := withWorkflowLock(repo, sessionID, func() error {
		workflow, err := LoadWorkflow(repo, sessionID)
		if err != nil {
			return err
		}
		if workflow.State != WorkflowActive {
			return fmt.Errorf("no phase tasks are ready")
		}
		requeueExpiredPhaseClaims(workflow.Tasks, now)
		for i := range workflow.Tasks {
			task := &workflow.Tasks[i]
			if task.State != PhaseQueued || !workflowPhaseBarrierComplete(workflow, task.Phase) {
				continue
			}
			task.State = PhaseClaimed
			task.Worker = worker
			task.ClaimedAt = now.UTC()
			task.LeaseExpiresAt = now.Add(lease).UTC()
			copy := *task
			claimed = &copy
			return saveWorkflow(repo, workflow)
		}
		return fmt.Errorf("no phase tasks are ready")
	})
	return claimed, err
}

// ClaimReadyPhases atomically claims every ready task in the earliest workflow
// stage so one host-model turn can cover the whole batch.
func ClaimReadyPhases(repo, sessionID, worker string, now time.Time, lease time.Duration) ([]PhaseTask, error) {
	if strings.TrimSpace(worker) == "" || lease <= 0 {
		return nil, fmt.Errorf("worker and positive lease are required")
	}
	claimed := make([]PhaseTask, 0)
	err := withWorkflowLock(repo, sessionID, func() error {
		workflow, err := LoadWorkflow(repo, sessionID)
		if err != nil {
			return err
		}
		if workflow.State != WorkflowActive {
			return fmt.Errorf("no phase tasks are ready")
		}
		requeueExpiredPhaseClaims(workflow.Tasks, now)
		phase := ""
		for i := range workflow.Tasks {
			task := &workflow.Tasks[i]
			if task.State == PhaseQueued && workflowPhaseBarrierComplete(workflow, task.Phase) {
				phase = task.Phase
				break
			}
		}
		if phase == "" {
			return fmt.Errorf("no phase tasks are ready")
		}
		for i := range workflow.Tasks {
			task := &workflow.Tasks[i]
			if task.State != PhaseQueued || task.Phase != phase || !workflowPhaseBarrierComplete(workflow, task.Phase) {
				continue
			}
			task.State = PhaseClaimed
			task.Worker = worker
			task.ClaimedAt = now.UTC()
			task.LeaseExpiresAt = now.Add(lease).UTC()
			claimed = append(claimed, *task)
		}
		return saveWorkflow(repo, workflow)
	})
	return claimed, err
}

func requeueExpiredPhaseClaims(tasks []PhaseTask, now time.Time) {
	for i := range tasks {
		task := &tasks[i]
		if task.State == PhaseClaimed && !task.LeaseExpiresAt.After(now) {
			task.State = PhaseQueued
			task.Worker = ""
			task.ClaimedAt = time.Time{}
			task.LeaseExpiresAt = time.Time{}
		}
	}
}

func workflowPhaseBarrierComplete(workflow *Workflow, phase string) bool {
	if workflow.ProtocolVersion == AdaptiveProtocolVersion {
		phaseIndex := indexOfWorkflowPhase(workflow.ProtocolVersion, phase)
		if phaseIndex <= 0 {
			return true
		}
		for _, task := range workflow.Tasks {
			if indexOfWorkflowPhase(workflow.ProtocolVersion, task.Phase) < phaseIndex && task.State != PhaseSubmitted {
				return false
			}
		}
		return true
	}
	return phaseBarrierComplete(workflow.Tasks, phase)
}

func phaseBarrierComplete(tasks []PhaseTask, phase string) bool {
	phaseIndex := indexOfPhase(phase)
	if phaseIndex <= 0 {
		return true
	}
	previous := phaseOrder[phaseIndex-1]
	for _, task := range tasks {
		if task.Phase == previous && task.State != PhaseSubmitted {
			return false
		}
	}
	return true
}

func indexOfPhase(phase string) int {
	for i, candidate := range phaseOrder {
		if candidate == phase {
			return i
		}
	}
	return -1
}

func SubmitPhase(repo, sessionID, taskID string, submission PhaseSubmission) (*PhaseSubmitResult, error) {
	return submitPhaseAt(repo, sessionID, taskID, submission, time.Now().UTC())
}

func submitPhaseAt(repo, sessionID, taskID string, submission PhaseSubmission, submittedAt time.Time) (*PhaseSubmitResult, error) {
	result := &PhaseSubmitResult{ProtocolVersion: submission.ProtocolVersion, SessionID: sessionID, TaskID: taskID}
	err := withWorkflowLock(repo, sessionID, func() error {
		request, err := LoadRequest(repo, sessionID)
		if err != nil {
			return err
		}
		workflow, err := LoadWorkflow(repo, sessionID)
		if err != nil {
			return err
		}
		index := findPhaseTask(workflow.Tasks, taskID)
		if index < 0 {
			return fmt.Errorf("phase task %q not found", taskID)
		}
		task := &workflow.Tasks[index]
		result.WorkflowState = workflow.State
		result.ProtocolVersion = request.ProtocolVersion
		if submission.ProtocolVersion != request.ProtocolVersion || submission.SessionID != sessionID || submission.TaskID != taskID || submission.UnitID != task.UnitID || submission.Phase != task.Phase {
			result.Rejections = append(result.Rejections, PhaseRejection{Code: "identity_mismatch", Message: "submission protocol, session, task, unit, or phase does not match"})
			if workflow.ProtocolVersion == AdaptiveProtocolVersion {
				workflow.ValidationRejections++
				return saveWorkflow(repo, workflow)
			}
			return nil
		}
		result.Rejections = append(result.Rejections, validatePhaseExecutor(request, workflow, task, submission)...)
		normalized, rejections := validatePhasePayload(request, workflow, task, submission.Payload)
		result.Rejections = append(result.Rejections, rejections...)
		if len(result.Rejections) > 0 {
			if workflow.ProtocolVersion == AdaptiveProtocolVersion {
				workflow.ValidationRejections += len(result.Rejections)
				return saveWorkflow(repo, workflow)
			}
			return nil
		}
		submission.Payload = normalized
		digest := phaseSubmissionDigest(submission)
		if task.State == PhaseSubmitted {
			if task.SubmissionSHA == digest {
				result.Accepted = true
				result.Idempotent = true
				return nil
			}
			result.Rejections = append(result.Rejections, PhaseRejection{Code: "conflicting_submission", Message: "task already has a different submission"})
			return nil
		}
		data, err := json.MarshalIndent(submission, "", "  ")
		if err != nil {
			return fmt.Errorf("encode phase submission: %w", err)
		}
		data = append(data, '\n')
		path := filepath.Join(SessionDir(repo, sessionID), "phases", task.Phase, task.UnitID, task.ID+".json")
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return fmt.Errorf("create phase directory: %w", err)
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			return fmt.Errorf("write phase submission: %w", err)
		}
		task.State = PhaseSubmitted
		task.SubmittedAt = submittedAt.UTC()
		if !task.ClaimedAt.IsZero() && submittedAt.After(task.ClaimedAt) {
			task.DurationMS = submittedAt.Sub(task.ClaimedAt).Milliseconds()
		}
		task.Worker = ""
		task.LeaseExpiresAt = time.Time{}
		task.SubmissionPath = path
		task.SubmissionSHA = digest
		if workflow.CommunicationBackend == "" {
			workflow.CommunicationBackend = submission.Communication.Backend
		}
		if workflow.PrimaryExecutor == nil && task.Phase != PhaseCritique {
			copy := submission.Executor
			workflow.PrimaryExecutor = &copy
		}
		if task.Phase == PhaseCritique {
			var critique CritiquePayload
			if err := json.Unmarshal(normalized, &critique); err == nil {
				switch critique.CriticMode {
				case CriticSameContext:
					workflow.CriticMode = CriticSameContext
				case CriticIndependent:
					if workflow.CriticMode == "" || workflow.CriticMode == CriticNotRequired {
						workflow.CriticMode = CriticIndependent
					}
				case CriticNotRequired:
					if workflow.CriticMode == "" {
						workflow.CriticMode = CriticNotRequired
					}
				}
			}
		}
		if workflow.ProtocolVersion == AdaptiveProtocolVersion {
			if err := advanceAdaptiveWorkflow(request, workflow); err != nil {
				return err
			}
		} else if allPhaseTasksSubmitted(workflow.Tasks) {
			workflow.State = WorkflowReady
		}
		result.Accepted = true
		result.WorkflowState = workflow.State
		return saveWorkflow(repo, workflow)
	})
	return result, err
}

func SubmitPhaseBatch(repo, sessionID string, batch PhaseBatchSubmission) (*PhaseBatchSubmitResult, error) {
	return submitPhaseBatchAt(repo, sessionID, batch, time.Now().UTC())
}

func submitPhaseBatchAt(repo, sessionID string, batch PhaseBatchSubmission, submittedAt time.Time) (*PhaseBatchSubmitResult, error) {
	request, err := LoadRequest(repo, sessionID)
	if err != nil {
		return nil, err
	}
	if batch.ProtocolVersion != request.ProtocolVersion || batch.SessionID != sessionID {
		return nil, fmt.Errorf("batch protocol or session does not match")
	}
	result := &PhaseBatchSubmitResult{
		ProtocolVersion: request.ProtocolVersion,
		SessionID:       sessionID,
		Results:         make([]PhaseSubmitResult, 0, len(batch.Submissions)),
	}
	for _, submission := range batch.Submissions {
		item, submitErr := submitPhaseAt(repo, sessionID, submission.TaskID, submission, submittedAt)
		if submitErr != nil {
			return nil, submitErr
		}
		result.Results = append(result.Results, *item)
	}
	err = withWorkflowLock(repo, sessionID, func() error {
		workflow, loadErr := LoadWorkflow(repo, sessionID)
		if loadErr != nil {
			return loadErr
		}
		workflow.BatchCalls++
		result.WorkflowState = workflow.State
		return saveWorkflow(repo, workflow)
	})
	return result, err
}

func advanceAdaptiveWorkflow(request *Request, workflow *Workflow) error {
	if !allTasksOfPhaseSubmitted(workflow.Tasks, PhaseAnalysis) {
		return nil
	}
	if !hasTasksOfPhase(workflow.Tasks, PhaseCritique) {
		for _, unit := range request.Units {
			analysis, err := analysisForUnit(workflow, unit.ID)
			if err != nil {
				return err
			}
			if len(analysis.Candidates) == 0 && len(analysis.Questions) == 0 && analysis.Risk.Level == RiskLow {
				continue
			}
			workflow.Tasks = append(workflow.Tasks, PhaseTask{
				ID: PhaseCritique + "-" + unit.ID, UnitID: unit.ID, Phase: PhaseCritique, State: PhaseQueued,
			})
		}
		if !hasTasksOfPhase(workflow.Tasks, PhaseCritique) {
			workflow.State = WorkflowReady
		}
		return nil
	}
	if allTasksOfPhaseSubmitted(workflow.Tasks, PhaseCritique) && !hasTasksOfPhase(workflow.Tasks, PhaseResolve) {
		for _, task := range workflow.Tasks {
			if task.Phase != PhaseCritique {
				continue
			}
			needsResolution, err := critiqueNeedsResolution(task)
			if err != nil {
				return err
			}
			if needsResolution {
				workflow.Tasks = append(workflow.Tasks, PhaseTask{
					ID: PhaseResolve + "-" + task.UnitID, UnitID: task.UnitID, Phase: PhaseResolve, State: PhaseQueued,
				})
			}
		}
		if !hasTasksOfPhase(workflow.Tasks, PhaseResolve) {
			workflow.State = WorkflowReady
		}
		return nil
	}
	if hasTasksOfPhase(workflow.Tasks, PhaseResolve) && allTasksOfPhaseSubmitted(workflow.Tasks, PhaseResolve) {
		workflow.State = WorkflowReady
	}
	return nil
}

func critiqueNeedsResolution(task PhaseTask) (bool, error) {
	var submission PhaseSubmission
	if err := readJSON(task.SubmissionPath, &submission); err != nil {
		return false, err
	}
	var payload CritiquePayload
	if err := json.Unmarshal(submission.Payload, &payload); err != nil {
		return false, err
	}
	if len(payload.NewCandidates) > 0 {
		return true, nil
	}
	for _, verdict := range payload.Verdicts {
		if verdict.Verdict != CritiqueSupported {
			return true, nil
		}
	}
	return false, nil
}

func hasTasksOfPhase(tasks []PhaseTask, phase string) bool {
	for _, task := range tasks {
		if task.Phase == phase {
			return true
		}
	}
	return false
}

func allTasksOfPhaseSubmitted(tasks []PhaseTask, phase string) bool {
	found := false
	for _, task := range tasks {
		if task.Phase != phase {
			continue
		}
		found = true
		if task.State != PhaseSubmitted {
			return false
		}
	}
	return found
}

func validatePhaseExecutor(request *Request, workflow *Workflow, task *PhaseTask, submission PhaseSubmission) []PhaseRejection {
	rejections := make([]PhaseRejection, 0)
	if strings.TrimSpace(submission.Executor.Host) == "" || strings.TrimSpace(submission.Executor.Model) == "" || strings.TrimSpace(submission.Executor.ContextID) == "" {
		rejections = append(rejections, PhaseRejection{Code: "missing_executor", Message: "executor host, model, and context_id are required"})
	}
	policy := request.Instructions.TokenEconomy
	communication := submission.Communication
	if communication.Mode != policy.Mode || communication.Level != policy.Level {
		rejections = append(rejections, PhaseRejection{Code: "communication_mismatch", Message: "communication mode and level must match the prepared request"})
	}
	if policy.Mode == TokenEconomyCaveman {
		if communication.Backend != CommunicationSkill && communication.Backend != CommunicationFallback {
			rejections = append(rejections, PhaseRejection{Code: "communication_backend_required", Message: "caveman mode requires skill or compact_fallback backend"})
		}
	} else if communication.Backend != CommunicationNormal {
		rejections = append(rejections, PhaseRejection{Code: "communication_backend_invalid", Message: "normal mode requires normal backend"})
	}
	if workflow.CommunicationBackend != "" && communication.Backend != workflow.CommunicationBackend {
		rejections = append(rejections, PhaseRejection{Code: "communication_backend_mismatch", Message: "all phases must use the same communication backend"})
	}
	if workflow.PrimaryExecutor != nil && task.Phase != PhaseCritique {
		if workflow.PrimaryExecutor.Host != submission.Executor.Host || workflow.PrimaryExecutor.Model != submission.Executor.Model || workflow.PrimaryExecutor.ContextID != submission.Executor.ContextID {
			rejections = append(rejections, PhaseRejection{Code: "primary_context_mismatch", Message: "primary phases must use the same host, model, and context"})
		}
	}
	if workflow.PrimaryExecutor != nil && task.Phase == PhaseCritique {
		var payload CritiquePayload
		if err := json.Unmarshal(submission.Payload, &payload); err == nil {
			if submission.Executor.Host != workflow.PrimaryExecutor.Host || submission.Executor.Model != workflow.PrimaryExecutor.Model {
				rejections = append(rejections, PhaseRejection{Code: "critic_model_mismatch", Message: "critic must use the same host and model as the primary reviewer"})
			}
			switch payload.CriticMode {
			case CriticIndependent:
				if submission.Executor.ContextID == workflow.PrimaryExecutor.ContextID {
					rejections = append(rejections, PhaseRejection{Code: "critic_context_not_fresh", Message: "independent critic requires a fresh context_id"})
				}
			case CriticSameContext:
				if submission.Executor.ContextID != workflow.PrimaryExecutor.ContextID {
					rejections = append(rejections, PhaseRejection{Code: "critic_context_mismatch", Message: "same_context critic must use the primary context_id"})
				}
			case CriticNotRequired:
			default:
				rejections = append(rejections, PhaseRejection{Code: "invalid_critic_mode", Message: "critic_mode must be independent, same_context, or not_required"})
			}
		}
	}
	return rejections
}

func validatePhasePayload(request *Request, workflow *Workflow, task *PhaseTask, raw json.RawMessage) (json.RawMessage, []PhaseRejection) {
	switch task.Phase {
	case PhaseAnalysis:
		var payload AdaptiveAnalysisPayload
		if err := decodeStrictJSON(raw, &payload); err != nil {
			return raw, []PhaseRejection{{Code: "malformed_phase_payload", Message: err.Error()}}
		}
		rejections := make([]PhaseRejection, 0)
		if strings.TrimSpace(payload.Coverage) == "" {
			rejections = append(rejections, PhaseRejection{Code: "missing_coverage", Message: "analysis requires coverage evidence"})
		}
		if payload.Risk.Level != RiskLow && payload.Risk.Level != RiskHigh {
			rejections = append(rejections, PhaseRejection{Code: "invalid_risk", Message: "analysis risk must be low or high"})
		}
		if strings.TrimSpace(payload.Risk.Rationale) == "" {
			rejections = append(rejections, PhaseRejection{Code: "missing_risk_rationale", Message: "analysis risk requires a rationale"})
		}
		seenStatements := map[string]bool{}
		for i := range payload.BehaviorChanges {
			rejections = append(rejections, validateEvidenceStatement(request, workflow, &payload.BehaviorChanges[i], seenStatements)...)
		}
		for i := range payload.Invariants {
			rejections = append(rejections, validateEvidenceStatement(request, workflow, &payload.Invariants[i], seenStatements)...)
		}
		for i := range payload.Traces {
			trace := &payload.Traces[i]
			statement := EvidenceStatement{ID: trace.ID, Summary: trace.Summary, Evidence: trace.Evidence}
			rejections = append(rejections, validateEvidenceStatement(request, workflow, &statement, seenStatements)...)
			trace.ID, trace.Summary, trace.Evidence = statement.ID, statement.Summary, statement.Evidence
			if strings.TrimSpace(trace.Kind) == "" {
				rejections = append(rejections, PhaseRejection{Code: "missing_trace_kind", Message: "analysis traces require a kind"})
			}
		}
		expectedQuestions := questionsForUnit(request, task.UnitID)
		questionIDs := map[string]bool{}
		for i := range payload.Questions {
			question := &payload.Questions[i]
			if !expectedQuestions[question.QuestionID] || questionIDs[question.QuestionID] {
				rejections = append(rejections, PhaseRejection{Code: "invalid_question_investigation", Message: "question investigation references an unknown or duplicate question"})
				continue
			}
			questionIDs[question.QuestionID] = true
			if strings.TrimSpace(question.Conclusion) == "" || len(question.Evidence) == 0 {
				rejections = append(rejections, PhaseRejection{Code: "missing_question_evidence", Message: "question investigations require conclusion and evidence"})
			}
			for j := range question.Evidence {
				rejections = append(rejections, validateEvidenceRef(request, workflow, &question.Evidence[j])...)
			}
		}
		for questionID := range expectedQuestions {
			if !questionIDs[questionID] {
				rejections = append(rejections, PhaseRejection{Code: "missing_question_investigation", Message: fmt.Sprintf("analysis must investigate %s", questionID)})
			}
		}
		invariantIDs := map[string]bool{}
		for _, invariant := range payload.Invariants {
			invariantIDs[invariant.ID] = true
		}
		candidateIDs := existingCandidateIDs(workflow)
		for i := range payload.Candidates {
			rejections = append(rejections, validateCandidate(request, workflow, task.UnitID, &payload.Candidates[i], candidateIDs, invariantIDs, questionIDs, true)...)
		}
		return marshalPhasePayload(payload), rejections
	case PhaseIntent:
		var payload IntentPayload
		if err := decodeStrictJSON(raw, &payload); err != nil {
			return raw, []PhaseRejection{{Code: "malformed_phase_payload", Message: err.Error()}}
		}
		rejections := make([]PhaseRejection, 0)
		if strings.TrimSpace(payload.Coverage) == "" {
			rejections = append(rejections, PhaseRejection{Code: "missing_coverage", Message: "intent phase requires coverage evidence"})
		}
		if len(payload.BehaviorChanges)+len(payload.Invariants) == 0 {
			rejections = append(rejections, PhaseRejection{Code: "missing_intent_analysis", Message: "intent phase requires a behavior change or invariant"})
		}
		seen := map[string]bool{}
		for i := range payload.BehaviorChanges {
			rejections = append(rejections, validateEvidenceStatement(request, workflow, &payload.BehaviorChanges[i], seen)...)
		}
		for i := range payload.Invariants {
			rejections = append(rejections, validateEvidenceStatement(request, workflow, &payload.Invariants[i], seen)...)
		}
		return marshalPhasePayload(payload), rejections
	case PhaseImpact:
		var payload ImpactPayload
		if err := decodeStrictJSON(raw, &payload); err != nil {
			return raw, []PhaseRejection{{Code: "malformed_phase_payload", Message: err.Error()}}
		}
		rejections := make([]PhaseRejection, 0)
		if strings.TrimSpace(payload.Coverage) == "" {
			rejections = append(rejections, PhaseRejection{Code: "missing_coverage", Message: "impact phase requires coverage evidence"})
		}
		seen := map[string]bool{}
		for i := range payload.Traces {
			trace := &payload.Traces[i]
			statement := EvidenceStatement{ID: trace.ID, Summary: trace.Summary, Evidence: trace.Evidence}
			rejections = append(rejections, validateEvidenceStatement(request, workflow, &statement, seen)...)
			trace.ID, trace.Summary, trace.Evidence = statement.ID, statement.Summary, statement.Evidence
			if strings.TrimSpace(trace.Kind) == "" {
				rejections = append(rejections, PhaseRejection{Code: "missing_trace_kind", Message: "impact traces require a kind"})
			}
		}
		expected := questionsForUnit(request, task.UnitID)
		questionSeen := map[string]bool{}
		for i := range payload.Questions {
			question := &payload.Questions[i]
			if !expected[question.QuestionID] || questionSeen[question.QuestionID] {
				rejections = append(rejections, PhaseRejection{Code: "invalid_question_investigation", Message: "question investigation references an unknown or duplicate question"})
				continue
			}
			questionSeen[question.QuestionID] = true
			if strings.TrimSpace(question.Conclusion) == "" || len(question.Evidence) == 0 {
				rejections = append(rejections, PhaseRejection{Code: "missing_question_evidence", Message: "question investigations require conclusion and evidence"})
			}
			for j := range question.Evidence {
				rejections = append(rejections, validateEvidenceRef(request, workflow, &question.Evidence[j])...)
			}
		}
		for questionID := range expected {
			if !questionSeen[questionID] {
				rejections = append(rejections, PhaseRejection{Code: "missing_question_investigation", Message: fmt.Sprintf("impact phase must investigate %s", questionID)})
			}
		}
		return marshalPhasePayload(payload), rejections
	case PhaseCandidates:
		var payload CandidatesPayload
		if err := decodeStrictJSON(raw, &payload); err != nil {
			return raw, []PhaseRejection{{Code: "malformed_phase_payload", Message: err.Error()}}
		}
		rejections := make([]PhaseRejection, 0)
		if strings.TrimSpace(payload.Coverage) == "" {
			rejections = append(rejections, PhaseRejection{Code: "missing_coverage", Message: "candidate phase requires coverage evidence"})
		}
		seen := existingCandidateIDs(workflow)
		invariantIDs, investigatedQuestionIDs := knownLineageIDsForUnit(workflow, task.UnitID)
		for i := range payload.Candidates {
			candidate := &payload.Candidates[i]
			candidate.ID = strings.TrimSpace(candidate.ID)
			if candidate.ID == "" || seen[candidate.ID] {
				rejections = append(rejections, PhaseRejection{Code: "invalid_candidate_id", Message: "candidate IDs must be non-empty and unique"})
			} else {
				seen[candidate.ID] = true
			}
			if strings.TrimSpace(candidate.Title) == "" || strings.TrimSpace(candidate.Trigger) == "" || strings.TrimSpace(candidate.Impact) == "" || len(candidate.Evidence) == 0 {
				rejections = append(rejections, PhaseRejection{Code: "incomplete_candidate", Message: "candidates require title, trigger, impact, and evidence"})
			}
			if candidate.Confidence <= 0 || candidate.Confidence > 1 {
				rejections = append(rejections, PhaseRejection{Code: "invalid_candidate_confidence", Message: "candidate confidence must be greater than 0 and at most 1"})
			}
			unit, file := findTarget(request, task.UnitID, candidate.File)
			if unit == nil || file == nil {
				rejections = append(rejections, PhaseRejection{Code: "candidate_target_invalid", Message: "candidate must reference a file in its review unit"})
			} else if candidate.StartLine < 1 || candidate.EndLine < candidate.StartLine || candidate.EndLine > file.LineCount || (request.Mode == ModeDiff && !rangeTouchesChangedLine(file.Diff, candidate.StartLine, candidate.EndLine)) {
				rejections = append(rejections, PhaseRejection{Code: "candidate_anchor_invalid", Message: "candidate must anchor a valid changed line"})
			}
			for j := range candidate.Evidence {
				rejections = append(rejections, validateEvidenceRef(request, workflow, &candidate.Evidence[j])...)
			}
			for _, invariantID := range candidate.InvariantIDs {
				if !invariantIDs[invariantID] {
					rejections = append(rejections, PhaseRejection{Code: "unknown_invariant_id", Message: fmt.Sprintf("candidate %q references unknown invariant %q", candidate.ID, invariantID)})
				}
			}
			for _, questionID := range candidate.QuestionIDs {
				if !investigatedQuestionIDs[questionID] {
					rejections = append(rejections, PhaseRejection{Code: "unknown_question_id", Message: fmt.Sprintf("candidate %q references uninvestigated question %q", candidate.ID, questionID)})
				}
			}
		}
		return marshalPhasePayload(payload), rejections
	case PhaseCritique:
		var payload CritiquePayload
		if err := decodeStrictJSON(raw, &payload); err != nil {
			return raw, []PhaseRejection{{Code: "malformed_phase_payload", Message: err.Error()}}
		}
		if request.ProtocolVersion == AdaptiveProtocolVersion {
			return validateAdaptiveCritiquePayload(request, workflow, task, payload)
		}
		candidates, err := candidatesForUnit(workflow, task.UnitID)
		if err != nil {
			return raw, []PhaseRejection{{Code: "candidate_artifact_unreadable", Message: err.Error()}}
		}
		rejections := make([]PhaseRejection, 0)
		if len(candidates) == 0 {
			if payload.CriticMode != CriticNotRequired || len(payload.Verdicts) != 0 {
				rejections = append(rejections, PhaseRejection{Code: "critic_not_required", Message: "units without candidates require not_required mode and no verdicts"})
			}
			return marshalPhasePayload(payload), rejections
		}
		if payload.CriticMode != CriticIndependent && payload.CriticMode != CriticSameContext {
			rejections = append(rejections, PhaseRejection{Code: "invalid_critic_mode", Message: "candidate critique requires independent or same_context mode"})
		}
		expected := map[string]bool{}
		for _, candidate := range candidates {
			expected[candidate.ID] = true
		}
		seen := map[string]bool{}
		for i := range payload.Verdicts {
			verdict := &payload.Verdicts[i]
			if !expected[verdict.CandidateID] || seen[verdict.CandidateID] {
				rejections = append(rejections, PhaseRejection{Code: "invalid_critique_candidate", Message: "critique references an unknown or duplicate candidate"})
				continue
			}
			seen[verdict.CandidateID] = true
			if verdict.Verdict != CritiqueSupported && verdict.Verdict != CritiqueUnsupported && verdict.Verdict != CritiqueRevise {
				rejections = append(rejections, PhaseRejection{Code: "invalid_critique_verdict", Message: "verdict must be supported, unsupported, or revise"})
			}
			if strings.TrimSpace(verdict.Rationale) == "" || verdict.Confidence <= 0 || verdict.Confidence > 1 {
				rejections = append(rejections, PhaseRejection{Code: "incomplete_critique", Message: "critique requires rationale and confidence greater than 0 and at most 1"})
			}
			for j := range verdict.Evidence {
				rejections = append(rejections, validateEvidenceRef(request, workflow, &verdict.Evidence[j])...)
			}
		}
		for candidateID := range expected {
			if !seen[candidateID] {
				rejections = append(rejections, PhaseRejection{Code: "missing_critique", Message: fmt.Sprintf("critique must resolve %s", candidateID)})
			}
		}
		return marshalPhasePayload(payload), rejections
	case PhaseFinalize:
		var payload FinalizePayload
		if err := decodeStrictJSON(raw, &payload); err != nil {
			return raw, []PhaseRejection{{Code: "malformed_phase_payload", Message: err.Error()}}
		}
		candidates, err := candidatesForUnit(workflow, task.UnitID)
		if err != nil {
			return raw, []PhaseRejection{{Code: "candidate_artifact_unreadable", Message: err.Error()}}
		}
		verdicts, err := verdictsForUnit(workflow, task.UnitID)
		if err != nil {
			return raw, []PhaseRejection{{Code: "critique_artifact_unreadable", Message: err.Error()}}
		}
		rejections := make([]PhaseRejection, 0)
		if strings.TrimSpace(payload.Coverage) == "" {
			rejections = append(rejections, PhaseRejection{Code: "missing_coverage", Message: "finalize phase requires synthesis coverage"})
		}
		expected := map[string]bool{}
		for _, candidate := range candidates {
			expected[candidate.ID] = true
		}
		seen := map[string]bool{}
		for i := range payload.CandidateDispositions {
			disposition := &payload.CandidateDispositions[i]
			if !expected[disposition.CandidateID] || seen[disposition.CandidateID] {
				rejections = append(rejections, PhaseRejection{Code: "invalid_candidate_disposition", Message: "finalize references an unknown or duplicate candidate"})
				continue
			}
			seen[disposition.CandidateID] = true
			if disposition.Outcome != DispositionSubmit && disposition.Outcome != DispositionDrop {
				rejections = append(rejections, PhaseRejection{Code: "invalid_candidate_disposition", Message: "candidate outcome must be submit or drop"})
			}
			if strings.TrimSpace(disposition.Reason) == "" {
				rejections = append(rejections, PhaseRejection{Code: "missing_disposition_reason", Message: "every candidate disposition requires a reason"})
			}
			if disposition.Outcome == DispositionSubmit && verdicts[disposition.CandidateID].Verdict == CritiqueUnsupported && (strings.TrimSpace(disposition.OverrideReason) == "" || len(disposition.AdditionalEvidence) == 0) {
				rejections = append(rejections, PhaseRejection{Code: "critic_override_required", Message: "critic-rejected candidates require override_reason and additional_evidence"})
			}
			for j := range disposition.AdditionalEvidence {
				rejections = append(rejections, validateEvidenceRef(request, workflow, &disposition.AdditionalEvidence[j])...)
			}
		}
		for candidateID := range expected {
			if !seen[candidateID] {
				rejections = append(rejections, PhaseRejection{Code: "missing_candidate_disposition", Message: fmt.Sprintf("finalize must resolve %s", candidateID)})
			}
		}
		return marshalPhasePayload(payload), rejections
	case PhaseResolve:
		var payload ResolvePayload
		if err := decodeStrictJSON(raw, &payload); err != nil {
			return raw, []PhaseRejection{{Code: "malformed_phase_payload", Message: err.Error()}}
		}
		return validateResolvePayload(request, workflow, task, payload)
	default:
		return raw, []PhaseRejection{{Code: "unsupported_phase", Message: fmt.Sprintf("phase %q is not implemented", task.Phase)}}
	}
}

func validateResolvePayload(request *Request, workflow *Workflow, task *PhaseTask, payload ResolvePayload) (json.RawMessage, []PhaseRejection) {
	critique, err := critiqueForUnit(workflow, task.UnitID)
	if err != nil {
		return marshalPhasePayload(payload), []PhaseRejection{{Code: "critique_artifact_unreadable", Message: err.Error()}}
	}
	expected := map[string]bool{}
	unsupported := map[string]bool{}
	for _, verdict := range critique.Verdicts {
		switch verdict.Verdict {
		case CritiqueUnsupported:
			expected[verdict.CandidateID] = true
			unsupported[verdict.CandidateID] = true
		case CritiqueRevise:
			if verdict.Replacement != nil {
				expected[verdict.Replacement.ID] = true
			}
		}
	}
	for _, candidate := range critique.NewCandidates {
		expected[candidate.ID] = true
	}
	rejections := make([]PhaseRejection, 0)
	if strings.TrimSpace(payload.Coverage) == "" {
		rejections = append(rejections, PhaseRejection{Code: "missing_coverage", Message: "resolve requires synthesis coverage"})
	}
	seen := map[string]bool{}
	for i := range payload.CandidateDispositions {
		disposition := &payload.CandidateDispositions[i]
		if !expected[disposition.CandidateID] || seen[disposition.CandidateID] {
			rejections = append(rejections, PhaseRejection{Code: "invalid_candidate_disposition", Message: "resolve references an unknown or duplicate candidate"})
			continue
		}
		seen[disposition.CandidateID] = true
		if disposition.Outcome != DispositionSubmit && disposition.Outcome != DispositionDrop {
			rejections = append(rejections, PhaseRejection{Code: "invalid_candidate_disposition", Message: "candidate outcome must be submit or drop"})
		}
		if strings.TrimSpace(disposition.Reason) == "" {
			rejections = append(rejections, PhaseRejection{Code: "missing_disposition_reason", Message: "every candidate disposition requires a reason"})
		}
		if disposition.Outcome == DispositionSubmit && unsupported[disposition.CandidateID] && (strings.TrimSpace(disposition.OverrideReason) == "" || len(disposition.AdditionalEvidence) == 0) {
			rejections = append(rejections, PhaseRejection{Code: "critic_override_required", Message: "critic-rejected candidates require override_reason and additional_evidence"})
		}
		for j := range disposition.AdditionalEvidence {
			rejections = append(rejections, validateEvidenceRef(request, workflow, &disposition.AdditionalEvidence[j])...)
		}
	}
	for candidateID := range expected {
		if !seen[candidateID] {
			rejections = append(rejections, PhaseRejection{Code: "missing_candidate_disposition", Message: fmt.Sprintf("resolve must decide %s", candidateID)})
		}
	}
	return marshalPhasePayload(payload), rejections
}

func validateAdaptiveCritiquePayload(request *Request, workflow *Workflow, task *PhaseTask, payload CritiquePayload) (json.RawMessage, []PhaseRejection) {
	analysis, err := analysisForUnit(workflow, task.UnitID)
	if err != nil {
		return marshalPhasePayload(payload), []PhaseRejection{{Code: "analysis_artifact_unreadable", Message: err.Error()}}
	}
	rejections := make([]PhaseRejection, 0)
	if payload.CriticMode != CriticIndependent && payload.CriticMode != CriticSameContext {
		rejections = append(rejections, PhaseRejection{Code: "invalid_critic_mode", Message: "adaptive critique requires independent or explicitly degraded same_context mode"})
	}
	expected := map[string]bool{}
	for _, candidate := range analysis.Candidates {
		expected[candidate.ID] = true
	}
	seenVerdicts := map[string]bool{}
	invariantIDs := map[string]bool{}
	for _, invariant := range analysis.Invariants {
		invariantIDs[invariant.ID] = true
	}
	questionIDs := map[string]bool{}
	for _, question := range analysis.Questions {
		questionIDs[question.QuestionID] = true
	}
	seenCandidates := existingCandidateIDs(workflow)
	for i := range payload.Verdicts {
		verdict := &payload.Verdicts[i]
		if !expected[verdict.CandidateID] || seenVerdicts[verdict.CandidateID] {
			rejections = append(rejections, PhaseRejection{Code: "invalid_critique_candidate", Message: "critique references an unknown or duplicate candidate"})
			continue
		}
		seenVerdicts[verdict.CandidateID] = true
		if verdict.Verdict != CritiqueSupported && verdict.Verdict != CritiqueUnsupported && verdict.Verdict != CritiqueRevise {
			rejections = append(rejections, PhaseRejection{Code: "invalid_critique_verdict", Message: "verdict must be supported, unsupported, or revise"})
		}
		if strings.TrimSpace(verdict.Rationale) == "" || verdict.Confidence <= 0 || verdict.Confidence > 1 {
			rejections = append(rejections, PhaseRejection{Code: "incomplete_critique", Message: "critique requires rationale and confidence greater than 0 and at most 1"})
		}
		for j := range verdict.Evidence {
			rejections = append(rejections, validateEvidenceRef(request, workflow, &verdict.Evidence[j])...)
		}
		if verdict.Verdict == CritiqueRevise {
			if verdict.Replacement == nil {
				rejections = append(rejections, PhaseRejection{Code: "missing_replacement_candidate", Message: "revise verdict requires a complete replacement candidate"})
			} else {
				rejections = append(rejections, validateCandidate(request, workflow, task.UnitID, verdict.Replacement, seenCandidates, invariantIDs, questionIDs, true)...)
			}
		} else if verdict.Replacement != nil {
			rejections = append(rejections, PhaseRejection{Code: "unexpected_replacement_candidate", Message: "only revise verdicts may contain a replacement candidate"})
		}
	}
	for candidateID := range expected {
		if !seenVerdicts[candidateID] {
			rejections = append(rejections, PhaseRejection{Code: "missing_critique", Message: fmt.Sprintf("critique must resolve %s", candidateID)})
		}
	}
	for i := range payload.NewCandidates {
		rejections = append(rejections, validateCandidate(request, workflow, task.UnitID, &payload.NewCandidates[i], seenCandidates, invariantIDs, questionIDs, true)...)
	}
	return marshalPhasePayload(payload), rejections
}

func questionsForUnit(request *Request, unitID string) map[string]bool {
	questions := map[string]bool{}
	for _, question := range request.ReviewQuestions {
		if question.UnitID == unitID {
			questions[question.ID] = true
		}
	}
	return questions
}

func existingCandidateIDs(workflow *Workflow) map[string]bool {
	seen := map[string]bool{}
	for _, task := range workflow.Tasks {
		if (task.Phase != PhaseCandidates && task.Phase != PhaseAnalysis) || task.State != PhaseSubmitted {
			continue
		}
		var submission PhaseSubmission
		if readJSON(task.SubmissionPath, &submission) != nil {
			continue
		}
		if task.Phase == PhaseAnalysis {
			var payload AdaptiveAnalysisPayload
			if json.Unmarshal(submission.Payload, &payload) == nil {
				for _, candidate := range payload.Candidates {
					seen[candidate.ID] = true
				}
			}
		} else {
			var payload CandidatesPayload
			if json.Unmarshal(submission.Payload, &payload) == nil {
				for _, candidate := range payload.Candidates {
					seen[candidate.ID] = true
				}
			}
		}
	}
	return seen
}

func analysisForUnit(workflow *Workflow, unitID string) (*AdaptiveAnalysisPayload, error) {
	for _, task := range workflow.Tasks {
		if task.Phase != PhaseAnalysis || task.UnitID != unitID || task.State != PhaseSubmitted {
			continue
		}
		var submission PhaseSubmission
		if err := readJSON(task.SubmissionPath, &submission); err != nil {
			return nil, err
		}
		var payload AdaptiveAnalysisPayload
		if err := json.Unmarshal(submission.Payload, &payload); err != nil {
			return nil, err
		}
		return &payload, nil
	}
	return nil, fmt.Errorf("analysis phase for unit %q is incomplete", unitID)
}

func validateCandidate(request *Request, workflow *Workflow, unitID string, candidate *Candidate, seen, invariantIDs, questionIDs map[string]bool, renderReady bool) []PhaseRejection {
	rejections := make([]PhaseRejection, 0)
	candidate.ID = strings.TrimSpace(candidate.ID)
	if candidate.ID == "" || seen[candidate.ID] {
		rejections = append(rejections, PhaseRejection{Code: "invalid_candidate_id", Message: "candidate IDs must be non-empty and unique"})
	} else {
		seen[candidate.ID] = true
	}
	if strings.TrimSpace(candidate.Title) == "" || strings.TrimSpace(candidate.Trigger) == "" || strings.TrimSpace(candidate.Impact) == "" || len(candidate.Evidence) == 0 {
		rejections = append(rejections, PhaseRejection{Code: "incomplete_candidate", Message: "candidates require title, trigger, impact, and evidence"})
	}
	if renderReady && (!allowedSeverity(candidate.Severity) || !allowedCategory(candidate.Category) || strings.TrimSpace(candidate.Explanation) == "") {
		rejections = append(rejections, PhaseRejection{Code: "candidate_not_render_ready", Message: "adaptive candidates require valid severity, category, and explanation"})
	}
	if candidate.Confidence <= 0 || candidate.Confidence > 1 {
		rejections = append(rejections, PhaseRejection{Code: "invalid_candidate_confidence", Message: "candidate confidence must be greater than 0 and at most 1"})
	}
	unit, file := findTarget(request, unitID, candidate.File)
	if unit == nil || file == nil {
		rejections = append(rejections, PhaseRejection{Code: "candidate_target_invalid", Message: "candidate must reference a file in its review unit"})
	} else if candidate.StartLine < 1 || candidate.EndLine < candidate.StartLine || candidate.EndLine > file.LineCount || (request.Mode == ModeDiff && !rangeTouchesChangedLine(file.Diff, candidate.StartLine, candidate.EndLine)) {
		rejections = append(rejections, PhaseRejection{Code: "candidate_anchor_invalid", Message: "candidate must anchor a valid changed line"})
	}
	for i := range candidate.Evidence {
		rejections = append(rejections, validateEvidenceRef(request, workflow, &candidate.Evidence[i])...)
	}
	for _, invariantID := range candidate.InvariantIDs {
		if !invariantIDs[invariantID] {
			rejections = append(rejections, PhaseRejection{Code: "unknown_invariant_id", Message: fmt.Sprintf("candidate %q references unknown invariant %q", candidate.ID, invariantID)})
		}
	}
	for _, questionID := range candidate.QuestionIDs {
		if !questionIDs[questionID] {
			rejections = append(rejections, PhaseRejection{Code: "unknown_question_id", Message: fmt.Sprintf("candidate %q references uninvestigated question %q", candidate.ID, questionID)})
		}
	}
	return rejections
}

func candidatesForUnit(workflow *Workflow, unitID string) ([]Candidate, error) {
	for _, task := range workflow.Tasks {
		if (task.Phase != PhaseCandidates && task.Phase != PhaseAnalysis) || task.UnitID != unitID || task.State != PhaseSubmitted {
			continue
		}
		var submission PhaseSubmission
		if err := readJSON(task.SubmissionPath, &submission); err != nil {
			return nil, err
		}
		if task.Phase == PhaseAnalysis {
			var payload AdaptiveAnalysisPayload
			if err := json.Unmarshal(submission.Payload, &payload); err != nil {
				return nil, err
			}
			return payload.Candidates, nil
		}
		var payload CandidatesPayload
		if err := json.Unmarshal(submission.Payload, &payload); err != nil {
			return nil, err
		}
		return payload.Candidates, nil
	}
	return nil, fmt.Errorf("candidate phase for unit %q is incomplete", unitID)
}

// PhasePrompt returns an agent-facing task without exposing executor identity
// or primary confidence to a critic.
func PhasePrompt(repo, sessionID, taskID string) (string, error) {
	request, err := LoadRequest(repo, sessionID)
	if err != nil {
		return "", err
	}
	workflow, err := LoadWorkflow(repo, sessionID)
	if err != nil {
		return "", err
	}
	index := findPhaseTask(workflow.Tasks, taskID)
	if index < 0 {
		return "", fmt.Errorf("phase task %q not found", taskID)
	}
	task := workflow.Tasks[index]
	unit, err := requestUnit(request, task.UnitID)
	if err != nil {
		return "", err
	}
	compact := request.Instructions.TokenEconomy.Mode == TokenEconomyCaveman
	var out strings.Builder
	if compact {
		fmt.Fprintf(&out, "ACR %s phase. Session %s. Unit %s. Review only; never edit source.\n", task.Phase, sessionID, task.UnitID)
		fmt.Fprintf(&out, "Use caveman skill level %s if installed; else compact fallback. Keep technical evidence complete.\n", request.Instructions.TokenEconomy.Level)
	} else {
		fmt.Fprintf(&out, "Complete the ACR %s phase for session %s and unit %s.\n", task.Phase, sessionID, task.UnitID)
		out.WriteString("This is a review-only task. Do not modify source files. Preserve complete technical evidence.\n")
	}
	for _, file := range unit.Files {
		fmt.Fprintf(&out, "File: %s\n", file.Path)
	}
	switch task.Phase {
	case PhaseAnalysis:
		out.WriteString("Analyze intent, impact, risk, deterministic questions, and complete render-ready candidates in one pass. Return AdaptiveAnalysisPayload JSON.\n")
	case PhaseIntent:
		out.WriteString("Extract changed behavior and evidence-backed invariants. Return IntentPayload JSON.\n")
	case PhaseImpact:
		out.WriteString("Trace callers, callees, state, side effects, lifecycle, errors, tests, and every focused risk question. Return ImpactPayload JSON.\n")
	case PhaseCandidates:
		out.WriteString("Propose only concrete reachable defects. Anchor each candidate to a changed line and cite evidence. Empty candidates require coverage. Return CandidatesPayload JSON.\n")
	case PhaseCritique:
		candidates, err := candidatesForUnit(workflow, task.UnitID)
		if err != nil {
			return "", err
		}
		out.WriteString("Blindly challenge each candidate. Do not assume candidate is correct. Return one supported, unsupported, or revise verdict per candidate.\n")
		for _, candidate := range candidates {
			fmt.Fprintf(&out, "Candidate %s: %s\nTrigger: %s\nImpact: %s\n", candidate.ID, candidate.Title, candidate.Trigger, candidate.Impact)
			for _, evidence := range candidate.Evidence {
				fmt.Fprintf(&out, "Evidence: %s:%d-%d\n", evidence.File, evidence.StartLine, evidence.EndLine)
			}
		}
		if request.ProtocolVersion == AdaptiveProtocolVersion {
			out.WriteString("Use the same host/model in a fresh context. You may add complete newly discovered candidates; revise verdicts require a complete replacement candidate.\n")
		}
	case PhaseFinalize:
		out.WriteString("Synthesize critic results. Resolve every candidate as submit or drop with reasons. Unsupported candidates need override reason and additional evidence. Return FinalizePayload JSON.\n")
	case PhaseResolve:
		out.WriteString("Resolve only critic disagreements, revisions, and newly discovered candidates in the original primary context. Critic-rejected submissions need an override reason and additional evidence. Return ResolvePayload JSON.\n")
	}
	return out.String(), nil
}

// CreatePhaseDraft writes a non-overwriting transport envelope for one claimed
// phase so agents only need to fill executor, backend, and payload details.
func CreatePhaseDraft(repo, sessionID, taskID string) (*PhaseSubmission, string, error) {
	request, err := LoadRequest(repo, sessionID)
	if err != nil {
		return nil, "", err
	}
	workflow, err := LoadWorkflow(repo, sessionID)
	if err != nil {
		return nil, "", err
	}
	index := findPhaseTask(workflow.Tasks, taskID)
	if index < 0 {
		return nil, "", fmt.Errorf("phase task %q not found", taskID)
	}
	task := workflow.Tasks[index]
	draft := &PhaseSubmission{
		ProtocolVersion: request.ProtocolVersion, SessionID: sessionID, TaskID: task.ID,
		UnitID: task.UnitID, Phase: task.Phase,
		Communication: Communication{Mode: request.Instructions.TokenEconomy.Mode, Level: request.Instructions.TokenEconomy.Level},
		Payload:       emptyPhasePayload(task.Phase),
	}
	if request.Instructions.TokenEconomy.Mode == TokenEconomyNormal {
		draft.Communication.Backend = CommunicationNormal
	} else if workflow.CommunicationBackend != "" {
		draft.Communication.Backend = workflow.CommunicationBackend
	}
	if workflow.PrimaryExecutor != nil {
		draft.Executor.Host = workflow.PrimaryExecutor.Host
		draft.Executor.Model = workflow.PrimaryExecutor.Model
		if task.Phase != PhaseCritique {
			draft.Executor.ContextID = workflow.PrimaryExecutor.ContextID
		}
	}
	path := filepath.Join(SessionDir(repo, sessionID), "phases", task.Phase, task.UnitID, task.ID+".input.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, path, fmt.Errorf("create phase directory: %w", err)
	}
	data, err := json.MarshalIndent(draft, "", "  ")
	if err != nil {
		return nil, path, err
	}
	data = append(data, '\n')
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if os.IsExist(err) {
		var existing PhaseSubmission
		if readErr := readJSON(path, &existing); readErr != nil {
			return nil, path, fmt.Errorf("phase draft already exists but is unreadable: %w", readErr)
		}
		return &existing, path, nil
	}
	if err != nil {
		return nil, path, err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return nil, path, err
	}
	if err := file.Close(); err != nil {
		return nil, path, err
	}
	return draft, path, nil
}

func emptyPhasePayload(phase string) json.RawMessage {
	switch phase {
	case PhaseIntent:
		return marshalPhasePayload(IntentPayload{BehaviorChanges: []EvidenceStatement{}, Invariants: []EvidenceStatement{}})
	case PhaseImpact:
		return marshalPhasePayload(ImpactPayload{Traces: []ImpactTrace{}, Questions: []InvestigatedQuestion{}})
	case PhaseCandidates:
		return marshalPhasePayload(CandidatesPayload{Candidates: []Candidate{}})
	case PhaseCritique:
		return marshalPhasePayload(CritiquePayload{Verdicts: []CritiqueVerdict{}})
	case PhaseFinalize:
		return marshalPhasePayload(FinalizePayload{CandidateDispositions: []CandidateDisposition{}})
	case PhaseAnalysis:
		return marshalPhasePayload(AdaptiveAnalysisPayload{BehaviorChanges: []EvidenceStatement{}, Invariants: []EvidenceStatement{}, Traces: []ImpactTrace{}, Questions: []InvestigatedQuestion{}, Candidates: []Candidate{}})
	case PhaseResolve:
		return marshalPhasePayload(ResolvePayload{CandidateDispositions: []CandidateDisposition{}})
	default:
		return json.RawMessage(`{}`)
	}
}

func knownLineageIDsForUnit(workflow *Workflow, unitID string) (map[string]bool, map[string]bool) {
	invariants := map[string]bool{}
	questions := map[string]bool{}
	for _, task := range workflow.Tasks {
		if task.UnitID != unitID || task.State != PhaseSubmitted {
			continue
		}
		var submission PhaseSubmission
		if readJSON(task.SubmissionPath, &submission) != nil {
			continue
		}
		switch task.Phase {
		case PhaseIntent:
			var payload IntentPayload
			if json.Unmarshal(submission.Payload, &payload) == nil {
				for _, invariant := range payload.Invariants {
					invariants[invariant.ID] = true
				}
			}
		case PhaseImpact:
			var payload ImpactPayload
			if json.Unmarshal(submission.Payload, &payload) == nil {
				for _, question := range payload.Questions {
					questions[question.QuestionID] = true
				}
			}
		}
	}
	return invariants, questions
}

func verdictsForUnit(workflow *Workflow, unitID string) (map[string]CritiqueVerdict, error) {
	for _, task := range workflow.Tasks {
		if task.Phase != PhaseCritique || task.UnitID != unitID || task.State != PhaseSubmitted {
			continue
		}
		var submission PhaseSubmission
		if err := readJSON(task.SubmissionPath, &submission); err != nil {
			return nil, err
		}
		var payload CritiquePayload
		if err := json.Unmarshal(submission.Payload, &payload); err != nil {
			return nil, err
		}
		result := map[string]CritiqueVerdict{}
		for _, verdict := range payload.Verdicts {
			result[verdict.CandidateID] = verdict
		}
		return result, nil
	}
	return nil, fmt.Errorf("critique phase for unit %q is incomplete", unitID)
}

func critiqueForUnit(workflow *Workflow, unitID string) (*CritiquePayload, error) {
	for _, task := range workflow.Tasks {
		if task.Phase != PhaseCritique || task.UnitID != unitID || task.State != PhaseSubmitted {
			continue
		}
		var submission PhaseSubmission
		if err := readJSON(task.SubmissionPath, &submission); err != nil {
			return nil, err
		}
		var payload CritiquePayload
		if err := json.Unmarshal(submission.Payload, &payload); err != nil {
			return nil, err
		}
		return &payload, nil
	}
	return nil, fmt.Errorf("critique phase for unit %q is incomplete", unitID)
}

func workflowFinalDispositions(workflow *Workflow) ([]CandidateDisposition, error) {
	result := make([]CandidateDisposition, 0)
	for _, task := range workflow.Tasks {
		if task.Phase != PhaseFinalize || task.State != PhaseSubmitted {
			continue
		}
		var submission PhaseSubmission
		if err := readJSON(task.SubmissionPath, &submission); err != nil {
			return nil, err
		}
		var payload FinalizePayload
		if err := json.Unmarshal(submission.Payload, &payload); err != nil {
			return nil, err
		}
		result = append(result, payload.CandidateDispositions...)
	}
	return result, nil
}

func adaptiveSubmission(request *Request, workflow *Workflow) (*Submission, error) {
	draft := &Submission{
		ProtocolVersion: AdaptiveProtocolVersion,
		SessionID:       request.SessionID,
		Findings:        []Finding{},
	}
	accepted := make([]struct {
		unitID    string
		candidate Candidate
	}, 0)
	for _, unit := range request.Units {
		analysis, err := analysisForUnit(workflow, unit.ID)
		if err != nil {
			return nil, err
		}
		if !hasSubmittedTask(workflow.Tasks, PhaseCritique, unit.ID) {
			continue
		}
		critique, err := critiqueForUnit(workflow, unit.ID)
		if err != nil {
			return nil, err
		}
		originals := map[string]Candidate{}
		for _, candidate := range analysis.Candidates {
			originals[candidate.ID] = candidate
		}
		resolved, err := resolveDispositionsForUnit(workflow, unit.ID)
		if err != nil {
			return nil, err
		}
		for _, verdict := range critique.Verdicts {
			switch verdict.Verdict {
			case CritiqueSupported:
				disposition := CandidateDisposition{CandidateID: verdict.CandidateID, Outcome: DispositionSubmit, Reason: "Supported by the critic."}
				draft.CandidateDispositions = append(draft.CandidateDispositions, disposition)
				accepted = append(accepted, struct {
					unitID    string
					candidate Candidate
				}{unit.ID, originals[verdict.CandidateID]})
			case CritiqueUnsupported:
				disposition := resolved[verdict.CandidateID]
				draft.CandidateDispositions = append(draft.CandidateDispositions, disposition)
				if disposition.Outcome == DispositionSubmit {
					accepted = append(accepted, struct {
						unitID    string
						candidate Candidate
					}{unit.ID, originals[verdict.CandidateID]})
				}
			case CritiqueRevise:
				if verdict.Replacement != nil {
					disposition := resolved[verdict.Replacement.ID]
					draft.CandidateDispositions = append(draft.CandidateDispositions, disposition)
					if disposition.Outcome == DispositionSubmit {
						accepted = append(accepted, struct {
							unitID    string
							candidate Candidate
						}{unit.ID, *verdict.Replacement})
					}
				}
			}
		}
		for _, candidate := range critique.NewCandidates {
			disposition := resolved[candidate.ID]
			draft.CandidateDispositions = append(draft.CandidateDispositions, disposition)
			if disposition.Outcome == DispositionSubmit {
				accepted = append(accepted, struct {
					unitID    string
					candidate Candidate
				}{unit.ID, candidate})
			}
		}
	}
	questionFinding := map[string]int{}
	for _, item := range accepted {
		candidate := item.candidate
		index := len(draft.Findings)
		draft.Findings = append(draft.Findings, Finding{
			CandidateID: candidate.ID, UnitID: item.unitID, File: candidate.File,
			StartLine: candidate.StartLine, EndLine: candidate.EndLine, Severity: candidate.Severity,
			Category: candidate.Category, Explanation: candidate.Explanation, Evidence: evidenceSummary(candidate.Evidence),
			SuggestedFix: candidate.SuggestedFix, Confidence: candidate.Confidence,
		})
		for _, questionID := range candidate.QuestionIDs {
			questionFinding[questionID] = index
		}
	}
	for _, unit := range request.Units {
		analysis, err := analysisForUnit(workflow, unit.ID)
		if err != nil {
			return nil, err
		}
		for _, question := range analysis.Questions {
			resolution := QuestionResolution{QuestionID: question.QuestionID, Outcome: "no_finding", Evidence: strings.TrimSpace(question.Conclusion + " " + evidenceSummary(question.Evidence))}
			if index, ok := questionFinding[question.QuestionID]; ok {
				resolution.Outcome = "finding"
				resolution.FindingIndex = &index
			}
			draft.QuestionResolutions = append(draft.QuestionResolutions, resolution)
		}
	}
	return draft, nil
}

func hasSubmittedTask(tasks []PhaseTask, phase, unitID string) bool {
	for _, task := range tasks {
		if task.Phase == phase && task.UnitID == unitID && task.State == PhaseSubmitted {
			return true
		}
	}
	return false
}

func resolveDispositionsForUnit(workflow *Workflow, unitID string) (map[string]CandidateDisposition, error) {
	result := map[string]CandidateDisposition{}
	for _, task := range workflow.Tasks {
		if task.Phase != PhaseResolve || task.UnitID != unitID {
			continue
		}
		if task.State != PhaseSubmitted {
			return nil, fmt.Errorf("resolve phase for unit %q is incomplete", unitID)
		}
		var submission PhaseSubmission
		if err := readJSON(task.SubmissionPath, &submission); err != nil {
			return nil, err
		}
		var payload ResolvePayload
		if err := json.Unmarshal(submission.Payload, &payload); err != nil {
			return nil, err
		}
		for _, disposition := range payload.CandidateDispositions {
			result[disposition.CandidateID] = disposition
		}
	}
	return result, nil
}

func evidenceSummary(evidence []EvidenceRef) string {
	parts := make([]string, 0, len(evidence))
	for _, ref := range evidence {
		parts = append(parts, fmt.Sprintf("%s:%d-%d sha256:%s", ref.File, ref.StartLine, ref.EndLine, ref.SHA256))
	}
	return strings.Join(parts, "; ")
}

func requestUnit(request *Request, unitID string) (*ReviewUnit, error) {
	for i := range request.Units {
		if request.Units[i].ID == unitID {
			return &request.Units[i], nil
		}
	}
	return nil, fmt.Errorf("review unit %q not found", unitID)
}

func validateEvidenceStatement(request *Request, workflow *Workflow, statement *EvidenceStatement, seen map[string]bool) []PhaseRejection {
	rejections := make([]PhaseRejection, 0)
	statement.ID = strings.TrimSpace(statement.ID)
	if statement.ID == "" || seen[statement.ID] {
		rejections = append(rejections, PhaseRejection{Code: "invalid_statement_id", Message: "statement IDs must be non-empty and unique"})
	} else {
		seen[statement.ID] = true
	}
	if strings.TrimSpace(statement.Summary) == "" || len(statement.Evidence) == 0 {
		rejections = append(rejections, PhaseRejection{Code: "missing_statement_evidence", Message: "statements require a summary and evidence"})
	}
	for i := range statement.Evidence {
		rejections = append(rejections, validateEvidenceRef(request, workflow, &statement.Evidence[i])...)
	}
	return rejections
}

func validateEvidenceRef(request *Request, workflow *Workflow, evidence *EvidenceRef) []PhaseRejection {
	rel, full, err := resolveRepoFile(request.Repository.Root, evidence.File)
	if err != nil {
		return []PhaseRejection{{Code: "unsafe_evidence_path", Message: err.Error()}}
	}
	var data []byte
	if snapshot := preparedSnapshot(request, rel); snapshot != nil && !snapshot.ValidateWorkspace {
		data = []byte(snapshot.Content)
	} else {
		data, err = readRegularFile(full)
		if err != nil {
			return []PhaseRejection{{Code: "evidence_unreadable", Message: fmt.Sprintf("%s: %v", rel, err)}}
		}
	}
	lineCount := countLines(data)
	if evidence.StartLine < 1 || evidence.EndLine < evidence.StartLine || evidence.EndLine > lineCount {
		return []PhaseRejection{{Code: "evidence_line_out_of_range", Message: fmt.Sprintf("%s line range must be within 1..%d", rel, lineCount)}}
	}
	digest := contentSHA256(data)
	if existing, ok := workflow.Evidence[rel]; ok && existing.SHA256 != digest {
		return []PhaseRejection{{Code: "stale_evidence", Message: fmt.Sprintf("%s changed after it was first cited", rel)}}
	}
	evidence.File = rel
	evidence.SHA256 = digest
	workflow.Evidence[rel] = EvidenceSnapshot{File: rel, SHA256: digest, LineCount: lineCount}
	return nil
}

func preparedSnapshot(request *Request, path string) *FileSnapshot {
	for i := range request.Units {
		for j := range request.Units[i].Files {
			if request.Units[i].Files[j].Path == path {
				return &request.Units[i].Files[j]
			}
		}
	}
	return nil
}

func decodeStrictJSON(raw json.RawMessage, target any) error {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode phase payload: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("decode phase payload: trailing data")
		}
		return fmt.Errorf("decode phase payload trailing data: %w", err)
	}
	return nil
}

func marshalPhasePayload(value any) json.RawMessage {
	data, _ := json.Marshal(value)
	return data
}

func phaseSubmissionDigest(submission PhaseSubmission) string {
	data, _ := json.Marshal(submission)
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func findPhaseTask(tasks []PhaseTask, id string) int {
	for i := range tasks {
		if tasks[i].ID == id {
			return i
		}
	}
	return -1
}

func allPhaseTasksSubmitted(tasks []PhaseTask) bool {
	for _, task := range tasks {
		if task.State != PhaseSubmitted {
			return false
		}
	}
	return true
}

func withWorkflowLock(repo, sessionID string, fn func() error) error {
	lock := filepath.Join(SessionDir(repo, sessionID), ".workflow.lock")
	deadline := time.Now().Add(2 * time.Second)
	for {
		err := os.Mkdir(lock, 0o700)
		if err == nil {
			defer os.Remove(lock)
			return fn()
		}
		if !os.IsExist(err) {
			return fmt.Errorf("lock workflow: %w", err)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("workflow is busy")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
