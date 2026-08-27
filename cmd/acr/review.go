// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/alibaba/open-code-review/internal/delegation"
	"github.com/spf13/cobra"
)

type reviewCLIOptions struct {
	repo        string
	from        string
	to          string
	commit      string
	paths       []string
	rulePath    string
	background  string
	profile     string
	maxGitProcs int
}

type prepareOutput struct {
	ProtocolVersion string `json:"protocol_version"`
	SessionID       string `json:"session_id"`
	RequestPath     string `json:"request_path"`
	FindingsPath    string `json:"findings_path"`
	ReviewUnits     int    `json:"review_units"`
	ModelExecution  string `json:"model_execution"`
	ReviewProfile   string `json:"review_profile"`
	NextStep        string `json:"next_step"`
}

type draftOutput struct {
	ProtocolVersion string `json:"protocol_version"`
	SessionID       string `json:"session_id"`
	FindingsPath    string `json:"findings_path"`
	ReviewQuestions int    `json:"review_questions"`
	NextStep        string `json:"next_step"`
}

func newReviewCommand() *cobra.Command {
	options := &reviewCLIOptions{}
	review := &cobra.Command{
		Use:   "review",
		Short: "Prepare and validate an agent-delegated code review",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runPrepare(cmd, options)
		},
	}
	flags := review.PersistentFlags()
	flags.StringVar(&options.repo, "repo", "", "repository or directory to review (default: current directory)")
	flags.StringVar(&options.from, "from", "", "base ref for a branch-range review")
	flags.StringVar(&options.to, "to", "", "target ref for a branch-range review")
	flags.StringVarP(&options.commit, "commit", "c", "", "commit to review")
	flags.StringSliceVar(&options.paths, "path", nil, "file or directory to scan; repeat for multiple paths")
	flags.StringVar(&options.rulePath, "rule", "", "custom review rule file")
	flags.StringVar(&options.background, "background", "", "requirements or business context")
	flags.StringVar(&options.profile, "profile", delegation.ReviewProfileDeep, "review profile: deep or standard")
	flags.IntVar(&options.maxGitProcs, "max-git-procs", 4, "maximum concurrent Git processes")

	review.AddCommand(&cobra.Command{
		Use:   "prepare",
		Short: "Create a versioned review packet for the active agent",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runPrepare(cmd, options)
		},
	})
	review.AddCommand(newSubmitCommand(options))
	review.AddCommand(newRenderCommand(options))
	review.AddCommand(newHandoffCommand(options))
	review.AddCommand(newBriefCommand(options))
	review.AddCommand(newDraftCommand(options))
	return review
}

func newDraftCommand(options *reviewCLIOptions) *cobra.Command {
	var sessionID string
	cmd := &cobra.Command{
		Use:   "draft",
		Short: "Create a non-overwriting findings form for the active agent",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if sessionID == "" {
				return fmt.Errorf("--session is required")
			}
			repo, err := reviewRepository(options.repo)
			if err != nil {
				return err
			}
			draft, path, err := delegation.CreateSubmissionDraft(repo, sessionID)
			if err != nil {
				return err
			}
			return writeCommandJSON(cmd, draftOutput{
				ProtocolVersion: delegation.ProtocolVersion,
				SessionID:       sessionID,
				FindingsPath:    path,
				ReviewQuestions: len(draft.QuestionResolutions),
				NextStep:        "Fill every question resolution and finding in this file, then run acr review submit.",
			})
		},
	}
	cmd.Flags().StringVar(&sessionID, "session", "", "prepared session id")
	return cmd
}

func newBriefCommand(options *reviewCLIOptions) *cobra.Command {
	var sessionID string
	cmd := &cobra.Command{
		Use:   "brief",
		Short: "Show a compact agent-safe manifest for a prepared session",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if sessionID == "" {
				return fmt.Errorf("--session is required")
			}
			repo, err := reviewRepository(options.repo)
			if err != nil {
				return err
			}
			briefing, err := delegation.Brief(repo, sessionID)
			if err != nil {
				return err
			}
			return writeCommandJSON(cmd, briefing)
		},
	}
	cmd.Flags().StringVar(&sessionID, "session", "", "prepared session id")
	return cmd
}

