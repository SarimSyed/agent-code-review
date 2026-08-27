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
	"time"

	"github.com/alibaba/open-code-review/internal/delegation"
	"github.com/spf13/cobra"
)

type reviewCLIOptions struct {
	repo         string
	from         string
	to           string
	commit       string
	paths        []string
	rulePath     string
	background   string
	profile      string
	caveman      bool
	cavemanLevel string
	maxGitProcs  int
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
	flags.BoolVar(&options.caveman, "caveman", false, "use Caveman token-economy prompts")
	flags.StringVar(&options.cavemanLevel, "caveman-level", delegation.CavemanFull, "Caveman intensity: lite, full, or ultra (requires --caveman)")
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
	review.AddCommand(newPhaseCommand(options))
	return review
}

type phaseNextOutput struct {
	ProtocolVersion string                      `json:"protocol_version"`
	SessionID       string                      `json:"session_id"`
	Task            delegation.PhaseTask        `json:"task"`
	Prompt          string                      `json:"prompt"`
	InputPath       string                      `json:"input_path"`
	Submission      *delegation.PhaseSubmission `json:"submission"`
}

func newPhaseCommand(options *reviewCLIOptions) *cobra.Command {
	phase := &cobra.Command{Use: "phase", Short: "Run validated deep-review phases", Args: cobra.NoArgs}
	phase.AddCommand(newPhaseNextCommand(options), newPhaseSubmitCommand(options), newPhaseStatusCommand(options))
	return phase
}

func newPhaseNextCommand(options *reviewCLIOptions) *cobra.Command {
	var sessionID, worker, format string
	cmd := &cobra.Command{
		Use: "next", Short: "Claim the next ready deep-review phase", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if sessionID == "" || worker == "" {
				return fmt.Errorf("--session and --worker are required")
			}
			repo, err := reviewRepository(options.repo)
			if err != nil {
				return err
			}
			task, err := delegation.ClaimNextPhase(repo, sessionID, worker, time.Now().UTC(), 15*time.Minute)
			if err != nil {
				return err
			}
			prompt, err := delegation.PhasePrompt(repo, sessionID, task.ID)
			if err != nil {
				return err
			}
			draft, inputPath, err := delegation.CreatePhaseDraft(repo, sessionID, task.ID)
			if err != nil {
				return err
			}
			switch strings.ToLower(format) {
			case "json":
				return writeCommandJSON(cmd, phaseNextOutput{ProtocolVersion: delegation.ProtocolVersion, SessionID: sessionID, Task: *task, Prompt: prompt, InputPath: inputPath, Submission: draft})
			case "prompt":
				_, err = io.WriteString(cmd.OutOrStdout(), prompt)
				return err
			default:
				return fmt.Errorf("unsupported format %q; use json or prompt", format)
			}
		},
	}
	cmd.Flags().StringVar(&sessionID, "session", "", "prepared session id")
	cmd.Flags().StringVar(&worker, "worker", "", "opaque worker id")
	cmd.Flags().StringVar(&format, "format", "json", "output format: json or prompt")
	return cmd
}

func newPhaseSubmitCommand(options *reviewCLIOptions) *cobra.Command {
	var sessionID, taskID, inputPath string
	cmd := &cobra.Command{
		Use: "submit", Short: "Validate and persist one phase artifact", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if sessionID == "" || taskID == "" || inputPath == "" {
				return fmt.Errorf("--session, --task, and --input are required")
			}
			repo, err := reviewRepository(options.repo)
			if err != nil {
				return err
			}
			var submission delegation.PhaseSubmission
			if err := readStrictJSONFile(inputPath, &submission); err != nil {
				return fmt.Errorf("decode phase submission: %w", err)
			}
			result, err := delegation.SubmitPhase(repo, sessionID, taskID, submission)
			if err != nil {
				return err
			}
			return writeCommandJSON(cmd, result)
		},
	}
	cmd.Flags().StringVar(&sessionID, "session", "", "prepared session id")
	cmd.Flags().StringVar(&taskID, "task", "", "claimed phase task id")
	cmd.Flags().StringVar(&inputPath, "input", "", "phase submission JSON file")
	return cmd
}

