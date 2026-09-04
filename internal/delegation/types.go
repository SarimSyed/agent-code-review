// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

// Package delegation implements the provider-free review protocol used by
// coding agents. It prepares immutable review packets and validates findings;
// it never calls a language model.
package delegation

import "time"

const (
	ProtocolVersion         = "2"
	AdaptiveProtocolVersion = "3"
	LegacyProtocolVersion   = "1"
	ModeDiff                = "diff"
	ModeScan                = "scan"
	ReviewProfileStandard   = "standard"
	ReviewProfileDeep       = "deep"
	ReviewProfileAdaptive   = "adaptive"
	RequestFileName         = "request.json"
	FindingsFileName        = "findings.json"
	ResultFileName          = "result.json"
	WorkflowFileName        = "workflow.json"
)

const (
	TokenEconomyNormal    = "normal"
	TokenEconomyCaveman   = "caveman"
	CavemanLite           = "lite"
	CavemanFull           = "full"
	CavemanUltra          = "ultra"
	CommunicationNormal   = "normal"
	CommunicationSkill    = "skill"
	CommunicationFallback = "compact_fallback"
)

type PrepareInput struct {
	Mode         string
	Revision     string
	Background   string
	Profile      string
	TokenEconomy TokenEconomy
	Units        []PreparedUnit
}

type PreparedUnit struct {
	ID    string
	Rule  string
	Files []PreparedFile
}

type PreparedFile struct {
	Path            string
	Diff            string
	SnapshotContent *string
	ImmutableRef    bool
}

type Request struct {
	ProtocolVersion string           `json:"protocol_version"`
	SessionID       string           `json:"session_id"`
	CreatedAt       time.Time        `json:"created_at"`
	Mode            string           `json:"mode"`
	Repository      Repository       `json:"repository"`
	Background      string           `json:"background,omitempty"`
	Units           []ReviewUnit     `json:"units"`
	ReviewQuestions []ReviewQuestion `json:"review_questions,omitempty"`
	Instructions    Instructions     `json:"instructions"`
}

type Repository struct {
	Root                string `json:"root"`
	Revision            string `json:"revision,omitempty"`
	TrackedSourceSHA256 string `json:"tracked_source_sha256,omitempty"`
}

type Instructions struct {
	ModelExecution    string       `json:"model_execution"`
	ReviewOnly        bool         `json:"review_only"`
	DoNotModify       bool         `json:"do_not_modify_files"`
	OutputFile        string       `json:"output_file"`
	AllowedCategories []string     `json:"allowed_categories"`
	ReviewProfile     string       `json:"review_profile"`
	RequiredPasses    []ReviewPass `json:"required_passes"`
	TokenEconomy      TokenEconomy `json:"token_economy"`
}

type TokenEconomy struct {
	Mode  string `json:"mode"`
	Level string `json:"level,omitempty"`
}

type Executor struct {
	Host      string `json:"host"`
	Model     string `json:"model"`
	ContextID string `json:"context_id"`
}

type Communication struct {
	Mode    string `json:"mode"`
	Level   string `json:"level,omitempty"`
	Backend string `json:"backend"`
}

// ReviewPass describes a distinct review activity the host agent must
// complete before it submits findings.
type ReviewPass struct {
	ID        string `json:"id"`
	Objective string `json:"objective"`
}

// ReviewQuestion is a deterministic risk signal that the host model must
// investigate. It is not itself a finding.
type ReviewQuestion struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	UnitID   string `json:"unit_id"`
	File     string `json:"file"`
	Line     int    `json:"line"`
	Subject  string `json:"subject"`
	Question string `json:"question"`
	Evidence string `json:"evidence"`
}

var supportedCategories = []string{
	"bug",
	"security",
	"performance",
	"maintainability",
	"test",
	"style",
	"documentation",
	"other",
}

// SupportedCategories returns a copy so callers cannot mutate the protocol's
// canonical category vocabulary.
func SupportedCategories() []string {
	return append([]string(nil), supportedCategories...)
}

type ReviewUnit struct {
	ID    string         `json:"id"`
	Rule  string         `json:"rule,omitempty"`
	Files []FileSnapshot `json:"files"`
}

