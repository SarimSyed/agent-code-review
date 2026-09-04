// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

// Package mcpserver exposes the local delegation protocol as MCP tools. The
// server is intentionally model-free: the connected host remains the LLM.
package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/alibaba/open-code-review/internal/delegation"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func New(version string) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "agent-code-review", Version: version}, nil)
	addTool(server, "review_prepare", "Prepare deterministic review units for the active host agent.", prepareSchema(), handlePrepare)
	addTool(server, "review_get_request", "Load a prepared review packet.", sessionSchema(), handleGetRequest)
	addTool(server, "review_get_briefing", "Load the compact risk questions, review passes, files, and changed-line anchors for a prepared review.", sessionSchema(), handleGetBriefing)
	addTool(server, "review_create_draft", "Create a non-overwriting submission form with one slot per focused risk question.", sessionSchema(), handleCreateDraft)
	addTool(server, "review_get_unit", "Retrieve one prepared review unit by ID.", unitSchema(), handleGetUnit)
	addTool(server, "review_submit_findings", "Validate findings and optionally return the completed Markdown report in the same call.", submitSchema(), handleSubmit)
	addTool(server, "review_validate_findings", "Validate findings and optionally return the completed Markdown report in the same call.", submitSchema(), handleSubmit)
	addTool(server, "review_get_result", "Load a validated review result.", sessionSchema(), handleGetResult)
	addTool(server, "review_render", "Render a validated review result as Markdown or JSON.", renderSchema(), handleRender)
	addTool(server, "review_handoff", "Create a prompt for an independent reviewer task from a prepared session.", sessionSchema(), handleHandoff)
	addTool(server, "review_session_list", "List delegated review sessions in a repository.", repoSchema(), handleList)
	addTool(server, "review_phase_next", "Claim the next ready deep-review phase.", phaseNextSchema(), handlePhaseNext)
	addTool(server, "review_phase_submit", "Validate and persist one deep-review phase artifact.", phaseSubmitSchema(), handlePhaseSubmit)
	addTool(server, "review_phase_status", "Load deep-review workflow state.", sessionSchema(), handlePhaseStatus)
	return server
}

func addTool(server *mcp.Server, name, description string, schema map[string]any, handler func(context.Context, json.RawMessage) (any, error)) {
	server.AddTool(&mcp.Tool{Name: name, Description: description, InputSchema: schema},
		func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			value, err := handler(ctx, request.Params.Arguments)
			if err != nil {
				return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, nil
			}
			data, err := json.MarshalIndent(value, "", "  ")
			if err != nil {
				return nil, err
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(data)}}}, nil
		})
}

type prepareArgs struct {
	Repo         string   `json:"repo"`
	From         string   `json:"from,omitempty"`
	To           string   `json:"to,omitempty"`
	Commit       string   `json:"commit,omitempty"`
	Paths        []string `json:"paths,omitempty"`
	RulePath     string   `json:"rule_path,omitempty"`
	Background   string   `json:"background,omitempty"`
	Profile      string   `json:"profile,omitempty"`
	Caveman      bool     `json:"caveman,omitempty"`
	CavemanLevel string   `json:"caveman_level,omitempty"`
}

type sessionArgs struct {
	Repo      string `json:"repo"`
	SessionID string `json:"session_id"`
}

type submitArgs struct {
	Repo       string                `json:"repo"`
	SessionID  string                `json:"session_id"`
	Submission delegation.Submission `json:"submission"`
	Render     bool                  `json:"render,omitempty"`
	FixPrompt  string                `json:"fix_prompt,omitempty"`
}

type unitArgs struct {
	Repo      string `json:"repo"`
	SessionID string `json:"session_id"`
	UnitID    string `json:"unit_id"`
}

type renderArgs struct {
	Repo      string `json:"repo"`
	SessionID string `json:"session_id"`
	Format    string `json:"format,omitempty"`
	FixPrompt string `json:"fix_prompt,omitempty"`
}

type phaseNextArgs struct {
	Repo      string `json:"repo"`
	SessionID string `json:"session_id"`
	Worker    string `json:"worker"`
	All       bool   `json:"all,omitempty"`
}