func runPrepare(cmd *cobra.Command, options *reviewCLIOptions) error {
	request, err := delegation.Build(cmd.Context(), delegation.BuildOptions{
		RepoDir: options.repo, From: options.from, To: options.to, Commit: options.commit,
		Paths: options.paths, RulePath: options.rulePath, Background: options.background,
		Profile:     options.profile,
		MaxGitProcs: options.maxGitProcs,
	})
	if err != nil {
		return err
	}
	dir := delegation.SessionDir(request.Repository.Root, request.SessionID)
	return writeCommandJSON(cmd, prepareOutput{
		ProtocolVersion: delegation.ProtocolVersion,
		SessionID:       request.SessionID,
		RequestPath:     filepath.Join(dir, delegation.RequestFileName),
		FindingsPath:    filepath.Join(dir, delegation.FindingsFileName),
		ReviewUnits:     len(request.Units),
		ModelExecution:  request.Instructions.ModelExecution,
		ReviewProfile:   request.Instructions.ReviewProfile,
		NextStep:        "Run acr review brief for focused risk questions, inspect every unit, resolve each question in findings.json, then submit and render Markdown.",
	})
}

func newHandoffCommand(options *reviewCLIOptions) *cobra.Command {
	var sessionID string
	cmd := &cobra.Command{
		Use:   "handoff",
		Short: "Create an independent-reviewer task prompt for a prepared session",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if sessionID == "" {
				return fmt.Errorf("--session is required")
			}
			repo, err := reviewRepository(options.repo)
			if err != nil {
				return err
			}
			request, err := delegation.LoadRequest(repo, sessionID)
			if err != nil {
				return err
			}
			prompt, err := delegation.HandoffPrompt(request)
			if err != nil {
				return err
			}
			_, err = io.WriteString(cmd.OutOrStdout(), prompt)
			return err
		},
	}
	cmd.Flags().StringVar(&sessionID, "session", "", "prepared session id")
	return cmd
}

func newSubmitCommand(options *reviewCLIOptions) *cobra.Command {
	var sessionID, inputPath string
	cmd := &cobra.Command{
		Use:   "submit",
		Short: "Validate findings produced by the active agent",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if sessionID == "" || inputPath == "" {
				return fmt.Errorf("--session and --input are required")
			}
			repo, err := reviewRepository(options.repo)
			if err != nil {
				return err
			}
			submission, err := readSubmission(inputPath)
			if err != nil {
				return err
			}
			result, err := delegation.Submit(repo, sessionID, submission)
			if err != nil {
				return err
			}
			return writeCommandJSON(cmd, result)
		},
	}
	cmd.Flags().StringVar(&sessionID, "session", "", "prepared session id")
	cmd.Flags().StringVar(&inputPath, "input", "", "agent findings JSON file")
	return cmd
}

func newRenderCommand(options *reviewCLIOptions) *cobra.Command {
	var sessionID, format string
	cmd := &cobra.Command{
		Use:   "render",
		Short: "Render a validated review result",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if sessionID == "" {
				return fmt.Errorf("--session is required")
			}
			repo, err := reviewRepository(options.repo)
			if err != nil {
				return err
			}
			result, err := delegation.LoadResult(repo, sessionID)
			if err != nil {
				return err
			}
			switch strings.ToLower(format) {
			case "json":
				return writeCommandJSON(cmd, result)
			case "markdown", "md":
				markdown, err := delegation.RenderMarkdown(*result)
				if err != nil {
					return err
				}
				_, err = io.WriteString(cmd.OutOrStdout(), markdown)
				return err
			default:
				return fmt.Errorf("unsupported format %q; use json or markdown", format)
			}
		},
	}
	cmd.Flags().StringVar(&sessionID, "session", "", "validated session id")
	cmd.Flags().StringVar(&format, "format", "markdown", "output format: markdown or json")
	return cmd
}

func readSubmission(path string) (delegation.Submission, error) {
	file, err := os.Open(path)
	if err != nil {
		return delegation.Submission{}, fmt.Errorf("open findings: %w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var submission delegation.Submission
	if err := decoder.Decode(&submission); err != nil {
		return delegation.Submission{}, fmt.Errorf("decode findings: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return delegation.Submission{}, fmt.Errorf("decode findings: multiple JSON values are not allowed")
		}
		return delegation.Submission{}, fmt.Errorf("decode findings: trailing data: %w", err)
	}
	return submission, nil
}

func reviewRepository(input string) (string, error) {
	if input == "" {
		input = "."
	}
	repo, err := filepath.Abs(input)
	if err != nil {
		return "", fmt.Errorf("resolve repository: %w", err)
	}
	cmd := exec.Command("git", "-C", repo, "rev-parse", "--show-toplevel")
	if output, gitErr := cmd.Output(); gitErr == nil {
		if root := strings.TrimSpace(string(output)); root != "" {
			return root, nil
		}
	}
	return repo, nil
}

func writeCommandJSON(cmd *cobra.Command, value any) error {
	encoder := json.NewEncoder(cmd.OutOrStdout())
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}
