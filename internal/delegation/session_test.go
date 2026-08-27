// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package delegation

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareWritesVersionedSessionWithoutChangingSource(t *testing.T) {
	repo := t.TempDir()
	sourcePath := filepath.Join(repo, "app.go")
	before := "package main\nfunc changed() {}\n"
	if err := os.WriteFile(sourcePath, []byte(before), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	request, err := Prepare(repo, PrepareInput{
		Mode:     ModeDiff,
		Revision: "abc123",
		Units: []PreparedUnit{{
			ID:   "unit-0001",
			Rule: "Report correctness defects.",
			Files: []PreparedFile{{
				Path: "app.go",
				Diff: "@@ -1,2 +1,2 @@\n package main\n-func old() {}\n+func changed() {}",
			}},
		}},
	})
	if err != nil {
		t.Fatalf("Prepare() error: %v", err)
	}
	if request.ProtocolVersion != ProtocolVersion || request.SessionID == "" {
		t.Fatalf("unexpected request identity: %#v", request)
	}
	if request.Instructions.ModelExecution != "host_agent" || !request.Instructions.ReviewOnly {
		t.Fatalf("unexpected instructions: %#v", request.Instructions)
	}
	if !containsString(request.Instructions.AllowedCategories, "bug") || !containsString(request.Instructions.AllowedCategories, "other") {
		t.Fatalf("review packet must advertise supported categories: %#v", request.Instructions.AllowedCategories)
	}
	if len(request.Units) != 1 || len(request.Units[0].Files) != 1 {
		t.Fatalf("unexpected units: %#v", request.Units)
	}
	file := request.Units[0].Files[0]
	if file.SHA256 == "" || file.LineCount != 2 {
		t.Fatalf("file snapshot not frozen: %#v", file)
	}

	requestPath := filepath.Join(repo, ".acr", "sessions", request.SessionID, RequestFileName)
	if _, err := os.Stat(requestPath); err != nil {
		t.Fatalf("request packet missing: %v", err)
	}
	after, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatalf("read source: %v", err)
	}
	if string(after) != before {
		t.Fatalf("Prepare changed source: got %q want %q", after, before)
	}
}

func TestPrepareDeepProfileIncludesIndependentReviewPasses(t *testing.T) {
	repo, request := prepareDiffFixture(t)
	if repo == "" {
		t.Fatal("fixture repository is required")
	}
	if request.Instructions.ReviewProfile != ReviewProfileDeep {
		t.Fatalf("default review profile = %q, want %q", request.Instructions.ReviewProfile, ReviewProfileDeep)
	}
	passes := map[string]bool{}
	for _, pass := range request.Instructions.RequiredPasses {
		passes[pass.ID] = true
	}
	for _, want := range []string{"invariants", "dependencies", "contracts", "lifecycle", "verification", "critique"} {
		if !passes[want] {
			t.Errorf("deep review packet missing %q pass: %#v", want, request.Instructions.RequiredPasses)
		}
	}
}

func TestBriefGeneratesFocusedQuestionsForRiskyDiffShapes(t *testing.T) {
	repo, request := prepareRiskFixture(t)
	briefing, err := Brief(repo, request.SessionID)
	if err != nil {
		t.Fatalf("Brief() error: %v", err)
	}
	kinds := map[string]bool{}
	for _, question := range briefing.ReviewQuestions {
		kinds[question.Kind] = true
	}
	for _, want := range []string{"dependency_order", "api_contract", "lifecycle"} {
		if !kinds[want] {
			t.Errorf("briefing missing %q review question: %#v", want, briefing.ReviewQuestions)
		}
	}
}

func TestSubmitRequiresEveryGeneratedQuestionToBeResolved(t *testing.T) {
	repo, request := prepareRiskFixture(t)
	result, err := Submit(repo, request.SessionID, Submission{
		ProtocolVersion: ProtocolVersion,
		SessionID:       request.SessionID,
	})
	if err != nil {
		t.Fatalf("Submit() error: %v", err)
	}
	if !hasRejectionCode(result.Rejected, "missing_question_resolution") {
		t.Fatalf("unresolved review questions must be rejected: %#v", result)
	}

	resolutions := make([]QuestionResolution, 0, len(request.ReviewQuestions))
	for _, question := range request.ReviewQuestions {
		resolutions = append(resolutions, QuestionResolution{
			QuestionID: question.ID,
			Outcome:    "no_finding",
			Evidence:   "Inspected the changed call and its implementation; the contract is preserved.",
		})
	}
	result, err = Submit(repo, request.SessionID, Submission{
		ProtocolVersion:     ProtocolVersion,
		SessionID:           request.SessionID,
		QuestionResolutions: resolutions,
	})
	if err != nil {
		t.Fatalf("Submit() with resolutions error: %v", err)
	}
	if len(result.Rejected) != 0 || len(result.QuestionResolutions) != len(request.ReviewQuestions) {
		t.Fatalf("resolved review questions should be accepted: %#v", result)
	}
}