func newPhaseStatusCommand(options *reviewCLIOptions) *cobra.Command {
	var sessionID string
	cmd := &cobra.Command{
		Use: "status", Short: "Show deep-review workflow state", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if sessionID == "" {
				return fmt.Errorf("--session is required")
			}
			repo, err := reviewRepository(options.repo)
			if err != nil {
				return err
			}
			workflow, err := delegation.LoadWorkflow(repo, sessionID)
			if err != nil {
				return err
			}
			return writeCommandJSON(cmd, workflow)
		},
	}
	cmd.Flags().StringVar(&sessionID, "session", "", "prepared session id")
	return cmd
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
				ProtocolVersion: draft.ProtocolVersion,
				SessionID:       sessionID,
				FindingsPath:    path,
				ReviewQuestions: len(draft.QuestionResolutions),
				NextStep:        "Fill every question resolution and finding, then run acr review submit --render so successful validation returns the completed report.",
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
	if !options.caveman && cmd.Flags().Changed("caveman-level") {
		return fmt.Errorf("--caveman-level requires --caveman")
	}
	tokenEconomy := delegation.TokenEconomy{Mode: delegation.TokenEconomyNormal}
	if options.caveman {
		tokenEconomy = delegation.TokenEconomy{Mode: delegation.TokenEconomyCaveman, Level: options.cavemanLevel}
	}
	request, err := delegation.Build(cmd.Context(), delegation.BuildOptions{
		RepoDir: options.repo, From: options.from, To: options.to, Commit: options.commit,
		Paths: options.paths, RulePath: options.rulePath, Background: options.background,
		Profile: options.profile, TokenEconomy: tokenEconomy,
		MaxGitProcs: options.maxGitProcs,
	})
	if err != nil {
		return err
	}
	dir := delegation.SessionDir(request.Repository.Root, request.SessionID)
	return writeCommandJSON(cmd, prepareOutput{
		ProtocolVersion: request.ProtocolVersion,
		SessionID:       request.SessionID,
		RequestPath:     filepath.Join(dir, delegation.RequestFileName),
		FindingsPath:    filepath.Join(dir, delegation.FindingsFileName),
		ReviewUnits:     len(request.Units),
		ModelExecution:  request.Instructions.ModelExecution,
		ReviewProfile:   request.Instructions.ReviewProfile,
		NextStep:        prepareNextStep(request),
	})
}

func prepareNextStep(request *delegation.Request) string {
	if request.Instructions.ReviewProfile == delegation.ReviewProfileDeep {
		return "Loop over acr review phase next and phase submit until workflow is ready, then run acr review draft and acr review submit --render; present the emitted Markdown."
	}
	return "Run acr review brief and draft, inspect every unit, then run acr review submit --render and present its Markdown output."
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
	var sessionID, inputPath, fixPrompt string
	var render bool
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
			if render && len(result.Rejected) == 0 {
				mode, err := parseFixPromptMode(fixPrompt)
				if err != nil {
					return err
				}
				markdown, err := delegation.RenderMarkdownWithOptions(*result, delegation.RenderMarkdownOptions{FixPromptMode: mode})
				if err != nil {
					return err
				}
				_, err = io.WriteString(cmd.OutOrStdout(), markdown)
				return err
			}
			return writeCommandJSON(cmd, result)
		},
	}
	cmd.Flags().StringVar(&sessionID, "session", "", "prepared session id")
	cmd.Flags().StringVar(&inputPath, "input", "", "agent findings JSON file")
	cmd.Flags().BoolVar(&render, "render", false, "render the completed Markdown report after successful validation")
	cmd.Flags().StringVar(&fixPrompt, "fix-prompt", string(delegation.FixPromptPerFinding), "fix prompt mode for --render: per-finding, combined, or none")
	return cmd
}

func newRenderCommand(options *reviewCLIOptions) *cobra.Command {
	var sessionID, format, fixPrompt string
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
				if cmd.Flags().Changed("fix-prompt") && !strings.EqualFold(strings.TrimSpace(fixPrompt), "none") {
					return fmt.Errorf("--fix-prompt requires --format markdown")
				}
				return writeCommandJSON(cmd, result)
			case "markdown", "md":
				mode, err := parseFixPromptMode(fixPrompt)
				if err != nil {
					return err
				}
				markdown, err := delegation.RenderMarkdownWithOptions(*result, delegation.RenderMarkdownOptions{
					FixPromptMode: mode,
				})
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
	cmd.Flags().StringVar(&fixPrompt, "fix-prompt", string(delegation.FixPromptPerFinding), "include copyable fix prompts: per-finding, combined, or none (Markdown only)")
	return cmd
}

func parseFixPromptMode(value string) (delegation.FixPromptMode, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "none":
		return delegation.FixPromptNone, nil
	case string(delegation.FixPromptPerFinding):
		return delegation.FixPromptPerFinding, nil
	case string(delegation.FixPromptCombined):
		return delegation.FixPromptCombined, nil
	default:
		return delegation.FixPromptNone, fmt.Errorf("unsupported fix prompt mode %q; use per-finding, combined, or none", value)
	}
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

func readStrictJSONFile(path string, target any) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open input: %w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple JSON values are not allowed")
		}
		return fmt.Errorf("trailing data: %w", err)
	}
	return nil
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
