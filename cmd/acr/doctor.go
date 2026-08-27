// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/alibaba/open-code-review/internal/delegation"
	"github.com/spf13/cobra"
)

type doctorCheck struct {
	Available bool   `json:"available"`
	Detail    string `json:"detail,omitempty"`
}

type doctorReport struct {
	Healthy           bool        `json:"healthy"`
	NoAPIKeyRequired  bool        `json:"no_api_key_required"`
	Git               doctorCheck `json:"git"`
	Repository        doctorCheck `json:"repository"`
	SessionStorage    doctorCheck `json:"session_storage"`
	AgentIntegration  doctorCheck `json:"agent_integration"`
	MCPConfiguration  doctorCheck `json:"mcp_configuration"`
	ProtocolRoundTrip doctorCheck `json:"protocol_round_trip"`
}

func newDoctorCommand() *cobra.Command {
	var repo, format string
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose local delegation prerequisites",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			report := runDoctor(repo)
			if format == "json" {
				return writeCommandJSON(cmd, report)
			}
			status := "ready"
			if !report.Healthy {
				status = "needs attention"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "ACR doctor: %s\n", status)
			fmt.Fprintf(cmd.OutOrStdout(), "- Git: %s\n", report.Git.Detail)
			fmt.Fprintf(cmd.OutOrStdout(), "- Repository: %s\n", report.Repository.Detail)
			fmt.Fprintf(cmd.OutOrStdout(), "- Session storage: %s\n", report.SessionStorage.Detail)
			fmt.Fprintf(cmd.OutOrStdout(), "- Agent integration: %s\n", report.AgentIntegration.Detail)
			fmt.Fprintf(cmd.OutOrStdout(), "- MCP configuration: %s\n", report.MCPConfiguration.Detail)
			fmt.Fprintf(cmd.OutOrStdout(), "- Protocol round trip: %s\n", report.ProtocolRoundTrip.Detail)
			fmt.Fprintln(cmd.OutOrStdout(), "- Model API key: not required; the active coding agent performs reasoning")
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", "", "repository or directory to diagnose")
	cmd.Flags().StringVar(&format, "format", "text", "output format: text or json")
	return cmd
}

func runDoctor(repoInput string) doctorReport {
	report := doctorReport{NoAPIKeyRequired: true}
	if path, err := exec.LookPath("git"); err == nil {
		out, versionErr := exec.Command(path, "--version").Output()
		report.Git = doctorCheck{Available: versionErr == nil, Detail: strings.TrimSpace(string(out))}
	} else {
		report.Git.Detail = "git not found"
	}
	if repoInput == "" {
		repoInput = "."
	}
	repo, err := filepath.Abs(repoInput)
	if err == nil {
		if info, statErr := os.Stat(repo); statErr == nil && info.IsDir() {
			report.Repository = doctorCheck{Available: true, Detail: repo}
			sessionRoot := filepath.Join(repo, ".acr", "sessions")
			if mkdirErr := os.MkdirAll(sessionRoot, 0o700); mkdirErr == nil {
				report.SessionStorage = doctorCheck{Available: true, Detail: sessionRoot}
			} else {
				report.SessionStorage.Detail = mkdirErr.Error()
			}
			report.AgentIntegration = detectAgentIntegration(repo)
			report.MCPConfiguration = detectMCPConfiguration(repo)
		} else if statErr != nil {
			report.Repository.Detail = statErr.Error()
		}
	} else {
		report.Repository.Detail = err.Error()
	}
	report.ProtocolRoundTrip = checkProtocolRoundTrip()
	report.Healthy = report.Git.Available && report.Repository.Available && report.SessionStorage.Available && report.ProtocolRoundTrip.Available
	return report
}

func detectAgentIntegration(repo string) doctorCheck {
	candidates := []string{
		filepath.Join(repo, "skills", "agent-code-review", "SKILL.md"),
		filepath.Join(repo, ".agents", "skills", "agent-code-review", "SKILL.md"),
		filepath.Join(repo, ".codex", "skills", "agent-code-review", "SKILL.md"),
		filepath.Join(repo, ".claude", "commands", "agent-code-review.md"),
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates,
			filepath.Join(home, ".codex", "skills", "agent-code-review", "SKILL.md"),
			filepath.Join(home, ".claude", "skills", "agent-code-review", "SKILL.md"),
		)
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return doctorCheck{Available: true, Detail: candidate}
		}
	}
	return doctorCheck{Detail: "not detected; install the agent-code-review skill or host plugin"}
}

func detectMCPConfiguration(repo string) doctorCheck {
	candidates := []string{
		filepath.Join(repo, ".mcp.json"),
		filepath.Join(repo, ".cursor", "mcp.json"),
		filepath.Join(repo, ".codex", "config.toml"),
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates,
			filepath.Join(home, ".codex", "config.toml"),
			filepath.Join(home, ".cursor", "mcp.json"),
		)
	}
	for _, candidate := range candidates {
		data, err := os.ReadFile(candidate)
		if err == nil && strings.Contains(strings.ToLower(string(data)), "acr") {
			return doctorCheck{Available: true, Detail: candidate}
		}
	}
	return doctorCheck{Detail: "not detected; configure the host to run `acr mcp` over stdio"}
}

func checkProtocolRoundTrip() doctorCheck {
	repo, err := os.MkdirTemp("", "acr-doctor-")
	if err != nil {
		return doctorCheck{Detail: err.Error()}
	}
	defer os.RemoveAll(repo)
	if err := os.WriteFile(filepath.Join(repo, "probe.txt"), []byte("acr protocol probe\n"), 0o600); err != nil {
		return doctorCheck{Detail: err.Error()}
	}
	request, err := delegation.Prepare(repo, delegation.PrepareInput{
		Mode: delegation.ModeScan, Profile: delegation.ReviewProfileStandard,
		Units: []delegation.PreparedUnit{{
			ID: "doctor", Files: []delegation.PreparedFile{{Path: "probe.txt"}},
		}},
	})
	if err != nil {
		return doctorCheck{Detail: err.Error()}
	}
	result, err := delegation.Submit(repo, request.SessionID, delegation.Submission{
		ProtocolVersion: delegation.ProtocolVersion,
		SessionID:       request.SessionID,
		Findings:        []delegation.Finding{},
	})
	if err != nil || len(result.Rejected) != 0 {
		if err != nil {
			return doctorCheck{Detail: err.Error()}
		}
		return doctorCheck{Detail: "local prepare-submit validation failed"}
	}
	if _, err := delegation.RenderMarkdown(*result); err != nil {
		return doctorCheck{Detail: err.Error()}
	}
	return doctorCheck{Available: true, Detail: "local prepare-submit-render protocol is operational"}
}
