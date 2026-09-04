// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package delegation

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

var sessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

type SessionSummary struct {
	SessionID string    `json:"session_id"`
	CreatedAt time.Time `json:"created_at"`
	Mode      string    `json:"mode"`
	Units     int       `json:"units"`
	State     string    `json:"state"`
}

func Prepare(repo string, input PrepareInput) (*Request, error) {
	root, err := filepath.Abs(repo)
	if err != nil {
		return nil, fmt.Errorf("resolve repository: %w", err)
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("repository is not a readable directory: %s", root)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("resolve repository symlinks: %w", err)
	}
	if input.Mode != ModeDiff && input.Mode != ModeScan {
		return nil, fmt.Errorf("unsupported review mode %q", input.Mode)
	}
	if len(input.Units) == 0 {
		return nil, fmt.Errorf("at least one review unit is required")
	}
	profile, err := normalizeReviewProfile(input.Profile)
	if err != nil {
		return nil, err
	}
	tokenEconomy, err := normalizeTokenEconomy(input.TokenEconomy)
	if err != nil {
		return nil, err
	}
	trackedSourceSHA, err := trackedSourceFingerprint(root)
	if err != nil {
		return nil, err
	}

	sessionID, err := newSessionID()
	if err != nil {
		return nil, err
	}
	protocolVersion := ProtocolVersion
	if profile == ReviewProfileAdaptive {
		protocolVersion = AdaptiveProtocolVersion
	}
	request := &Request{
		ProtocolVersion: protocolVersion,
		SessionID:       sessionID,
		CreatedAt:       time.Now().UTC(),
		Mode:            input.Mode,
		Repository:      Repository{Root: root, Revision: input.Revision, TrackedSourceSHA256: trackedSourceSHA},
		Background:      input.Background,
		Units:           make([]ReviewUnit, 0, len(input.Units)),
		Instructions: Instructions{
			ModelExecution:    "host_agent",
			ReviewOnly:        true,
			DoNotModify:       true,
			OutputFile:        FindingsFileName,
			AllowedCategories: SupportedCategories(),
			ReviewProfile:     profile,
			RequiredPasses:    reviewPasses(profile),
			TokenEconomy:      tokenEconomy,
		},
	}

	seenUnits := map[string]struct{}{}
	for i, prepared := range input.Units {
		unitID := strings.TrimSpace(prepared.ID)
		if unitID == "" {
			unitID = fmt.Sprintf("unit-%04d", i+1)
		}
		if _, exists := seenUnits[unitID]; exists {
			return nil, fmt.Errorf("duplicate review unit id %q", unitID)
		}
		seenUnits[unitID] = struct{}{}
		if len(prepared.Files) == 0 {
			return nil, fmt.Errorf("review unit %q has no files", unitID)
		}
		unit := ReviewUnit{ID: unitID, Rule: prepared.Rule, Files: make([]FileSnapshot, 0, len(prepared.Files))}
		for _, preparedFile := range prepared.Files {
			rel, full, err := resolveRepoPath(root, preparedFile.Path)
			if err != nil {
				return nil, fmt.Errorf("prepare %q: %w", preparedFile.Path, err)
			}
			var content []byte
			if preparedFile.SnapshotContent != nil {
				content = []byte(*preparedFile.SnapshotContent)
			} else {
				if _, _, err := resolveRepoFile(root, preparedFile.Path); err != nil {
					return nil, fmt.Errorf("prepare %q: %w", preparedFile.Path, err)
				}
				content, err = readRegularFile(full)
				if err != nil {
					return nil, fmt.Errorf("read %s: %w", rel, err)
				}
			}
			unit.Files = append(unit.Files, FileSnapshot{
				Path:              rel,
				Diff:              preparedFile.Diff,
				Content:           string(content),
				SHA256:            contentSHA256(content),
				LineCount:         countLines(content),
				ValidateWorkspace: !preparedFile.ImmutableRef,
			})
		}
		request.Units = append(request.Units, unit)
	}
	if request.Mode == ModeDiff && (profile == ReviewProfileDeep || profile == ReviewProfileAdaptive) {
		request.ReviewQuestions = generateReviewQuestions(request.Units)
	}

	dir := SessionDir(root, sessionID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create session directory: %w", err)
	}
	if err := writeJSON(filepath.Join(dir, RequestFileName), request); err != nil {
		return nil, err
	}
	if err := saveWorkflow(root, newWorkflow(request)); err != nil {
		return nil, err
	}
	return request, nil
}

