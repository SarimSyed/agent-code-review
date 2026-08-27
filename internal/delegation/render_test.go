// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package delegation

import (
	"strings"
	"testing"
)

func TestRenderMarkdownWithPerFindingFixPromptsScopesEachPrompt(t *testing.T) {
	result := fixPromptResult()

	markdown, err := RenderMarkdownWithOptions(result, RenderMarkdownOptions{FixPromptMode: FixPromptPerFinding})
	if err != nil {
		t.Fatalf("RenderMarkdownWithOptions() error: %v", err)
	}

	if got := strings.Count(markdown, "### Copyable fix prompt"); got != len(result.Findings) {
		t.Fatalf("fix prompt count = %d, want %d:\n%s", got, len(result.Findings), markdown)
	}
	for _, want := range []string{
		"Work on exactly one validated ACR finding.",
		"Do not address other ACR findings in this change.",
		"File: app.go",
		"File: worker.go",
		"Suggested approach: Return the computed value.",
		"Add or update a regression test",
	} {
		if !strings.Contains(markdown, want) {
			t.Errorf("markdown missing %q:\n%s", want, markdown)
		}
	}
}

func TestRenderMarkdownWithCombinedFixPromptProducesOnePrompt(t *testing.T) {
	result := fixPromptResult()

	markdown, err := RenderMarkdownWithOptions(result, RenderMarkdownOptions{FixPromptMode: FixPromptCombined})
	if err != nil {
		t.Fatalf("RenderMarkdownWithOptions() error: %v", err)
	}

	if got := strings.Count(markdown, "## Copyable combined fix prompt"); got != 1 {
		t.Fatalf("combined prompt count = %d, want 1:\n%s", got, markdown)
	}
	for _, want := range []string{"Finding 1", "Finding 2", "app.go:12", "worker.go:30-32"} {
		if !strings.Contains(markdown, want) {
			t.Errorf("markdown missing %q:\n%s", want, markdown)
		}
	}
}

func TestRenderMarkdownWithOptionsRejectsUnsupportedFixPromptMode(t *testing.T) {
	_, err := RenderMarkdownWithOptions(fixPromptResult(), RenderMarkdownOptions{FixPromptMode: "all-at-once"})
	if err == nil || !strings.Contains(err.Error(), "unsupported fix prompt mode") {
		t.Fatalf("error = %v, want unsupported fix prompt mode", err)
	}
}

func TestRenderMarkdownWithoutFixPromptKeepsExistingOutput(t *testing.T) {
	markdown, err := RenderMarkdownWithOptions(fixPromptResult(), RenderMarkdownOptions{})
	if err != nil {
		t.Fatalf("RenderMarkdownWithOptions() error: %v", err)
	}
	if strings.Contains(markdown, "Copyable") {
		t.Fatalf("default rendering unexpectedly included a fix prompt:\n%s", markdown)
	}
}

func fixPromptResult() Result {
	return Result{
		ProtocolVersion: ProtocolVersion,
		SessionID:       "session-fix-prompts",
		Findings: []Finding{
			{
				UnitID: "unit-1", File: "app.go", StartLine: 12, EndLine: 12,
				Severity: "high", Category: "bug", Explanation: "The function always returns zero.",
				Evidence: "The computed value is discarded.", SuggestedFix: "Return the computed value.", Confidence: 0.98,
			},
			{
				UnitID: "unit-2", File: "worker.go", StartLine: 30, EndLine: 32,
				Severity: "medium", Category: "performance", Explanation: "The work now runs serially.",
				Evidence: "Each independent operation is awaited before the next starts.", Confidence: 0.85,
			},
		},
		Summary: ResultSummary{Accepted: 2},
	}
}
