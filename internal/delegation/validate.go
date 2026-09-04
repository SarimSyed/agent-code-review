// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package delegation

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var hunkHeader = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,(\d+))? @@`)

func Submit(repo, sessionID string, submission Submission) (*Result, error) {
	request, err := LoadRequest(repo, sessionID)
	if err != nil {
		return nil, err
	}
	if submission.ProtocolVersion != request.ProtocolVersion {
		return nil, fmt.Errorf("unsupported submission protocol %q", submission.ProtocolVersion)
	}
	if submission.SessionID != sessionID {
		return nil, fmt.Errorf("submission session id %q does not match %q", submission.SessionID, sessionID)
	}
	submission = normalizeSubmission(submission)
	dir := SessionDir(repo, sessionID)
	if err := writeJSON(filepath.Join(dir, FindingsFileName), submission); err != nil {
		return nil, err
	}

	result := &Result{
		ProtocolVersion:       request.ProtocolVersion,
		SessionID:             sessionID,
		QuestionResolutions:   append([]QuestionResolution(nil), submission.QuestionResolutions...),
		CandidateDispositions: append([]CandidateDisposition(nil), submission.CandidateDispositions...),
		Findings:              make([]Finding, 0, len(submission.Findings)),
		Rejected:              make([]Rejection, 0),
	}
	result.Rejected = validateSnapshots(request)
	if request.Repository.TrackedSourceSHA256 != "" {
		current, fingerprintErr := trackedSourceFingerprint(request.Repository.Root)
		if fingerprintErr != nil || current != request.Repository.TrackedSourceSHA256 {
			message := "tracked source changed after review preparation"
			if fingerprintErr != nil {
				message = fingerprintErr.Error()
			}
			result.Rejected = append(result.Rejected, Rejection{Index: -1, Code: "source_modified", Message: message})
		}
	}
	if len(result.Rejected) > 0 {
		result.Summary.Rejected = len(result.Rejected)
		if err := writeJSON(filepath.Join(dir, ResultFileName), result); err != nil {
			return nil, err
		}
		return result, nil
	}
	result.Rejected = append(result.Rejected, validateQuestionResolutions(request, submission)...)
	var workflow *Workflow
	if request.ProtocolVersion == ProtocolVersion && request.Instructions.ReviewProfile == ReviewProfileDeep {
		workflow, err = LoadWorkflow(repo, sessionID)
		if err != nil {
			result.Rejected = append(result.Rejected, Rejection{Index: -1, Code: "workflow_unreadable", Message: err.Error()})
		} else {
			result.Rejected = append(result.Rejected, validateDeepFinalization(request, workflow, submission)...)
			result.Assurance = buildReviewAssurance(request, workflow, submission)
		}
		if len(result.Rejected) > 0 {
			result.Summary.Rejected = len(result.Rejected)
			if err := writeJSON(filepath.Join(dir, ResultFileName), result); err != nil {
				return nil, err
			}
			return result, nil
		}
	} else if request.ProtocolVersion == AdaptiveProtocolVersion && request.Instructions.ReviewProfile == ReviewProfileAdaptive {
		workflow, err = LoadWorkflow(repo, sessionID)
		if err != nil {
			result.Rejected = append(result.Rejected, Rejection{Index: -1, Code: "workflow_unreadable", Message: err.Error()})
		} else {
			result.Rejected = append(result.Rejected, validateAdaptiveFinalization(request, workflow, submission)...)
			result.Assurance = buildReviewAssurance(request, workflow, submission)
		}
		if len(result.Rejected) > 0 {
			result.Summary.Rejected = len(result.Rejected)
			if err := writeJSON(filepath.Join(dir, ResultFileName), result); err != nil {
				return nil, err
			}
			return result, nil
		}
	}
	seen := map[string]struct{}{}
	acceptedIndexes := map[int]bool{}
	for i, finding := range submission.Findings {
		if rejection := validateFinding(request, finding, i); rejection != nil {
			result.Rejected = append(result.Rejected, *rejection)
			continue
		}
		key := duplicateKey(finding)
		if _, duplicate := seen[key]; duplicate {
			result.Summary.Duplicates++
			continue
		}
		seen[key] = struct{}{}
		result.Findings = append(result.Findings, finding)
		acceptedIndexes[i] = true
	}
	result.Rejected = append(result.Rejected, validateResolutionFindingIndexes(submission, acceptedIndexes)...)
	result.Summary.Accepted = len(result.Findings)
	result.Summary.Rejected = len(result.Rejected)
	if err := writeJSON(filepath.Join(dir, ResultFileName), result); err != nil {
		return nil, err
	}
	if workflow != nil && len(result.Rejected) == 0 {
		workflow.State = WorkflowComplete
		if err := saveWorkflow(repo, workflow); err != nil {
			return nil, err
		}
		result.Assurance.WorkflowState = WorkflowComplete
		if err := writeJSON(filepath.Join(dir, ResultFileName), result); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func validateAdaptiveFinalization(request *Request, workflow *Workflow, submission Submission) []Rejection {
	if workflow.State != WorkflowReady && workflow.State != WorkflowComplete {
		return []Rejection{{Index: -1, Code: "phase_incomplete", Message: "complete every adaptive review phase before final submission"}}
	}
	rejections := validateWorkflowEvidence(request, workflow)
	expected, err := adaptiveSubmission(request, workflow)
	if err != nil {
		return append(rejections, Rejection{Index: -1, Code: "workflow_artifact_invalid", Message: err.Error()})
	}
	expectedValue := normalizeSubmission(*expected)
	expectedJSON, _ := json.Marshal(expectedValue)
	actualJSON, _ := json.Marshal(submission)
	if string(expectedJSON) != string(actualJSON) {
		rejections = append(rejections, Rejection{Index: -1, Code: "adaptive_draft_mismatch", Message: "adaptive findings must match the accepted render-ready candidates and stored question resolutions"})
	}
	return rejections
}

func validateDeepFinalization(request *Request, workflow *Workflow, submission Submission) []Rejection {
	if workflow.State != WorkflowReady && workflow.State != WorkflowComplete {
		return []Rejection{{Index: -1, Code: "phase_incomplete", Message: "complete every deep-review phase before final submission"}}
	}
	candidates, verdicts, err := workflowCandidatesAndVerdicts(workflow)
	if err != nil {
		return []Rejection{{Index: -1, Code: "workflow_artifact_invalid", Message: err.Error()}}
	}
	rejections := validateWorkflowEvidence(request, workflow)
	finalized, err := workflowFinalDispositions(workflow)
	if err != nil {
		return append(rejections, Rejection{Index: -1, Code: "finalize_artifact_invalid", Message: err.Error()})
	}
	finalizedByID := map[string]CandidateDisposition{}
	for _, disposition := range finalized {
		finalizedByID[disposition.CandidateID] = disposition
	}
	dispositions := map[string]CandidateDisposition{}
	for _, disposition := range submission.CandidateDispositions {
		if _, ok := candidates[disposition.CandidateID]; !ok {
			rejections = append(rejections, Rejection{Index: -1, Code: "unknown_candidate_disposition", Message: fmt.Sprintf("disposition references unknown candidate %q", disposition.CandidateID)})
			continue
		}
		if _, duplicate := dispositions[disposition.CandidateID]; duplicate {
			rejections = append(rejections, Rejection{Index: -1, Code: "duplicate_candidate_disposition", Message: fmt.Sprintf("candidate %q has multiple dispositions", disposition.CandidateID)})
			continue
		}
		if disposition.Outcome != DispositionSubmit && disposition.Outcome != DispositionDrop {
			rejections = append(rejections, Rejection{Index: -1, Code: "invalid_candidate_disposition", Message: "candidate outcome must be submit or drop"})
		}
		if strings.TrimSpace(disposition.Reason) == "" {
			rejections = append(rejections, Rejection{Index: -1, Code: "missing_disposition_reason", Message: fmt.Sprintf("candidate %q requires a disposition reason", disposition.CandidateID)})
		}
		verdict := verdicts[disposition.CandidateID]
		if disposition.Outcome == DispositionSubmit && verdict.Verdict == CritiqueUnsupported {
			if strings.TrimSpace(disposition.OverrideReason) == "" || len(disposition.AdditionalEvidence) == 0 {
				rejections = append(rejections, Rejection{Index: -1, Code: "critic_override_required", Message: fmt.Sprintf("candidate %q was rejected by the critic and requires override_reason plus additional_evidence", disposition.CandidateID)})
			}
		}
		for i := range disposition.AdditionalEvidence {
			phaseRejections := validateEvidenceRef(request, workflow, &disposition.AdditionalEvidence[i])
			for _, rejection := range phaseRejections {
				rejections = append(rejections, Rejection{Index: -1, Code: rejection.Code, Message: rejection.Message})
			}
		}
		dispositions[disposition.CandidateID] = disposition
		if persisted, ok := finalizedByID[disposition.CandidateID]; !ok || dispositionDigest(persisted) != dispositionDigest(disposition) {
			rejections = append(rejections, Rejection{Index: -1, Code: "finalize_disposition_mismatch", Message: fmt.Sprintf("candidate %q disposition must match the persisted finalize phase", disposition.CandidateID)})
		}
	}
	for candidateID := range candidates {
		if _, ok := dispositions[candidateID]; !ok {
			rejections = append(rejections, Rejection{Index: -1, Code: "missing_candidate_disposition", Message: fmt.Sprintf("resolve candidate %q as submit or drop", candidateID)})
		}
	}
	findingCounts := map[string]int{}
	for i, finding := range submission.Findings {
		disposition, ok := dispositions[finding.CandidateID]
		if finding.CandidateID == "" || !ok {
			rejections = append(rejections, Rejection{Index: i, Code: "finding_candidate_missing", Message: "deep findings must reference a known candidate_id"})
			continue
		}
		if disposition.Outcome != DispositionSubmit {
			rejections = append(rejections, Rejection{Index: i, Code: "finding_candidate_dropped", Message: "finding references a dropped candidate"})
		}
		findingCounts[finding.CandidateID]++
	}
	for candidateID, disposition := range dispositions {
		if disposition.Outcome == DispositionSubmit && findingCounts[candidateID] != 1 {
			rejections = append(rejections, Rejection{Index: -1, Code: "candidate_finding_count", Message: fmt.Sprintf("submitted candidate %q must map to exactly one finding", candidateID)})
		}
	}
	return rejections
}

func validateWorkflowEvidence(request *Request, workflow *Workflow) []Rejection {
	rejections := make([]Rejection, 0)
	for path, snapshot := range workflow.Evidence {
		if prepared := preparedSnapshot(request, path); prepared != nil && !prepared.ValidateWorkspace {
			continue
		}
		_, full, err := resolveRepoFile(request.Repository.Root, path)
		if err != nil {
			rejections = append(rejections, Rejection{Index: -1, Code: "unsafe_evidence_path", Message: err.Error()})
			continue
		}
		data, err := readRegularFile(full)
		if err != nil || contentSHA256(data) != snapshot.SHA256 {
			rejections = append(rejections, Rejection{Index: -1, Code: "stale_evidence", Message: fmt.Sprintf("%s changed after phase evidence was recorded", path)})
		}
	}
	return rejections
}

func workflowCandidatesAndVerdicts(workflow *Workflow) (map[string]Candidate, map[string]CritiqueVerdict, error) {
	candidates := map[string]Candidate{}
	verdicts := map[string]CritiqueVerdict{}
	for _, task := range workflow.Tasks {
		if task.State != PhaseSubmitted || (task.Phase != PhaseCandidates && task.Phase != PhaseCritique) {
			continue
		}
		var phase PhaseSubmission
		if err := readJSON(task.SubmissionPath, &phase); err != nil {
			return nil, nil, err
		}
		if task.Phase == PhaseCandidates {
			var payload CandidatesPayload
			if err := json.Unmarshal(phase.Payload, &payload); err != nil {
				return nil, nil, err
			}
			for _, candidate := range payload.Candidates {
				candidates[candidate.ID] = candidate
			}
		} else {
			var payload CritiquePayload
			if err := json.Unmarshal(phase.Payload, &payload); err != nil {
				return nil, nil, err
			}
			for _, verdict := range payload.Verdicts {
				verdicts[verdict.CandidateID] = verdict
			}
		}
	}
	return candidates, verdicts, nil
}

func buildReviewAssurance(request *Request, workflow *Workflow, submission Submission) *ReviewAssurance {
	candidates, _, _ := workflowCandidatesAndVerdicts(workflow)
	assurance := &ReviewAssurance{
		WorkflowState: workflow.State, CriticMode: workflow.CriticMode,
		CommunicationMode:    request.Instructions.TokenEconomy.Mode,
		CommunicationLevel:   request.Instructions.TokenEconomy.Level,
		CommunicationBackend: workflow.CommunicationBackend,
		Candidates:           len(candidates), EvidenceFiles: len(workflow.Evidence),
		BatchCalls: workflow.BatchCalls, ValidationRejections: workflow.ValidationRejections,
	}
	if workflow.ProtocolVersion == AdaptiveProtocolVersion {
		assurance.Candidates = len(submission.CandidateDispositions)
	}
	var earliest, latest time.Time
	for _, task := range workflow.Tasks {
		if task.State == PhaseSubmitted {
			assurance.PhasesCompleted++
		}
		if task.ClaimedAt.IsZero() || task.SubmittedAt.IsZero() {
			continue
		}
		if earliest.IsZero() || task.ClaimedAt.Before(earliest) {
			earliest = task.ClaimedAt
		}
		if latest.IsZero() || task.SubmittedAt.After(latest) {
			latest = task.SubmittedAt
		}
		switch task.Phase {
		case PhaseAnalysis:
			assurance.AnalysisMS = extendStageWindow(assurance.AnalysisMS, workflow.Tasks, PhaseAnalysis)
		case PhaseCritique:
			assurance.CritiqueMS = extendStageWindow(assurance.CritiqueMS, workflow.Tasks, PhaseCritique)
			var phase PhaseSubmission
			var payload CritiquePayload
			if readJSON(task.SubmissionPath, &phase) == nil && json.Unmarshal(phase.Payload, &payload) == nil && payload.CriticMode == CriticSameContext {
				assurance.CriticFallbacks++
			}
		case PhaseResolve:
			assurance.ResolutionMS = extendStageWindow(assurance.ResolutionMS, workflow.Tasks, PhaseResolve)
		}
	}
	if !earliest.IsZero() && latest.After(earliest) {
		assurance.TotalElapsedMS = latest.Sub(earliest).Milliseconds()
	}
	for _, disposition := range submission.CandidateDispositions {
		if disposition.Outcome == DispositionDrop {
			assurance.Dropped++
		}
		if disposition.OverrideReason != "" {
			assurance.Overrides++
		}
	}
	return assurance
}

func extendStageWindow(current int64, tasks []PhaseTask, phase string) int64 {
	if current != 0 {
		return current
	}
	var earliest, latest time.Time
	for _, task := range tasks {
		if task.Phase != phase || task.ClaimedAt.IsZero() || task.SubmittedAt.IsZero() {
			continue
		}
		if earliest.IsZero() || task.ClaimedAt.Before(earliest) {
			earliest = task.ClaimedAt
		}
		if latest.IsZero() || task.SubmittedAt.After(latest) {
			latest = task.SubmittedAt
		}
	}
	if earliest.IsZero() || !latest.After(earliest) {
		return 0
	}
	return latest.Sub(earliest).Milliseconds()
}

func dispositionDigest(disposition CandidateDisposition) string {
	data, _ := json.Marshal(disposition)
	return string(data)
}

func validateQuestionResolutions(request *Request, submission Submission) []Rejection {
	if len(request.ReviewQuestions) == 0 {
		return nil
	}
	expected := make(map[string]ReviewQuestion, len(request.ReviewQuestions))
	for _, question := range request.ReviewQuestions {
		expected[question.ID] = question
	}
	seen := map[string]bool{}
	rejections := make([]Rejection, 0)
	for i, resolution := range submission.QuestionResolutions {
		question, ok := expected[resolution.QuestionID]
		if !ok {
			rejections = append(rejections, Rejection{Index: -1, Code: "unknown_question", Message: fmt.Sprintf("question resolution %d references unknown question %q", i, resolution.QuestionID)})
			continue
		}
		if seen[resolution.QuestionID] {
			rejections = append(rejections, Rejection{Index: -1, Code: "duplicate_question_resolution", Message: fmt.Sprintf("question %q is resolved more than once", resolution.QuestionID)})
			continue
		}
		seen[resolution.QuestionID] = true
		if strings.TrimSpace(resolution.Evidence) == "" {
			rejections = append(rejections, Rejection{Index: -1, Code: "missing_question_evidence", Message: fmt.Sprintf("question %q requires concrete inspection evidence", resolution.QuestionID)})
		}
		switch resolution.Outcome {
		case "no_finding":
			if resolution.FindingIndex != nil {
				rejections = append(rejections, Rejection{Index: -1, Code: "unexpected_finding_index", Message: fmt.Sprintf("question %q has no_finding outcome but references a finding", resolution.QuestionID)})
			}
		case "finding":
			if resolution.FindingIndex == nil || *resolution.FindingIndex < 0 || *resolution.FindingIndex >= len(submission.Findings) {
				rejections = append(rejections, Rejection{Index: -1, Code: "invalid_question_finding", Message: fmt.Sprintf("question %q must reference a valid finding index", resolution.QuestionID)})
				continue
			}
			finding := submission.Findings[*resolution.FindingIndex]
			if finding.UnitID != question.UnitID || filepath.ToSlash(finding.File) != question.File {
				rejections = append(rejections, Rejection{Index: -1, Code: "question_finding_mismatch", Message: fmt.Sprintf("question %q must reference a finding for %s in %s", resolution.QuestionID, question.Subject, question.File)})
			}
		default:
			rejections = append(rejections, Rejection{Index: -1, Code: "invalid_question_outcome", Message: fmt.Sprintf("question %q outcome must be finding or no_finding", resolution.QuestionID)})
		}
	}
	for _, question := range request.ReviewQuestions {
		if !seen[question.ID] {
			rejections = append(rejections, Rejection{Index: -1, Code: "missing_question_resolution", Message: fmt.Sprintf("resolve %s: %s", question.ID, question.Question)})
		}
	}
	return rejections
}

func validateResolutionFindingIndexes(submission Submission, accepted map[int]bool) []Rejection {
	rejections := make([]Rejection, 0)
	for _, resolution := range submission.QuestionResolutions {
		if resolution.Outcome == "finding" && resolution.FindingIndex != nil && !accepted[*resolution.FindingIndex] {
			rejections = append(rejections, Rejection{Index: -1, Code: "question_finding_rejected", Message: fmt.Sprintf("question %q references a rejected or duplicate finding", resolution.QuestionID)})
		}
	}
	return rejections
}

func normalizeSubmission(submission Submission) Submission {
	normalized := submission
	normalized.Findings = make([]Finding, len(submission.Findings))
	copy(normalized.Findings, submission.Findings)
	for i := range normalized.Findings {
		normalized.Findings[i].Category = normalizeCategory(normalized.Findings[i].Category)
	}
	return normalized
}

func validateFinding(request *Request, finding Finding, index int) *Rejection {
	unit, file := findTarget(request, finding.UnitID, finding.File)
	if unit == nil {
		return reject(index, "unit_not_found", "finding references an unknown review unit")
	}
	if file == nil {
		return reject(index, "file_not_in_unit", "finding file is not part of the referenced review unit")
	}
	if strings.TrimSpace(finding.Explanation) == "" || strings.TrimSpace(finding.Evidence) == "" {
		return reject(index, "missing_explanation", "explanation and evidence are required")
	}
	if !allowedSeverity(finding.Severity) {
		return reject(index, "invalid_severity", "severity must be critical, high, medium, or low")
	}
	if !allowedCategory(finding.Category) {
		return reject(index, "invalid_category", "category is not supported")
	}
	if finding.Confidence <= 0 || finding.Confidence > 1 {
		return reject(index, "invalid_confidence", "confidence must be greater than 0 and at most 1")
	}
	if finding.StartLine < 1 || finding.EndLine < finding.StartLine || finding.EndLine > file.LineCount {
		return reject(index, "line_out_of_range", fmt.Sprintf("line range must be within 1..%d", file.LineCount))
	}
	if request.Mode == ModeDiff && !rangeTouchesChangedLine(file.Diff, finding.StartLine, finding.EndLine) {
		return reject(index, "line_not_changed", "diff findings must anchor to a changed line")
	}
	return nil
}

func validateSnapshots(request *Request) []Rejection {
	rejections := make([]Rejection, 0)
	seen := map[string]struct{}{}
	for _, unit := range request.Units {
		for _, file := range unit.Files {
			if !file.ValidateWorkspace {
				continue
			}
			if _, ok := seen[file.Path]; ok {
				continue
			}
			seen[file.Path] = struct{}{}
			_, full, err := resolveRepoFile(request.Repository.Root, file.Path)
			if err != nil {
				rejections = append(rejections, Rejection{Index: -1, Code: "unsafe_path", Message: err.Error()})
				continue
			}
			content, err := readRegularFile(full)
			if err != nil {
				rejections = append(rejections, Rejection{Index: -1, Code: "file_unreadable", Message: fmt.Sprintf("%s: %v", file.Path, err)})
				continue
			}
			if contentSHA256(content) != file.SHA256 {
				rejections = append(rejections, Rejection{
					Index: -1, Code: "file_changed",
					Message: fmt.Sprintf("%s changed after preparation; prepare a new review session", file.Path),
				})
			}
		}
	}
	return rejections
}

func findTarget(request *Request, unitID, path string) (*ReviewUnit, *FileSnapshot) {
	for i := range request.Units {
		unit := &request.Units[i]
		if unit.ID != unitID {
			continue
		}
		for j := range unit.Files {
			if unit.Files[j].Path == filepath.ToSlash(path) {
				return unit, &unit.Files[j]
			}
		}
		return unit, nil
	}
	return nil, nil
}

func rangeTouchesChangedLine(raw string, start, end int) bool {
	changed := changedLines(raw)
	for line := start; line <= end; line++ {
		if changed[line] {
			return true
		}
	}
	return false
}

func changedLineRanges(raw string) []LineRange {
	lines := changedLines(raw)
	if len(lines) == 0 {
		return nil
	}
	ordered := make([]int, 0, len(lines))
	for line := range lines {
		ordered = append(ordered, line)
	}
	sort.Ints(ordered)
	ranges := make([]LineRange, 0, len(ordered))
	start, previous := ordered[0], ordered[0]
	for _, line := range ordered[1:] {
		if line == previous+1 {
			previous = line
			continue
		}
		ranges = append(ranges, LineRange{Start: start, End: previous})
		start, previous = line, line
	}
	return append(ranges, LineRange{Start: start, End: previous})
}

func changedLines(raw string) map[int]bool {
	changed := map[int]bool{}
	lineNo := 0
	inHunk := false
	for _, line := range strings.Split(raw, "\n") {
		if match := hunkHeader.FindStringSubmatch(line); match != nil {
			lineNo, _ = strconv.Atoi(match[1])
			inHunk = true
			continue
		}
		if !inHunk || strings.HasPrefix(line, "\\ No newline") {
			continue
		}
		switch {
		case strings.HasPrefix(line, "+"):
			changed[lineNo] = true
			lineNo++
		case strings.HasPrefix(line, "-"):
			// A deletion anchors to the nearest target-side line. This lets an
			// agent report lost behavior even when the hunk adds no replacement.
			changed[lineNo] = true
		default:
			lineNo++
		}
	}
	return changed
}

func reject(index int, code, message string) *Rejection {
	return &Rejection{Index: index, Code: code, Message: message}
}

func allowedSeverity(value string) bool {
	switch value {
	case "critical", "high", "medium", "low":
		return true
	default:
		return false
	}
}

func allowedCategory(value string) bool {
	value = normalizeCategory(value)
	for _, supported := range supportedCategories {
		if value == supported {
			return true
		}
	}
	return false
}

func normalizeCategory(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "correctness":
		// Correctness defects are bugs in the canonical review vocabulary.
		return "bug"
	default:
		return value
	}
}

func duplicateKey(finding Finding) string {
	return fmt.Sprintf("%s\x00%s\x00%d\x00%d\x00%s\x00%s",
		finding.UnitID, finding.File, finding.StartLine, finding.EndLine,
		strings.ToLower(strings.TrimSpace(finding.Category)),
		strings.ToLower(strings.TrimSpace(finding.Explanation)))
}