func trackedSourceFingerprint(root string) (string, error) {
	probe := exec.Command("git", "-C", root, "rev-parse", "--verify", "HEAD")
	if err := probe.Run(); err != nil {
		return "", nil
	}
	command := exec.Command("git", "-C", root, "diff", "--no-ext-diff", "--binary", "HEAD", "--")
	data, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("fingerprint tracked source: %w", err)
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func normalizeTokenEconomy(value TokenEconomy) (TokenEconomy, error) {
	value.Mode = strings.ToLower(strings.TrimSpace(value.Mode))
	value.Level = strings.ToLower(strings.TrimSpace(value.Level))
	if value.Mode == "" || value.Mode == TokenEconomyNormal {
		if value.Level != "" {
			return TokenEconomy{}, fmt.Errorf("caveman level requires caveman mode")
		}
		return TokenEconomy{Mode: TokenEconomyNormal}, nil
	}
	if value.Mode != TokenEconomyCaveman {
		return TokenEconomy{}, fmt.Errorf("unsupported token economy mode %q", value.Mode)
	}
	if value.Level == "" {
		value.Level = CavemanFull
	}
	switch value.Level {
	case CavemanLite, CavemanFull, CavemanUltra:
		return value, nil
	default:
		return TokenEconomy{}, fmt.Errorf("unsupported caveman level %q", value.Level)
	}
}

// NormalizeTokenEconomy validates and fills defaults for a host communication
// policy without starting a review session.
func NormalizeTokenEconomy(value TokenEconomy) (TokenEconomy, error) {
	return normalizeTokenEconomy(value)
}

func normalizeReviewProfile(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ReviewProfileDeep, nil
	}
	switch value {
	case ReviewProfileStandard, ReviewProfileDeep, ReviewProfileAdaptive:
		return value, nil
	default:
		return "", fmt.Errorf("unsupported review profile %q", value)
	}
}

func reviewPasses(profile string) []ReviewPass {
	if profile == ReviewProfileStandard {
		return []ReviewPass{{
			ID: "inspect", Objective: "Inspect every prepared unit and report only concrete, evidence-backed defects.",
		}}
	}
	return []ReviewPass{
		{ID: "invariants", Objective: "Extract ordering, state, lifecycle, and business invariants from comments, tests, assertions, and existing control flow."},
		{ID: "dependencies", Objective: "Prove that async, sequencing, transaction, and concurrency changes preserve dependency order and atomicity."},
		{ID: "contracts", Objective: "Trace changed calls through callee signatures, guards, return values, errors, and focused tests."},
		{ID: "lifecycle", Objective: "Trace removed or moved initialization, registration, listener, cleanup, and ownership calls for lost behavior."},
		{ID: "verification", Objective: "Inspect relevant tests and run focused safe checks when available; record only evidence actually observed."},
		{ID: "critique", Objective: "Challenge every candidate finding for reachability, intended behavior, and false positives before submitting it."},
	}
}

func LoadRequest(repo, sessionID string) (*Request, error) {
	if !sessionIDPattern.MatchString(sessionID) {
		return nil, fmt.Errorf("invalid session id")
	}
	var request Request
	if err := readJSON(filepath.Join(SessionDir(repo, sessionID), RequestFileName), &request); err != nil {
		return nil, err
	}
	if request.ProtocolVersion != AdaptiveProtocolVersion && request.ProtocolVersion != ProtocolVersion && request.ProtocolVersion != LegacyProtocolVersion {
		return nil, fmt.Errorf("unsupported request protocol %q", request.ProtocolVersion)
	}
	if request.SessionID != sessionID {
		return nil, fmt.Errorf("request session id %q does not match %q", request.SessionID, sessionID)
	}
	root, err := filepath.Abs(repo)
	if err != nil {
		return nil, fmt.Errorf("resolve repository: %w", err)
	}
	if root, err = filepath.EvalSymlinks(root); err != nil {
		return nil, fmt.Errorf("resolve repository symlinks: %w", err)
	}
	if filepath.Clean(request.Repository.Root) != filepath.Clean(root) {
		return nil, fmt.Errorf("request repository root does not match the selected repository")
	}
	return &request, nil
}

