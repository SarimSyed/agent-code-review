// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package delegation

import (
	"fmt"
	"path/filepath"
	"strings"
)

// HandoffPrompt produces a portable prompt for a separate review-agent task.
// It contains the immutable packet location and the review passes, but never
// supplies a model or asks an external provider to perform the review.
func HandoffPrompt(request *Request) (string, error) {
	if request == nil {
		return "", fmt.Errorf("review request is required")
	}
	if strings.TrimSpace(request.Repository.Root) == "" || strings.TrimSpace(request.SessionID) == "" {
		return "", fmt.Errorf("review request is missing repository or session identity")
	}
	dir := SessionDir(request.Repository.Root, request.SessionID)
	var out strings.Builder
	fmt.Fprintln(&out, "You are the independent reviewer for this ACR session.")
	fmt.Fprintln(&out, "Do not rely on conclusions from the author task. Do not modify source files.")
	fmt.Fprintln(&out)
	fmt.Fprintf(&out, "Repository: %s\n", request.Repository.Root)
	if request.Repository.Revision != "" {
		fmt.Fprintf(&out, "Prepared revision: %s\n", request.Repository.Revision)
	}
	fmt.Fprintf(&out, "Session: %s\n", request.SessionID)
	fmt.Fprintf(&out, "Packet: %s\n", filepath.Join(dir, RequestFileName))
	fmt.Fprintln(&out)
	fmt.Fprintf(&out, "Use the %s profile and complete every required pass:\n", request.Instructions.ReviewProfile)
	for _, pass := range request.Instructions.RequiredPasses {
		fmt.Fprintf(&out, "- %s: %s\n", pass.ID, pass.Objective)
	}
	fmt.Fprintln(&out)
	fmt.Fprintln(&out, "Start with the compact briefing rather than printing the raw packet. Resolve every focused risk question; each question is a lead to verify, not a presumed defect.")
	fmt.Fprintln(&out, "Include evidence-backed question_resolutions plus findings in the submission. Inspect every listed file directly, submit only concrete defects, and then run:")
	fmt.Fprintf(&out, "acr review brief --repo %s --session %s\n", request.Repository.Root, request.SessionID)
	fmt.Fprintf(&out, "acr review draft --repo %s --session %s\n", request.Repository.Root, request.SessionID)
	fmt.Fprintf(&out, "acr review submit --repo %s --session %s --input %s --render\n", request.Repository.Root, request.SessionID, filepath.Join(dir, FindingsFileName))
	out.WriteString("On successful validation, present the Markdown emitted by that command. Do not stop at a validation summary or tell the user to run a render command.\n")
	return out.String(), nil
}
