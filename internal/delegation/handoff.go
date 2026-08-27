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
	if request.Instructions.ReviewProfile == ReviewProfileDeep && request.ProtocolVersion == ProtocolVersion {
		return phasedHandoffPrompt(request, dir), nil
	}
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

func phasedHandoffPrompt(request *Request, dir string) string {
	compact := request.Instructions.TokenEconomy.Mode == TokenEconomyCaveman
	var out strings.Builder
	if compact {
		fmt.Fprintln(&out, "Independent ACR reviewer. Do not modify source. Ignore author conclusions.")
		fmt.Fprintf(&out, "Repo %s. Session %s.\n", request.Repository.Root, request.SessionID)
		fmt.Fprintf(&out, "Use installed caveman skill level %s; if absent, compact fallback. Never lose technical evidence.\n", request.Instructions.TokenEconomy.Level)
		fmt.Fprintln(&out, "Loop: claim `acr review phase next`; fill returned draft; submit with `acr review phase submit`. Intent, impact, candidates first. Use fresh critic context when candidates exist; same model, different context ID. If host cannot, same-context fallback and label it.")
		fmt.Fprintln(&out, "Resolve every focused risk question. After workflow ready, run draft then final submit. Present emitted Markdown; never stop at validation summary.")
	} else {
		fmt.Fprintln(&out, "You are the independent reviewer for this ACR session.")
		fmt.Fprintln(&out, "Do not rely on conclusions from the author task. Do not modify source files.")
		fmt.Fprintln(&out)
		fmt.Fprintf(&out, "Repository: %s\nSession: %s\nPacket: %s\n\n", request.Repository.Root, request.SessionID, filepath.Join(dir, RequestFileName))
		fmt.Fprintln(&out, "Complete every persisted phase in order: intent, impact, candidates, critique, then finalization. Resolve every focused risk question with concrete evidence.")
		fmt.Fprintln(&out, "Required review passes:")
		for _, pass := range request.Instructions.RequiredPasses {
			fmt.Fprintf(&out, "- %s: %s\n", pass.ID, pass.Objective)
		}
		fmt.Fprintln(&out, "For critique, launch a fresh critic context using the same active host model when possible. Give it the returned blinded critic prompt. If the host cannot create a fresh context, use same_context fallback and disclose that limitation.")
		fmt.Fprintln(&out, "Repeatedly claim a task, fill its non-overwriting draft, and submit it. Resume until phase status is ready.")
	}
	fmt.Fprintf(&out, "acr review phase next --repo %s --session %s --worker <context-id>\n", request.Repository.Root, request.SessionID)
	fmt.Fprintf(&out, "acr review phase submit --repo %s --session %s --task <task-id> --input <input-path>\n", request.Repository.Root, request.SessionID)
	fmt.Fprintf(&out, "acr review phase status --repo %s --session %s\n", request.Repository.Root, request.SessionID)
	fmt.Fprintf(&out, "acr review draft --repo %s --session %s\n", request.Repository.Root, request.SessionID)
	fmt.Fprintf(&out, "acr review submit --repo %s --session %s --input %s --render\n", request.Repository.Root, request.SessionID, filepath.Join(dir, FindingsFileName))
	out.WriteString("On successful validation, present Markdown emitted by final submit. Do not stop at a validation summary or tell user to run render.\n")
	return out.String()
}
