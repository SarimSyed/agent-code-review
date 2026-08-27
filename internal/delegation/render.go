// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package delegation

import (
	"fmt"
	"strings"
)

type FixPromptMode string

const (
	FixPromptNone       FixPromptMode = ""
	FixPromptPerFinding FixPromptMode = "per-finding"
	FixPromptCombined   FixPromptMode = "combined"
)

type RenderMarkdownOptions struct {
	FixPromptMode FixPromptMode
}

func RenderMarkdown(result Result) (string, error) {
	return RenderMarkdownWithOptions(result, RenderMarkdownOptions{})
}

func RenderMarkdownWithOptions(result Result, options RenderMarkdownOptions) (string, error) {
	if options.FixPromptMode != FixPromptNone && options.FixPromptMode != FixPromptPerFinding && options.FixPromptMode != FixPromptCombined {
		return "", fmt.Errorf("unsupported fix prompt mode %q; use per-finding or combined", options.FixPromptMode)
	}

	var out strings.Builder
	out.WriteString("# Agent Code Review\n\n")
	fmt.Fprintf(&out, "Session: `%s`\n\n", result.SessionID)
	if len(result.Findings) == 0 {
		out.WriteString("No validated findings.\n")
	} else {
		for index, finding := range result.Findings {
			location := findingLocation(finding)
			fmt.Fprintf(&out, "## [%s] %s at `%s`\n\n", strings.ToUpper(finding.Severity), finding.Category, location)
			fmt.Fprintf(&out, "%s\n\nEvidence: %s\n\nConfidence: %.2f\n\n", finding.Explanation, finding.Evidence, finding.Confidence)
			if finding.SuggestedFix != "" {
				fmt.Fprintf(&out, "Suggested fix: %s\n\n", finding.SuggestedFix)
			}
			if options.FixPromptMode == FixPromptPerFinding {
				out.WriteString("### Copyable fix prompt\n\n")
				out.WriteString(markdownCodeBlock(perFindingFixPrompt(result.SessionID, index+1, finding)))
			}
		}
		if options.FixPromptMode == FixPromptCombined {
			out.WriteString("## Copyable combined fix prompt\n\n")
			out.WriteString(markdownCodeBlock(combinedFixPrompt(result)))
		}
	}
	if len(result.QuestionResolutions) > 0 {
		out.WriteString("## Review coverage\n\n")
		for _, resolution := range result.QuestionResolutions {
			fmt.Fprintf(&out, "- `%s` — **%s**: %s\n", resolution.QuestionID, strings.ReplaceAll(resolution.Outcome, "_", " "), resolution.Evidence)
		}
		out.WriteString("\n")
	}
	noun := "findings"
	if result.Summary.Accepted == 1 {
		noun = "finding"
	}
	fmt.Fprintf(&out, "%d validated %s; %d rejected; %d duplicates removed.\n",
		result.Summary.Accepted, noun, result.Summary.Rejected, result.Summary.Duplicates)
	return out.String(), nil
}

func findingLocation(finding Finding) string {
	if finding.EndLine != finding.StartLine {
		return fmt.Sprintf("%s:%d-%d", finding.File, finding.StartLine, finding.EndLine)
	}
	return fmt.Sprintf("%s:%d", finding.File, finding.StartLine)
}

func perFindingFixPrompt(sessionID string, findingNumber int, finding Finding) string {
	var prompt strings.Builder
	prompt.WriteString("Work on exactly one validated ACR finding. Do not address other ACR findings in this change.\n\n")
	fmt.Fprintf(&prompt, "Finding %d from ACR session %s:\n", findingNumber, sessionID)
	fmt.Fprintf(&prompt, "- File: %s\n", finding.File)
	fmt.Fprintf(&prompt, "- Lines: %d-%d\n", finding.StartLine, finding.EndLine)
	fmt.Fprintf(&prompt, "- Severity: %s\n", finding.Severity)
	fmt.Fprintf(&prompt, "- Category: %s\n", finding.Category)
	fmt.Fprintf(&prompt, "- Problem: %s\n", finding.Explanation)
	fmt.Fprintf(&prompt, "- Evidence: %s\n", finding.Evidence)
	if finding.SuggestedFix != "" {
		fmt.Fprintf(&prompt, "- Suggested approach: %s (Treat this as a hypothesis and verify it against the code.)\n", finding.SuggestedFix)
	}
	prompt.WriteString("\nInstructions:\n")
	prompt.WriteString("1. Inspect the referenced code, surrounding control flow, and relevant call sites.\n")
	prompt.WriteString("2. Confirm the problem still exists before editing. If the finding is stale or incorrect, stop and explain why.\n")
	prompt.WriteString("3. Implement the smallest complete fix without unrelated refactoring.\n")
	prompt.WriteString("4. Add or update a regression test that fails without the fix.\n")
	prompt.WriteString("5. Run relevant formatting, tests, and static checks.\n")
	prompt.WriteString("6. Summarize the change and the verification performed.\n")
	return prompt.String()
}

func combinedFixPrompt(result Result) string {
	var prompt strings.Builder
	prompt.WriteString("Fix the validated ACR findings below. Handle one finding at a time as a small, independently verifiable change.\n\n")
	fmt.Fprintf(&prompt, "ACR session: %s\n\n", result.SessionID)
	for index, finding := range result.Findings {
		fmt.Fprintf(&prompt, "Finding %d\n", index+1)
		fmt.Fprintf(&prompt, "- Location: %s\n", findingLocation(finding))
		fmt.Fprintf(&prompt, "- Severity: %s\n", finding.Severity)
		fmt.Fprintf(&prompt, "- Category: %s\n", finding.Category)
		fmt.Fprintf(&prompt, "- Problem: %s\n", finding.Explanation)
		fmt.Fprintf(&prompt, "- Evidence: %s\n", finding.Evidence)
		if finding.SuggestedFix != "" {
			fmt.Fprintf(&prompt, "- Suggested approach: %s (Verify this against the code.)\n", finding.SuggestedFix)
		}
		prompt.WriteString("\n")
	}
	prompt.WriteString("For each finding:\n")
	prompt.WriteString("1. Inspect the referenced code and relevant call sites, then confirm the problem still exists.\n")
	prompt.WriteString("2. Implement the smallest complete fix without unrelated refactoring.\n")
	prompt.WriteString("3. Add or update a regression test that fails without the fix.\n")
	prompt.WriteString("4. Run relevant formatting, tests, and static checks before moving to the next finding.\n")
	prompt.WriteString("5. If a finding is stale or incorrect, do not force a change; explain why.\n")
	prompt.WriteString("6. Summarize each change and its verification separately.\n")
	return prompt.String()
}

func markdownCodeBlock(content string) string {
	longestRun := 0
	currentRun := 0
	for _, character := range content {
		if character == '`' {
			currentRun++
			if currentRun > longestRun {
				longestRun = currentRun
			}
		} else {
			currentRun = 0
		}
	}
	fenceLength := 3
	if longestRun >= fenceLength {
		fenceLength = longestRun + 1
	}
	fence := strings.Repeat("`", fenceLength)
	return fence + "text\n" + strings.TrimRight(content, "\n") + "\n" + fence + "\n\n"
}
