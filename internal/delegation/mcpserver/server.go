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
	addTool(server, "review_submit_findings", "Validate and persist findings produced by the active host agent.", submitSchema(), handleSubmit)
	addTool(server, "review_validate_findings", "Validate and persist findings using the canonical local protocol.", submitSchema(), handleSubmit)
	addTool(server, "review_get_result", "Load a validated review result.", sessionSchema(), handleGetResult)
	addTool(server, "review_render", "Render a validated review result as Markdown or JSON.", renderSchema(), handleRender)
	addTool(server, "review_handoff", "Create a prompt for an independent reviewer task from a prepared session.", sessionSchema(), handleHandoff)
	addTool(server, "review_session_list", "List delegated review sessions in a repository.", repoSchema(), handleList)
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
	Repo       string   `json:"repo"`
	From       string   `json:"from,omitempty"`
	To         string   `json:"to,omitempty"`
	Commit     string   `json:"commit,omitempty"`
	Paths      []string `json:"paths,omitempty"`
	RulePath   string   `json:"rule_path,omitempty"`
	Background string   `json:"background,omitempty"`
	Profile    string   `json:"profile,omitempty"`
}

type sessionArgs struct {
	Repo      string `json:"repo"`
	SessionID string `json:"session_id"`
}

type submitArgs struct {
	Repo       string                `json:"repo"`
	SessionID  string                `json:"session_id"`
	Submission delegation.Submission `json:"submission"`
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

func handlePrepare(ctx context.Context, raw json.RawMessage) (any, error) {
	var args prepareArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, fmt.Errorf("decode review_prepare arguments: %w", err)
	}
	return delegation.Build(ctx, delegation.BuildOptions{
		RepoDir: args.Repo, From: args.From, To: args.To, Commit: args.Commit,
		Paths: args.Paths, RulePath: args.RulePath, Background: args.Background,
		Profile: args.Profile,
	})
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
	return delegation.Submit(args.Repo, args.SessionID, args.Submission)
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
		"profile": map[string]any{"type": "string", "enum": []string{delegation.ReviewProfileDeep, delegation.ReviewProfileStandard}},
	}, []string{"repo"})
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