type phaseSubmitArgs struct {
	Repo        string                       `json:"repo"`
	SessionID   string                       `json:"session_id"`
	TaskID      string                       `json:"task_id"`
	Submission  delegation.PhaseSubmission   `json:"submission"`
	Submissions []delegation.PhaseSubmission `json:"submissions,omitempty"`
}

func handlePrepare(ctx context.Context, raw json.RawMessage) (any, error) {
	var args prepareArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, fmt.Errorf("decode review_prepare arguments: %w", err)
	}
	if !args.Caveman && args.CavemanLevel != "" {
		return nil, fmt.Errorf("caveman_level requires caveman")
	}
	policy := delegation.TokenEconomy{Mode: delegation.TokenEconomyNormal}
	if args.Caveman {
		policy = delegation.TokenEconomy{Mode: delegation.TokenEconomyCaveman, Level: args.CavemanLevel}
	}
	return delegation.Build(ctx, delegation.BuildOptions{
		RepoDir: args.Repo, From: args.From, To: args.To, Commit: args.Commit,
		Paths: args.Paths, RulePath: args.RulePath, Background: args.Background,
		Profile: args.Profile, TokenEconomy: policy,
	})
}

func handlePhaseNext(_ context.Context, raw json.RawMessage) (any, error) {
	var args phaseNextArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, fmt.Errorf("decode review_phase_next arguments: %w", err)
	}
	if args.Repo == "" || args.SessionID == "" || args.Worker == "" {
		return nil, fmt.Errorf("repo, session_id, and worker are required")
	}
	if args.All {
		tasks, err := delegation.ClaimReadyPhases(args.Repo, args.SessionID, args.Worker, time.Now().UTC(), 15*time.Minute)
		if err != nil {
			return nil, err
		}
		prompts := make([]string, 0, len(tasks))
		drafts := make([]delegation.PhaseSubmission, 0, len(tasks))
		paths := make([]string, 0, len(tasks))
		protocol := ""
		for _, task := range tasks {
			prompt, promptErr := delegation.PhasePrompt(args.Repo, args.SessionID, task.ID)
			if promptErr != nil {
				return nil, promptErr
			}
			draft, path, draftErr := delegation.CreatePhaseDraft(args.Repo, args.SessionID, task.ID)
			if draftErr != nil {
				return nil, draftErr
			}
			protocol = draft.ProtocolVersion
			prompts = append(prompts, prompt)
			drafts = append(drafts, *draft)
			paths = append(paths, path)
		}
		return map[string]any{"protocol_version": protocol, "session_id": args.SessionID, "tasks": tasks, "prompt": strings.Join(prompts, "\n"), "prompts": prompts, "input_paths": paths, "submissions": drafts}, nil
	}
	task, err := delegation.ClaimNextPhase(args.Repo, args.SessionID, args.Worker, time.Now().UTC(), 15*time.Minute)
	if err != nil {
		return nil, err
	}
	prompt, err := delegation.PhasePrompt(args.Repo, args.SessionID, task.ID)
	if err != nil {
		return nil, err
	}
	draft, inputPath, err := delegation.CreatePhaseDraft(args.Repo, args.SessionID, task.ID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"protocol_version": draft.ProtocolVersion, "session_id": args.SessionID, "task": task, "prompt": prompt, "input_path": inputPath, "submission": draft}, nil
}

func handlePhaseSubmit(_ context.Context, raw json.RawMessage) (any, error) {
	var args phaseSubmitArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, fmt.Errorf("decode review_phase_submit arguments: %w", err)
	}
	if args.Repo == "" || args.SessionID == "" {
		return nil, fmt.Errorf("repo and session_id are required")
	}
	if len(args.Submissions) > 0 {
		if args.TaskID != "" || args.Submission.TaskID != "" {
			return nil, fmt.Errorf("submissions is mutually exclusive with task_id and submission")
		}
		if len(args.Submissions) == 0 {
			return nil, fmt.Errorf("submissions must not be empty")
		}
		return delegation.SubmitPhaseBatch(args.Repo, args.SessionID, delegation.PhaseBatchSubmission{ProtocolVersion: args.Submissions[0].ProtocolVersion, SessionID: args.SessionID, Submissions: args.Submissions})
	}
	if args.TaskID == "" || args.Submission.TaskID == "" {
		return nil, fmt.Errorf("task_id and submission are required")
	}
	return delegation.SubmitPhase(args.Repo, args.SessionID, args.TaskID, args.Submission)
}