func TestCreateSubmissionDraftScaffoldsQuestionsWithoutOverwriting(t *testing.T) {
	repo, request := prepareRiskFixture(t)
	draft, path, err := CreateSubmissionDraft(repo, request.SessionID)
	if err != nil {
		t.Fatalf("CreateSubmissionDraft() error: %v", err)
	}
	if path != filepath.Join(SessionDir(repo, request.SessionID), FindingsFileName) {
		t.Fatalf("unexpected draft path: %q", path)
	}
	if len(draft.QuestionResolutions) != len(request.ReviewQuestions) || len(draft.Findings) != 0 {
		t.Fatalf("unexpected draft: %#v", draft)
	}
	for i, resolution := range draft.QuestionResolutions {
		if resolution.QuestionID != request.ReviewQuestions[i].ID || resolution.Outcome != "" || resolution.Evidence != "" {
			t.Fatalf("draft resolution must be an unfilled scaffold: %#v", resolution)
		}
	}
	if _, _, err := CreateSubmissionDraft(repo, request.SessionID); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("draft creation must not overwrite agent work: %v", err)
	}
}

func TestSubmitAcceptsNearestTargetLineForDeletionOnlyFinding(t *testing.T) {
	repo := t.TempDir()
	content := "const slack = require('./slack');\nstart();\n"
	if err := os.WriteFile(filepath.Join(repo, "boot.js"), []byte(content), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	request, err := Prepare(repo, PrepareInput{
		Mode: ModeDiff,
		Units: []PreparedUnit{{ID: "unit-0001", Files: []PreparedFile{{
			Path: "boot.js",
			Diff: "@@ -1,3 +1,2 @@\n const slack = require('./slack');\n-slack.listen();\n start();",
		}}}},
	})
	if err != nil {
		t.Fatalf("Prepare() error: %v", err)
	}
	resolutions := make([]QuestionResolution, 0, len(request.ReviewQuestions))
	for _, question := range request.ReviewQuestions {
		index := 0
		resolutions = append(resolutions, QuestionResolution{
			QuestionID: question.ID, Outcome: "finding", Evidence: "The listener registration has no replacement.", FindingIndex: &index,
		})
	}
	result, err := Submit(repo, request.SessionID, Submission{
		ProtocolVersion:     ProtocolVersion,
		SessionID:           request.SessionID,
		QuestionResolutions: resolutions,
		Findings: []Finding{{
			UnitID: "unit-0001", File: "boot.js", StartLine: 2, EndLine: 2,
			Severity: "high", Category: "bug", Explanation: "Listener registration was removed.",
			Evidence: "No remaining production call installs the listener.", Confidence: 0.95,
		}},
	})
	if err != nil {
		t.Fatalf("Submit() error: %v", err)
	}
	if len(result.Rejected) != 0 || len(result.Findings) != 1 {
		t.Fatalf("deletion-only finding should anchor to the nearest target line: %#v", result)
	}
}

func TestPrepareRejectsUnknownReviewProfile(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "app.go"), []byte("package app\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	_, err := Prepare(repo, PrepareInput{
		Mode: ModeScan, Profile: "unsupported",
		Units: []PreparedUnit{{ID: "unit-0001", Files: []PreparedFile{{Path: "app.go"}}}},
	})
	if err == nil || !strings.Contains(err.Error(), "review profile") {
		t.Fatalf("Prepare() error = %v, want invalid profile error", err)
	}
}

