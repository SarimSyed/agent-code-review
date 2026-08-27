// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package delegation

import (
	"fmt"
	"strings"
)

func RenderMarkdown(result Result) (string, error) {
	var out strings.Builder
	out.WriteString("# Agent Code Review\n\n")
	fmt.Fprintf(&out, "Session: `%s`\n\n", result.SessionID)
	if len(result.Findings) == 0 {
		out.WriteString("No validated findings.\n")
	} else {
		for _, finding := range result.Findings {
			location := fmt.Sprintf("%s:%d", finding.File, finding.StartLine)
			if finding.EndLine != finding.StartLine {
				location = fmt.Sprintf("%s:%d-%d", finding.File, finding.StartLine, finding.EndLine)
			}
			fmt.Fprintf(&out, "## [%s] %s at `%s`\n\n", strings.ToUpper(finding.Severity), finding.Category, location)
			fmt.Fprintf(&out, "%s\n\nEvidence: %s\n\nConfidence: %.2f\n\n", finding.Explanation, finding.Evidence, finding.Confidence)
			if finding.SuggestedFix != "" {
				fmt.Fprintf(&out, "Suggested fix: %s\n\n", finding.SuggestedFix)
			}
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