func handlePhaseStatus(_ context.Context, raw json.RawMessage) (any, error) {
	args, err := decodeSessionArgs(raw)
	if err != nil {
		return nil, err
	}
	return delegation.LoadWorkflow(args.Repo, args.SessionID)
}

func handleHandoff(_ context.Context, raw json.RawMessage) (any, error) {
	args, err := decodeSessionArgs(raw)
	if err != nil {
		return nil, err
	}
	request, err := delegation.LoadRequest(args.Repo, args.SessionID)
	if err != nil {
		return nil, err
	}
	prompt, err := delegation.HandoffPrompt(request)
	if err != nil {
		return nil, err
	}
	return map[string]string{"prompt": prompt}, nil
}

func handleGetRequest(_ context.Context, raw json.RawMessage) (any, error) {
	args, err := decodeSessionArgs(raw)
	if err != nil {
		return nil, err
	}
	return delegation.LoadRequest(args.Repo, args.SessionID)
}

func handleGetBriefing(_ context.Context, raw json.RawMessage) (any, error) {
	args, err := decodeSessionArgs(raw)
	if err != nil {
		return nil, err
	}
	return delegation.Brief(args.Repo, args.SessionID)
}

func handleCreateDraft(_ context.Context, raw json.RawMessage) (any, error) {
	args, err := decodeSessionArgs(raw)
	if err != nil {
		return nil, err
	}
	draft, path, err := delegation.CreateSubmissionDraft(args.Repo, args.SessionID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"findings_path": path, "submission": draft}, nil
}

func handleGetUnit(_ context.Context, raw json.RawMessage) (any, error) {
	var args unitArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, fmt.Errorf("decode review_get_unit arguments: %w", err)
	}
	if args.Repo == "" || args.SessionID == "" || args.UnitID == "" {
		return nil, fmt.Errorf("repo, session_id, and unit_id are required")
	}
	request, err := delegation.LoadRequest(args.Repo, args.SessionID)
	if err != nil {
		return nil, err
	}
	for _, unit := range request.Units {
		if unit.ID == args.UnitID {
			return unit, nil
		}
	}
	return nil, fmt.Errorf("review unit %q not found", args.UnitID)
}

func handleSubmit(_ context.Context, raw json.RawMessage) (any, error) {
	var args submitArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, fmt.Errorf("decode review_submit_findings arguments: %w", err)
	}
	result, err := delegation.Submit(args.Repo, args.SessionID, args.Submission)
	if err != nil || !args.Render || len(result.Rejected) > 0 {
		return result, err
	}
	mode := delegation.FixPromptPerFinding
	switch strings.ToLower(strings.TrimSpace(args.FixPrompt)) {
	case "", string(delegation.FixPromptPerFinding):
	case string(delegation.FixPromptCombined):
		mode = delegation.FixPromptCombined
	case "none":
		mode = delegation.FixPromptNone
	default:
		return nil, fmt.Errorf("unsupported fix prompt mode %q; use per-finding, combined, or none", args.FixPrompt)
	}
	markdown, err := delegation.RenderMarkdownWithOptions(*result, delegation.RenderMarkdownOptions{FixPromptMode: mode})
	if err != nil {
		return nil, err
	}
	return map[string]any{"format": "markdown", "content": markdown, "result": result}, nil
}

func handleGetResult(_ context.Context, raw json.RawMessage) (any, error) {
	args, err := decodeSessionArgs(raw)
	if err != nil {
		return nil, err
	}
	return delegation.LoadResult(args.Repo, args.SessionID)
}