func TestHandoffPromptDirectsIndependentDeepReview(t *testing.T) {
	repo, request := prepareDiffFixture(t)
	prompt, err := HandoffPrompt(request)
	if err != nil {
		t.Fatalf("HandoffPrompt() error: %v", err)
	}
	for _, want := range []string{
		"independent reviewer", repo, request.SessionID, "invariants", "dependencies", "contracts", "lifecycle", "verification", "critique",
		"Resolve every focused risk question", "question_resolutions", "acr review brief", "acr review draft", "acr review submit", "acr review render",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("handoff prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestSubmitNormalizesCorrectnessCategoryToBug(t *testing.T) {
	repo, request := prepareDiffFixture(t)

	result, err := Submit(repo, request.SessionID, Submission{
		ProtocolVersion: ProtocolVersion,
		SessionID:       request.SessionID,
		Findings: []Finding{{
			UnitID: "unit-0001", File: "app.go", StartLine: 2, EndLine: 2,
			Severity: "high", Category: "correctness",
			Explanation: "The active lease can be reused for the wrong contact.",
			Evidence:    "The lease is reused without validating its contact and sender.",
			Confidence:  0.95,
		}},
	})
	if err != nil {
		t.Fatalf("Submit() error: %v", err)
	}
	if len(result.Rejected) != 0 || len(result.Findings) != 1 {
		t.Fatalf("correctness finding should be accepted: %#v", result)
	}
	if result.Findings[0].Category != "bug" {
		t.Fatalf("correctness category should canonicalize to bug: %#v", result.Findings[0])
	}
}

func TestPrepareRejectsSymlinkedSource(t *testing.T) {
	repo := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.go")
	if err := os.WriteFile(outside, []byte("package secret\n"), 0o600); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(repo, "linked.go")); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	_, err := Prepare(repo, PrepareInput{
		Mode:  ModeScan,
		Units: []PreparedUnit{{ID: "unit-0001", Files: []PreparedFile{{Path: "linked.go"}}}},
	})
	if err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("Prepare() error = %v, want symbolic link rejection", err)
	}
}

func TestSubmitAcceptsChangedLineAndCollapsesDuplicates(t *testing.T) {
	repo, request := prepareDiffFixture(t)
	finding := Finding{
		UnitID:      "unit-0001",
		File:        "app.go",
		StartLine:   2,
		EndLine:     2,
		Severity:    "high",
		Category:    "bug",
		Explanation: "The changed function always fails.",
		Evidence:    "The return value is hard-coded.",
		Confidence:  0.95,
	}

	result, err := Submit(repo, request.SessionID, Submission{
		ProtocolVersion: ProtocolVersion,
		SessionID:       request.SessionID,
		Findings:        []Finding{finding, finding},
	})
	if err != nil {
		t.Fatalf("Submit() error: %v", err)
	}
	if len(result.Findings) != 1 || len(result.Rejected) != 0 {
		t.Fatalf("unexpected result: %#v", result)
	}
	if result.Summary.Accepted != 1 || result.Summary.Duplicates != 1 {
		t.Fatalf("unexpected summary: %#v", result.Summary)
	}
	if _, err := os.Stat(filepath.Join(repo, ".acr", "sessions", request.SessionID, ResultFileName)); err != nil {
		t.Fatalf("result packet missing: %v", err)
	}
}

func TestSubmitRejectsTraversalInvalidLineAndNonChangedLine(t *testing.T) {
	repo, request := prepareDiffFixture(t)
	base := Finding{
		UnitID:      "unit-0001",
		File:        "app.go",
		StartLine:   2,
		EndLine:     2,
		Severity:    "medium",
		Category:    "bug",
		Explanation: "A concrete defect.",
		Evidence:    "The changed statement demonstrates it.",
		Confidence:  0.8,
	}
	traversal := base
	traversal.File = "../outside.go"
	invalidLine := base
	invalidLine.StartLine, invalidLine.EndLine = 50, 50
	contextLine := base
	contextLine.StartLine, contextLine.EndLine = 1, 1

	result, err := Submit(repo, request.SessionID, Submission{
		ProtocolVersion: ProtocolVersion,
		SessionID:       request.SessionID,
		Findings:        []Finding{traversal, invalidLine, contextLine},
	})
	if err != nil {
		t.Fatalf("Submit() error: %v", err)
	}
	if len(result.Findings) != 0 || len(result.Rejected) != 3 {
		t.Fatalf("unexpected result: %#v", result)
	}
	codes := map[string]bool{}
	for _, rejected := range result.Rejected {
		codes[rejected.Code] = true
	}
	for _, code := range []string{"file_not_in_unit", "line_out_of_range", "line_not_changed"} {
		if !codes[code] {
			t.Errorf("missing rejection code %q in %#v", code, result.Rejected)
		}
	}
}

