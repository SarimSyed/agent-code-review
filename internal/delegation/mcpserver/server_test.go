// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alibaba/open-code-review/internal/delegation"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestServerExposesDelegationTools(t *testing.T) {
	server := New("test")
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	if _, err := server.Connect(context.Background(), serverTransport, nil); err != nil {
		t.Fatalf("connect server: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1"}, nil)
	session, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatalf("connect client: %v", err)
	}
	defer session.Close()

	listed, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	got := map[string]bool{}
	for _, tool := range listed.Tools {
		got[tool.Name] = true
	}
	for _, name := range []string{
		"review_prepare", "review_get_request", "review_get_briefing", "review_create_draft", "review_get_unit", "review_submit_findings", "review_validate_findings",
		"review_get_result", "review_render", "review_handoff", "review_session_list",
	} {
		if !got[name] {
			t.Errorf("missing MCP tool %q", name)
		}
	}
}

func TestMCPHandlersCompleteProviderFreeRoundTrip(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "app.go"), []byte("package app\n"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}

	prepared, err := handlePrepare(context.Background(), rawJSON(t, map[string]any{
		"repo": repo, "paths": []string{"app.go"},
	}))
	if err != nil {
		t.Fatalf("review_prepare: %v", err)
	}
	request := prepared.(*delegation.Request)
	if request.Instructions.ReviewProfile != delegation.ReviewProfileDeep {
		t.Fatalf("unexpected default profile: %#v", request.Instructions)
	}
	if _, err := handleGetRequest(context.Background(), rawJSON(t, map[string]any{
		"repo": repo, "session_id": request.SessionID,
	})); err != nil {
		t.Fatalf("review_get_request: %v", err)
	}
	briefing, err := handleGetBriefing(context.Background(), rawJSON(t, map[string]any{
		"repo": repo, "session_id": request.SessionID,
	}))
	if err != nil {
		t.Fatalf("review_get_briefing: %v", err)
	}
	if len(briefing.(*delegation.Briefing).Units) != 1 {
		t.Fatalf("unexpected briefing: %#v", briefing)
	}
	if _, err := handleCreateDraft(context.Background(), rawJSON(t, map[string]any{
		"repo": repo, "session_id": request.SessionID,
	})); err != nil {
		t.Fatalf("review_create_draft: %v", err)
	}
	if _, err := handleGetUnit(context.Background(), rawJSON(t, map[string]any{
		"repo": repo, "session_id": request.SessionID, "unit_id": request.Units[0].ID,
	})); err != nil {
		t.Fatalf("review_get_unit: %v", err)
	}
	handoff, err := handleHandoff(context.Background(), rawJSON(t, map[string]any{
		"repo": repo, "session_id": request.SessionID,
	}))
	if err != nil {
		t.Fatalf("review_handoff: %v", err)
	}
	if prompt := handoff.(map[string]string)["prompt"]; !strings.Contains(prompt, "independent reviewer") {
		t.Fatalf("unexpected handoff prompt: %q", prompt)
	}

	submitted, err := handleSubmit(context.Background(), rawJSON(t, map[string]any{
		"repo":       repo,
		"session_id": request.SessionID,
		"submission": map[string]any{
			"protocol_version": delegation.ProtocolVersion,
			"session_id":       request.SessionID,
			"findings":         []any{},
		},
	}))
	if err != nil {
		t.Fatalf("review_submit_findings: %v", err)
	}
	if result := submitted.(*delegation.Result); result.Summary.Rejected != 0 {
		t.Fatalf("unexpected result: %#v", result)
	}
	if _, err := handleRender(context.Background(), rawJSON(t, map[string]any{
		"repo": repo, "session_id": request.SessionID, "format": "markdown",
	})); err != nil {
		t.Fatalf("review_render: %v", err)
	}
}

func TestReviewRenderIncludesPerFindingFixPrompt(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "app.go"), []byte("package app\n"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	request, err := delegation.Prepare(repo, delegation.PrepareInput{
		Mode: delegation.ModeScan,
		Units: []delegation.PreparedUnit{{
			ID: "unit-1", Files: []delegation.PreparedFile{{Path: "app.go"}},
		}},
	})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if _, err := delegation.Submit(repo, request.SessionID, delegation.Submission{
		ProtocolVersion: delegation.ProtocolVersion,
		SessionID:       request.SessionID,
		Findings: []delegation.Finding{{
			UnitID: "unit-1", File: "app.go", StartLine: 1, EndLine: 1,
			Severity: "medium", Category: "bug", Explanation: "A validated problem.",
			Evidence: "The source demonstrates it.", Confidence: 0.9,
		}},
	}); err != nil {
		t.Fatalf("submit: %v", err)
	}

	value, err := handleRender(context.Background(), rawJSON(t, map[string]any{
		"repo": repo, "session_id": request.SessionID, "format": "markdown", "fix_prompt": "per-finding",
	}))
	if err != nil {
		t.Fatalf("review_render: %v", err)
	}
	content := value.(map[string]string)["content"]
	if !strings.Contains(content, "Copyable fix prompt") || !strings.Contains(content, "Work on exactly one validated ACR finding") {
		t.Fatalf("rendered content missing fix prompt:\n%s", content)
	}
}

func rawJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal arguments: %v", err)
	}
	return data
}