// Brief returns a compact manifest that lets an agent navigate a review
// session without echoing the packet's potentially large source snapshots.
func Brief(repo, sessionID string) (*Briefing, error) {
	request, err := LoadRequest(repo, sessionID)
	if err != nil {
		return nil, err
	}
	briefing := &Briefing{
		ProtocolVersion:   request.ProtocolVersion,
		SessionID:         request.SessionID,
		Repository:        request.Repository,
		ReviewProfile:     request.Instructions.ReviewProfile,
		RequiredPasses:    append([]ReviewPass(nil), request.Instructions.RequiredPasses...),
		AllowedCategories: append([]string(nil), request.Instructions.AllowedCategories...),
		ReviewQuestions:   append([]ReviewQuestion(nil), request.ReviewQuestions...),
		Units:             make([]BriefingUnit, 0, len(request.Units)),
	}
	for _, unit := range request.Units {
		entry := BriefingUnit{
			ID:          unit.ID,
			RulePreview: compactRule(unit.Rule),
			Files:       make([]BriefingFile, 0, len(unit.Files)),
		}
		for _, file := range unit.Files {
			entry.Files = append(entry.Files, BriefingFile{
				Path:              file.Path,
				LineCount:         file.LineCount,
				SHA256:            file.SHA256,
				ChangedLineRanges: changedLineRanges(file.Diff),
			})
		}
		briefing.Units = append(briefing.Units, entry)
	}
	return briefing, nil
}

func compactRule(rule string) string {
	rule = strings.Join(strings.Fields(strings.TrimSpace(rule)), " ")
	const maxRunes = 240
	if len([]rune(rule)) <= maxRunes {
		return rule
	}
	return string([]rune(rule)[:maxRunes-1]) + "…"
}

func LoadResult(repo, sessionID string) (*Result, error) {
	if !sessionIDPattern.MatchString(sessionID) {
		return nil, fmt.Errorf("invalid session id")
	}
	var result Result
	if err := readJSON(filepath.Join(SessionDir(repo, sessionID), ResultFileName), &result); err != nil {
		return nil, err
	}
	if result.ProtocolVersion != AdaptiveProtocolVersion && result.ProtocolVersion != ProtocolVersion && result.ProtocolVersion != LegacyProtocolVersion {
		return nil, fmt.Errorf("unsupported result protocol %q", result.ProtocolVersion)
	}
	if result.SessionID != sessionID {
		return nil, fmt.Errorf("result session id %q does not match %q", result.SessionID, sessionID)
	}
	return &result, nil
}

// CreateSubmissionDraft writes an unfilled, non-overwriting submission form
// so agents do not need to rediscover the transport schema from request.json.
func CreateSubmissionDraft(repo, sessionID string) (*Submission, string, error) {
	request, err := LoadRequest(repo, sessionID)
	if err != nil {
		return nil, "", err
	}
	draft := &Submission{
		ProtocolVersion:     request.ProtocolVersion,
		SessionID:           sessionID,
		QuestionResolutions: make([]QuestionResolution, 0, len(request.ReviewQuestions)),
		Findings:            make([]Finding, 0),
	}
	if request.ProtocolVersion == ProtocolVersion && request.Instructions.ReviewProfile == ReviewProfileDeep {
		workflow, err := LoadWorkflow(repo, sessionID)
		if err != nil {
			return nil, "", err
		}
		if workflow.State != WorkflowReady && workflow.State != WorkflowComplete {
			return nil, "", fmt.Errorf("deep workflow phases are incomplete; run acr review phase next")
		}
		dispositions, err := workflowFinalDispositions(workflow)
		if err != nil {
			return nil, "", err
		}
		draft.CandidateDispositions = append(draft.CandidateDispositions, dispositions...)
		sort.Slice(draft.CandidateDispositions, func(i, j int) bool {
			return draft.CandidateDispositions[i].CandidateID < draft.CandidateDispositions[j].CandidateID
		})
	} else if request.ProtocolVersion == AdaptiveProtocolVersion && request.Instructions.ReviewProfile == ReviewProfileAdaptive {
		workflow, err := LoadWorkflow(repo, sessionID)
		if err != nil {
			return nil, "", err
		}
		if workflow.State != WorkflowReady && workflow.State != WorkflowComplete {
			return nil, "", fmt.Errorf("adaptive workflow phases are incomplete; run acr review phase next")
		}
		generated, err := adaptiveSubmission(request, workflow)
		if err != nil {
			return nil, "", err
		}
		draft = generated
	}
	if request.ProtocolVersion != AdaptiveProtocolVersion {
		for _, question := range request.ReviewQuestions {
			draft.QuestionResolutions = append(draft.QuestionResolutions, QuestionResolution{QuestionID: question.ID})
		}
	}
	data, err := json.MarshalIndent(draft, "", "  ")
	if err != nil {
		return nil, "", fmt.Errorf("encode findings draft: %w", err)
	}
	data = append(data, '\n')
	path := filepath.Join(SessionDir(repo, sessionID), FindingsFileName)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if os.IsExist(err) {
		return nil, path, fmt.Errorf("findings draft already exists: %s", path)
	}
	if err != nil {
		return nil, path, fmt.Errorf("create findings draft: %w", err)
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return nil, path, fmt.Errorf("write findings draft: %w", err)
	}
	if err := file.Close(); err != nil {
		return nil, path, fmt.Errorf("close findings draft: %w", err)
	}
	return draft, path, nil
}