func handleRender(_ context.Context, raw json.RawMessage) (any, error) {
	var args renderArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, fmt.Errorf("decode review_render arguments: %w", err)
	}
	result, err := delegation.LoadResult(args.Repo, args.SessionID)
	if err != nil {
		return nil, err
	}
	if args.Format == "" || args.Format == "markdown" || args.Format == "md" {
		markdown, err := delegation.RenderMarkdownWithOptions(*result, delegation.RenderMarkdownOptions{
			FixPromptMode: delegation.FixPromptMode(strings.ToLower(args.FixPrompt)),
		})
		if err != nil {
			return nil, err
		}
		return map[string]string{"format": "markdown", "content": markdown}, nil
	}
	if args.Format == "json" {
		if args.FixPrompt != "" {
			return nil, fmt.Errorf("fix_prompt requires Markdown format")
		}
		return result, nil
	}
	return nil, fmt.Errorf("unsupported format %q", args.Format)
}

func handleList(_ context.Context, raw json.RawMessage) (any, error) {
	var args struct {
		Repo string `json:"repo"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, fmt.Errorf("decode review_session_list arguments: %w", err)
	}
	return delegation.ListSessions(args.Repo)
}

func decodeSessionArgs(raw json.RawMessage) (sessionArgs, error) {
	var args sessionArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return args, fmt.Errorf("decode session arguments: %w", err)
	}
	if args.Repo == "" || args.SessionID == "" {
		return args, fmt.Errorf("repo and session_id are required")
	}
	return args, nil
}

func prepareSchema() map[string]any {
	return objectSchema(map[string]any{
		"repo": map[string]any{"type": "string"}, "from": map[string]any{"type": "string"},
		"to": map[string]any{"type": "string"}, "commit": map[string]any{"type": "string"},
		"paths":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"rule_path": map[string]any{"type": "string"}, "background": map[string]any{"type": "string"},
		"profile":       map[string]any{"type": "string", "enum": []string{delegation.ReviewProfileDeep, delegation.ReviewProfileAdaptive, delegation.ReviewProfileStandard}},
		"caveman":       map[string]any{"type": "boolean"},
		"caveman_level": map[string]any{"type": "string", "enum": []string{delegation.CavemanLite, delegation.CavemanFull, delegation.CavemanUltra}},
	}, []string{"repo"})
}

func phaseNextSchema() map[string]any {
	return objectSchema(map[string]any{
		"repo": map[string]any{"type": "string"}, "session_id": map[string]any{"type": "string"}, "worker": map[string]any{"type": "string"},
		"all": map[string]any{"type": "boolean"},
	}, []string{"repo", "session_id", "worker"})
}

func phaseSubmitSchema() map[string]any {
	return objectSchema(map[string]any{
		"repo": map[string]any{"type": "string"}, "session_id": map[string]any{"type": "string"},
		"task_id": map[string]any{"type": "string"}, "submission": map[string]any{"type": "object"},
		"submissions": map[string]any{"type": "array", "minItems": 1, "items": map[string]any{"type": "object"}},
	}, []string{"repo", "session_id"})
}

func sessionSchema() map[string]any {
	return objectSchema(map[string]any{
		"repo": map[string]any{"type": "string"}, "session_id": map[string]any{"type": "string"},
	}, []string{"repo", "session_id"})
}

func submitSchema() map[string]any {
	return objectSchema(map[string]any{
		"repo": map[string]any{"type": "string"}, "session_id": map[string]any{"type": "string"},
		"submission": map[string]any{"type": "object"},
		"render":     map[string]any{"type": "boolean"},
		"fix_prompt": map[string]any{"type": "string", "enum": []string{"per-finding", "combined", "none"}},
	}, []string{"repo", "session_id", "submission"})
}

func unitSchema() map[string]any {
	return objectSchema(map[string]any{
		"repo": map[string]any{"type": "string"}, "session_id": map[string]any{"type": "string"},
		"unit_id": map[string]any{"type": "string"},
	}, []string{"repo", "session_id", "unit_id"})
}

func renderSchema() map[string]any {
	return objectSchema(map[string]any{
		"repo": map[string]any{"type": "string"}, "session_id": map[string]any{"type": "string"},
		"format":     map[string]any{"type": "string", "enum": []string{"markdown", "md", "json"}},
		"fix_prompt": map[string]any{"type": "string", "enum": []string{"per-finding", "combined"}},
	}, []string{"repo", "session_id"})
}

func repoSchema() map[string]any {
	return objectSchema(map[string]any{"repo": map[string]any{"type": "string"}}, []string{"repo"})
}

func objectSchema(properties map[string]any, required []string) map[string]any {
	return map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
}