// Briefing is a compact, agent-safe view of a prepared review packet. It
// deliberately excludes frozen file content and raw diffs; agents inspect the
// repository directly while retaining every valid changed-line anchor.
type Briefing struct {
	ProtocolVersion   string           `json:"protocol_version"`
	SessionID         string           `json:"session_id"`
	Repository        Repository       `json:"repository"`
	ReviewProfile     string           `json:"review_profile"`
	RequiredPasses    []ReviewPass     `json:"required_passes"`
	AllowedCategories []string         `json:"allowed_categories"`
	ReviewQuestions   []ReviewQuestion `json:"review_questions,omitempty"`
	Units             []BriefingUnit   `json:"units"`
}

type BriefingUnit struct {
	ID          string         `json:"id"`
	RulePreview string         `json:"rule_preview,omitempty"`
	Files       []BriefingFile `json:"files"`
}

type BriefingFile struct {
	Path              string      `json:"path"`
	LineCount         int         `json:"line_count"`
	SHA256            string      `json:"sha256"`
	ChangedLineRanges []LineRange `json:"changed_line_ranges,omitempty"`
}

type LineRange struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

type FileSnapshot struct {
	Path              string `json:"path"`
	Diff              string `json:"diff,omitempty"`
	Content           string `json:"content"`
	SHA256            string `json:"sha256"`
	LineCount         int    `json:"line_count"`
	ValidateWorkspace bool   `json:"validate_workspace"`
}

type Submission struct {
	ProtocolVersion       string                 `json:"protocol_version"`
	SessionID             string                 `json:"session_id"`
	QuestionResolutions   []QuestionResolution   `json:"question_resolutions,omitempty"`
	CandidateDispositions []CandidateDisposition `json:"candidate_dispositions,omitempty"`
	Findings              []Finding              `json:"findings"`
}

type CandidateDisposition struct {
	CandidateID        string        `json:"candidate_id"`
	Outcome            string        `json:"outcome"`
	Reason             string        `json:"reason"`
	OverrideReason     string        `json:"override_reason,omitempty"`
	AdditionalEvidence []EvidenceRef `json:"additional_evidence,omitempty"`
}

type QuestionResolution struct {
	QuestionID   string `json:"question_id"`
	Outcome      string `json:"outcome"`
	Evidence     string `json:"evidence"`
	FindingIndex *int   `json:"finding_index,omitempty"`
}

type Finding struct {
	CandidateID  string  `json:"candidate_id,omitempty"`
	UnitID       string  `json:"unit_id"`
	File         string  `json:"file"`
	StartLine    int     `json:"start_line"`
	EndLine      int     `json:"end_line"`
	Severity     string  `json:"severity"`
	Category     string  `json:"category"`
	Explanation  string  `json:"explanation"`
	Evidence     string  `json:"evidence"`
	SuggestedFix string  `json:"suggested_fix,omitempty"`
	Confidence   float64 `json:"confidence"`
}

type Rejection struct {
	Index   int    `json:"index"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type Result struct {
	ProtocolVersion       string                 `json:"protocol_version"`
	SessionID             string                 `json:"session_id"`
	QuestionResolutions   []QuestionResolution   `json:"question_resolutions,omitempty"`
	CandidateDispositions []CandidateDisposition `json:"candidate_dispositions,omitempty"`
	Findings              []Finding              `json:"findings"`
	Rejected              []Rejection            `json:"rejected"`
	Summary               ResultSummary          `json:"summary"`
	Assurance             *ReviewAssurance       `json:"assurance,omitempty"`
}

type ReviewAssurance struct {
	WorkflowState        string `json:"workflow_state"`
	CriticMode           string `json:"critic_mode,omitempty"`
	CommunicationMode    string `json:"communication_mode"`
	CommunicationLevel   string `json:"communication_level,omitempty"`
	CommunicationBackend string `json:"communication_backend,omitempty"`
	PhasesCompleted      int    `json:"phases_completed"`
	Candidates           int    `json:"candidates"`
	Dropped              int    `json:"dropped"`
	Overrides            int    `json:"overrides"`
	EvidenceFiles        int    `json:"evidence_files"`
	AnalysisMS           int64  `json:"analysis_ms,omitempty"`
	CritiqueMS           int64  `json:"critique_ms,omitempty"`
	ResolutionMS         int64  `json:"resolution_ms,omitempty"`
	TotalElapsedMS       int64  `json:"total_elapsed_ms,omitempty"`
	BatchCalls           int    `json:"batch_calls,omitempty"`
	ValidationRejections int    `json:"validation_rejections,omitempty"`
	CriticFallbacks      int    `json:"critic_fallbacks,omitempty"`
}

type ResultSummary struct {
	Accepted   int `json:"accepted"`
	Rejected   int `json:"rejected"`
	Duplicates int `json:"duplicates"`
}