func ListSessions(repo string) ([]SessionSummary, error) {
	root := filepath.Join(repo, ".acr", "sessions")
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return []SessionSummary{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	summaries := make([]SessionSummary, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		request, loadErr := LoadRequest(repo, entry.Name())
		if loadErr != nil {
			continue
		}
		state := "prepared"
		if _, statErr := os.Stat(filepath.Join(root, entry.Name(), ResultFileName)); statErr == nil {
			state = "validated"
		}
		summaries = append(summaries, SessionSummary{
			SessionID: request.SessionID, CreatedAt: request.CreatedAt, Mode: request.Mode,
			Units: len(request.Units), State: state,
		})
	}
	sort.Slice(summaries, func(i, j int) bool { return summaries[i].CreatedAt.After(summaries[j].CreatedAt) })
	return summaries, nil
}

func SessionDir(repo, sessionID string) string {
	return filepath.Join(repo, ".acr", "sessions", sessionID)
}

func newSessionID() (string, error) {
	random := make([]byte, 6)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("generate session id: %w", err)
	}
	return time.Now().UTC().Format("20060102T150405") + "-" + hex.EncodeToString(random), nil
}

func resolveRepoFile(root, path string) (string, string, error) {
	rel, full, err := resolveRepoPath(root, path)
	if err != nil {
		return "", "", err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(full))
	if err != nil {
		return "", "", fmt.Errorf("resolve parent directory: %w", err)
	}
	parentRel, err := filepath.Rel(root, parent)
	if err != nil || parentRel == ".." || strings.HasPrefix(parentRel, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("path escapes repository through a symbolic link")
	}
	return rel, full, nil
}

func resolveRepoPath(root, path string) (string, string, error) {
	path = filepath.Clean(filepath.FromSlash(strings.TrimSpace(path)))
	if path == "." || path == "" || filepath.IsAbs(path) {
		return "", "", fmt.Errorf("path must be repository-relative")
	}
	full := filepath.Join(root, path)
	rel, err := filepath.Rel(root, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("path escapes repository")
	}
	return filepath.ToSlash(rel), full, nil
}

func readRegularFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("symbolic links are not reviewable")
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("path is not a regular file")
	}
	return os.ReadFile(path)
}

func contentSHA256(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func countLines(content []byte) int {
	if len(content) == 0 {
		return 0
	}
	count := 1
	for _, b := range content {
		if b == '\n' {
			count++
		}
	}
	if content[len(content)-1] == '\n' {
		count--
	}
	return count
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", filepath.Base(path), err)
	}
	data = append(data, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("publish %s: %w", filepath.Base(path), err)
	}
	return nil
}

func readJSON(path string, value any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", filepath.Base(path), err)
	}
	if err := json.Unmarshal(data, value); err != nil {
		return fmt.Errorf("decode %s: %w", filepath.Base(path), err)
	}
	return nil
}