func TestSubmitRejectsStaleFile(t *testing.T) {
	repo, request := prepareDiffFixture(t)
	if err := os.WriteFile(filepath.Join(repo, "app.go"), []byte("package main\nfunc changedAgain() {}\n"), 0o644); err != nil {
		t.Fatalf("change source: %v", err)
	}

	result, err := Submit(repo, request.SessionID, Submission{
		ProtocolVersion: ProtocolVersion,
		SessionID:       request.SessionID,
		Findings: []Finding{{
			UnitID: "unit-0001", File: "app.go", StartLine: 2, EndLine: 2,
			Severity: "high", Category: "bug", Explanation: "Stale finding.",
			Evidence: "Prepared content no longer matches.", Confidence: 0.9,
		}},
	})
	if err != nil {
		t.Fatalf("Submit() error: %v", err)
	}
	if len(result.Rejected) != 1 || result.Rejected[0].Code != "file_changed" {
		t.Fatalf("unexpected stale result: %#v", result)
	}
}

func TestSubmitRejectsStaleSessionWithNoFindings(t *testing.T) {
	repo, request := prepareDiffFixture(t)
	if err := os.WriteFile(filepath.Join(repo, "app.go"), []byte("package main\nfunc changedAgain() {}\n"), 0o644); err != nil {
		t.Fatalf("change source: %v", err)
	}

	result, err := Submit(repo, request.SessionID, Submission{
		ProtocolVersion: ProtocolVersion,
		SessionID:       request.SessionID,
		Findings:        []Finding{},
	})
	if err != nil {
		t.Fatalf("Submit() error: %v", err)
	}
	if len(result.Rejected) != 1 || result.Rejected[0].Index != -1 || result.Rejected[0].Code != "file_changed" {
		t.Fatalf("unexpected stale result: %#v", result)
	}
}

func TestRenderMarkdownUsesValidatedFindings(t *testing.T) {
	result := Result{
		ProtocolVersion: ProtocolVersion,
		SessionID:       "session-1",
		QuestionResolutions: []QuestionResolution{{
			QuestionID: "question-0001", Outcome: "no_finding", Evidence: "The callee accepts an omitted option.",
		}},
		Findings: []Finding{{
			File: "app.go", StartLine: 2, EndLine: 2, Severity: "high", Category: "bug",
			Explanation: "Always fails.", Evidence: "The changed return is false.", Confidence: 0.95,
		}},
		Summary: ResultSummary{Accepted: 1},
	}

	markdown, err := RenderMarkdown(result)
	if err != nil {
		t.Fatalf("RenderMarkdown() error: %v", err)
	}
	for _, want := range []string{"# Agent Code Review", "app.go:2", "Always fails.", "Review coverage", "question-0001", "The callee accepts", "1 validated finding"} {
		if !strings.Contains(markdown, want) {
			t.Errorf("markdown missing %q:\n%s", want, markdown)
		}
	}
}

func prepareDiffFixture(t *testing.T) (string, *Request) {
	t.Helper()
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "app.go"), []byte("package main\nfunc changed() bool { return false }\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	request, err := Prepare(repo, PrepareInput{
		Mode: ModeDiff,
		Units: []PreparedUnit{{
			ID: "unit-0001",
			Files: []PreparedFile{{
				Path: "app.go",
				Diff: "@@ -1,2 +1,2 @@\n package main\n-func old() bool { return true }\n+func changed() bool { return false }",
			}},
		}},
	})
	if err != nil {
		t.Fatalf("Prepare() error: %v", err)
	}
	return repo, request
}

func prepareRiskFixture(t *testing.T) (string, *Request) {
	t.Helper()
	repo := t.TempDir()
	content := "const slack = require('./slack');\nawait Promise.all([\n    emailAddressService.init(),\n    scheduling.init(),\n]);\n"
	if err := os.WriteFile(filepath.Join(repo, "boot.js"), []byte(content), 0o644); err != nil {
		t.Fatalf("write risk fixture: %v", err)
	}
	request, err := Prepare(repo, PrepareInput{
		Mode: ModeDiff,
		Units: []PreparedUnit{{ID: "unit-0001", Files: []PreparedFile{{
			Path: "boot.js",
			Diff: "@@ -1,8 +1,5 @@\n const slack = require('./slack');\n-await emailAddressService.init();\n-slack.listen();\n await Promise.all([\n+    emailAddressService.init(),\n-    scheduling.init({apiUrl: adminUrl});\n+    scheduling.init(),\n ]);",
		}}}},
	})
	if err != nil {
		t.Fatalf("Prepare() risk fixture error: %v", err)
	}
	return repo, request
}

func hasRejectionCode(rejections []Rejection, want string) bool {
	for _, rejection := range rejections {
		if rejection.Code == want {
			return true
		}
	}
	return false
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
