// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package benchmark

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	mathrand "math/rand"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/alibaba/open-code-review/internal/delegation"
)

const (
	ArmBaseline = "baseline"
	ArmACR      = "acr"
	ArmJudge    = "judge"

	TaskQueued            = "queued"
	TaskClaimed           = "claimed"
	TaskSubmitted         = "submitted"
	TaskNeedsAdjudication = "needs_adjudication"
	TaskScored            = "scored"
	TaskFailed            = "failed"
)

const runFileName = "run.json"

type PrepareRunOptions struct {
	DatasetPath         string
	PRURL               string
	Limit               int
	All                 bool
	Trials              int
	Seed                int64
	Repository          string
	RepositoryOverrides map[string]string
	CacheDir            string
}

type Selection struct {
	PRURL string
	Limit int
	All   bool
	Seed  int64
}

type Run struct {
	ProtocolVersion string          `json:"protocol_version"`
	ID              string          `json:"id"`
	CreatedAt       time.Time       `json:"created_at"`
	Dataset         DatasetMetadata `json:"dataset"`
	Seed            int64           `json:"seed"`
	Trials          int             `json:"trials"`
	Workspace       string          `json:"workspace"`
	Cases           []Case          `json:"cases"`
	Tasks           []Task          `json:"tasks"`
	Evaluations     []Evaluation    `json:"evaluations,omitempty"`
	SetupFailures   []SetupFailure  `json:"setup_failures,omitempty"`
}

type SetupFailure struct {
	CaseID  string `json:"case_id"`
	Message string `json:"message"`
}

type Task struct {
	ID             string    `json:"id"`
	CaseID         string    `json:"case_id"`
	Trial          int       `json:"trial"`
	Arm            string    `json:"arm"`
	State          string    `json:"state"`
	BaseSHA        string    `json:"base_sha"`
	HeadSHA        string    `json:"head_sha"`
	CheckoutPath   string    `json:"checkout_path"`
	PromptPath     string    `json:"prompt_path"`
	ReviewSession  string    `json:"review_session,omitempty"`
	Worker         string    `json:"worker,omitempty"`
	ClaimedAt      time.Time `json:"claimed_at,omitempty"`
	LeaseExpiresAt time.Time `json:"lease_expires_at,omitempty"`
	Executor       Executor  `json:"executor,omitempty"`
	SubmissionPath string    `json:"submission_path,omitempty"`
	SubmissionSHA  string    `json:"submission_sha256,omitempty"`
	SourceTreeSHA  string    `json:"source_tree_sha256"`
	Rejections     []Repair  `json:"rejections,omitempty"`
	JudgeBatchID   string    `json:"judge_batch_id,omitempty"`
	JudgeRound     int       `json:"judge_round,omitempty"`
	PairIDs        []string  `json:"pair_ids,omitempty"`
}

type Evaluation struct {
	ID        string          `json:"id"`
	TaskID    string          `json:"task_id"`
	CaseID    string          `json:"case_id"`
	Trial     int             `json:"trial"`
	Arm       string          `json:"arm"`
	Analysis  FindingAnalysis `json:"analysis"`
	Score     Score           `json:"score"`
	Judgments []Judgment      `json:"judgments,omitempty"`
}

type Executor struct {
	Host      string `json:"host"`
	Model     string `json:"model"`
	ContextID string `json:"context_id"`
}

type TaskSubmission struct {
	ProtocolVersion string     `json:"protocol_version"`
	RunID           string     `json:"run_id"`
	TaskID          string     `json:"task_id"`
	Executor        Executor   `json:"executor"`
	Findings        []Finding  `json:"findings,omitempty"`
	Judgments       []Judgment `json:"judgments,omitempty"`
}

type Judgment struct {
	PairID     string  `json:"pair_id"`
	Decision   string  `json:"decision"`
	Rationale  string  `json:"rationale"`
	Confidence float64 `json:"confidence"`
}

type Repair struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type RepairError struct {
	ProtocolVersion string `json:"protocol_version"`
	TaskID          string `json:"task_id"`
	Accepted        bool   `json:"accepted"`
	Code            string `json:"code"`
	Message         string `json:"message"`
}

func (repair *RepairError) Error() string {
	data, err := json.Marshal(repair)
	if err != nil {
		return repair.Code + ": " + repair.Message
	}
	return string(data)
}

func PrepareRun(ctx context.Context, workspace string, options PrepareRunOptions) (*Run, error) {
	if strings.TrimSpace(options.DatasetPath) == "" {
		return nil, fmt.Errorf("--dataset is required")
	}
	manifest, err := LoadManifest(options.DatasetPath)
	if err != nil {
		return nil, err
	}
	selected := SelectCases(manifest.Cases, Selection{PRURL: options.PRURL, Limit: options.Limit, All: options.All, Seed: options.Seed})
	if len(selected) == 0 {
		return nil, fmt.Errorf("select cases with exactly one of --pr, --limit, or --all")
	}
	if options.Trials == 0 {
		options.Trials = 1
	}
	if options.Trials < 1 {
		return nil, fmt.Errorf("--trials must be at least 1")
	}
	root, err := filepath.Abs(workspace)
	if err != nil {
		return nil, fmt.Errorf("resolve benchmark workspace: %w", err)
	}
	runID, err := newRunID()
	if err != nil {
		return nil, err
	}
	run := &Run{
		ProtocolVersion: BenchmarkProtocolVersion, ID: runID, CreatedAt: time.Now().UTC(),
		Dataset: manifest.Dataset, Seed: options.Seed, Trials: options.Trials, Workspace: root,
		Cases: selected, Tasks: make([]Task, 0, len(selected)*options.Trials*2),
	}
	runRoot := RunDir(root, runID)
	if err := os.MkdirAll(filepath.Join(runRoot, "tasks"), 0o700); err != nil {
		return nil, fmt.Errorf("create benchmark run: %w", err)
	}
	for caseIndex, benchmarkCase := range selected {
		if benchmarkCase.BaseSHA == "" || benchmarkCase.HeadSHA == "" {
			resolved, resolveErr := ResolveGitHubPR(ctx, http.DefaultClient, benchmarkCase)
			if resolveErr != nil {
				run.SetupFailures = append(run.SetupFailures, SetupFailure{CaseID: benchmarkCase.ID, Message: resolveErr.Error()})
				continue
			}
			benchmarkCase = resolved
			run.Cases[caseIndex] = resolved
		}
		repository := options.Repository
		if override := options.RepositoryOverrides[benchmarkCase.ID]; override != "" {
			repository = override
		}
		if repository == "" && isLocalRepository(benchmarkCase.Repository) {
			repository = benchmarkCase.Repository
		}
		if repository == "" {
			repository, err = prepareRemoteRepository(ctx, benchmarkCase, options.CacheDir)
			if err != nil {
				run.SetupFailures = append(run.SetupFailures, SetupFailure{CaseID: benchmarkCase.ID, Message: err.Error()})
				continue
			}
		}
		for trial := 1; trial <= options.Trials; trial++ {
			pair := make([]Task, 0, 2)
			pairFailed := false
			for _, arm := range []string{ArmBaseline, ArmACR} {
				task, taskErr := prepareReviewTask(ctx, runRoot, repository, benchmarkCase, trial, arm)
				if taskErr != nil {
					run.SetupFailures = append(run.SetupFailures, SetupFailure{CaseID: benchmarkCase.ID, Message: taskErr.Error()})
					pairFailed = true
					break
				}
				pair = append(pair, task)
			}
			if pairFailed {
				cleanupPreparedPair(ctx, repository, runRoot, benchmarkCase.ID, trial)
				continue
			}
			run.Tasks = append(run.Tasks, pair...)
		}
	}
	orderTasks(run.Tasks, options.Seed)
	if err := SaveRun(root, run); err != nil {
		return nil, err
	}
	return run, nil
}

func cleanupPreparedPair(ctx context.Context, repository, runRoot, caseID string, trial int) {
	for _, arm := range []string{ArmBaseline, ArmACR} {
		taskID := fmt.Sprintf("%s-t%02d-%s", caseID, trial, arm)
		checkout := filepath.Join(runRoot, "tasks", taskID, "checkout")
		if _, err := os.Stat(checkout); err == nil {
			_, _ = runGitCommand(ctx, repository, "worktree", "remove", "--force", checkout)
		}
	}
	_, _ = runGitCommand(ctx, repository, "worktree", "prune")
}

func SelectCases(cases []Case, selection Selection) []Case {
	if strings.TrimSpace(selection.PRURL) != "" {
		for _, benchmarkCase := range cases {
			if benchmarkCase.PRURL == selection.PRURL {
				return []Case{benchmarkCase}
			}
		}
		return nil
	}
	if selection.All {
		return append([]Case(nil), cases...)
	}
	if selection.Limit < 1 {
		return nil
	}
	selected := append([]Case(nil), cases...)
	random := mathrand.New(mathrand.NewSource(selection.Seed))
	random.Shuffle(len(selected), func(i, j int) { selected[i], selected[j] = selected[j], selected[i] })
	if selection.Limit < len(selected) {
		selected = selected[:selection.Limit]
	}
	return selected
}

func prepareReviewTask(ctx context.Context, runRoot, repository string, benchmarkCase Case, trial int, arm string) (Task, error) {
	taskID := fmt.Sprintf("%s-t%02d-%s", benchmarkCase.ID, trial, arm)
	taskRoot := filepath.Join(runRoot, "tasks", taskID)
	checkout := filepath.Join(taskRoot, "checkout")
	if err := os.MkdirAll(taskRoot, 0o700); err != nil {
		return Task{}, fmt.Errorf("create task %s: %w", taskID, err)
	}
	if output, err := runGitCommand(ctx, repository, "worktree", "add", "--detach", checkout, benchmarkCase.HeadSHA); err != nil {
		return Task{}, fmt.Errorf("create checkout for %s: %w: %s", taskID, err, output)
	}
	task := Task{
		ID: taskID, CaseID: benchmarkCase.ID, Trial: trial, Arm: arm, State: TaskQueued,
		BaseSHA: benchmarkCase.BaseSHA, HeadSHA: benchmarkCase.HeadSHA, CheckoutPath: checkout,
		PromptPath: filepath.Join(taskRoot, "prompt.md"),
	}
	tree, err := trackedSourceSHA256(ctx, checkout)
	if err != nil {
		return Task{}, fmt.Errorf("fingerprint checkout for %s: %w", taskID, err)
	}
	task.SourceTreeSHA = tree
	var prompt string
	if arm == ArmACR {
		request, err := delegation.Build(ctx, delegation.BuildOptions{
			RepoDir: checkout, From: benchmarkCase.BaseSHA, To: benchmarkCase.HeadSHA,
			Profile: delegation.ReviewProfileDeep,
		})
		if err != nil {
			return Task{}, fmt.Errorf("prepare ACR task %s: %w", taskID, err)
		}
		task.ReviewSession = request.SessionID
		prompt, err = delegation.HandoffPrompt(request)
		if err != nil {
			return Task{}, fmt.Errorf("render ACR prompt %s: %w", taskID, err)
		}
		prompt += benchmarkSubmissionInstructions(task)
	} else {
		prompt = baselinePrompt(task)
	}
	if err := os.WriteFile(task.PromptPath, []byte(prompt), 0o600); err != nil {
		return Task{}, fmt.Errorf("write task prompt %s: %w", taskID, err)
	}
	return task, nil
}

func baselinePrompt(task Task) string {
	return fmt.Sprintf(`# Independent baseline code review

Review the changes between base %s and head %s in the repository at %s.
Find concrete correctness, security, performance, or reliability defects introduced by the change. Inspect relevant callers, callees, tests, and invariants. Do not modify files and do not use ACR guidance or another review result.
`, task.BaseSHA, task.HeadSHA, task.CheckoutPath) + benchmarkSubmissionInstructions(task)
}

func benchmarkSubmissionInstructions(task Task) string {
	return fmt.Sprintf(`
Return one JSON object with protocol_version "1", run_id supplied by the orchestrator, task_id %q, executor {host, model, context_id}, and findings. Each finding may contain title, description, explanation, evidence, file, start_line, end_line, severity, and confidence. Use an empty findings array when no concrete defect is found.
`, task.ID)
}

func orderTasks(tasks []Task, seed int64) {
	sort.SliceStable(tasks, func(i, j int) bool {
		left := sha256.Sum256([]byte(fmt.Sprintf("%d:%s", seed, tasks[i].ID)))
		right := sha256.Sum256([]byte(fmt.Sprintf("%d:%s", seed, tasks[j].ID)))
		return bytes.Compare(left[:], right[:]) < 0
	})
}

func RunDir(workspace, runID string) string {
	return filepath.Join(workspace, ".acr", "benchmarks", "runs", runID)
}

func SaveRun(workspace string, run *Run) error {
	if run == nil || !validIdentifier(run.ID) {
		return fmt.Errorf("invalid benchmark run")
	}
	path := filepath.Join(RunDir(workspace, run.ID), runFileName)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create benchmark run directory: %w", err)
	}
	data, err := json.MarshalIndent(run, "", "  ")
	if err != nil {
		return fmt.Errorf("encode benchmark run: %w", err)
	}
	data = append(data, '\n')
	temporary, err := os.CreateTemp(filepath.Dir(path), ".run-*")
	if err != nil {
		return fmt.Errorf("create run temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace benchmark run: %w", err)
	}
	return nil
}

func LoadRun(workspace, runID string) (*Run, error) {
	if !validIdentifier(runID) {
		return nil, fmt.Errorf("invalid benchmark run id")
	}
	data, err := os.ReadFile(filepath.Join(RunDir(workspace, runID), runFileName))
	if err != nil {
		return nil, fmt.Errorf("read benchmark run: %w", err)
	}
	var run Run
	if err := json.Unmarshal(data, &run); err != nil {
		return nil, fmt.Errorf("decode benchmark run: %w", err)
	}
	if run.ProtocolVersion != BenchmarkProtocolVersion || run.ID != runID {
		return nil, fmt.Errorf("invalid benchmark run protocol or id")
	}
	return &run, nil
}

func ClaimNextTask(workspace, runID, worker string, now time.Time, lease time.Duration) (*Task, error) {
	if strings.TrimSpace(worker) == "" || lease <= 0 {
		return nil, fmt.Errorf("worker and positive lease are required")
	}
	var claimed *Task
	err := withRunLock(workspace, runID, func() error {
		run, err := LoadRun(workspace, runID)
		if err != nil {
			return err
		}
		for index := range run.Tasks {
			task := &run.Tasks[index]
			if task.State != TaskQueued && !(task.State == TaskClaimed && !task.LeaseExpiresAt.After(now)) {
				continue
			}
			task.State = TaskClaimed
			task.Worker = worker
			task.ClaimedAt = now.UTC()
			task.LeaseExpiresAt = now.Add(lease).UTC()
			copy := *task
			claimed = &copy
			return SaveRun(workspace, run)
		}
		return fmt.Errorf("no benchmark tasks are ready")
	})
	return claimed, err
}

func SubmitTask(workspace, runID, taskID string, submission TaskSubmission) (*Task, error) {
	var submitted *Task
	err := withRunLock(workspace, runID, func() error {
		run, err := LoadRun(workspace, runID)
		if err != nil {
			return err
		}
		index := findTask(run.Tasks, taskID)
		if index < 0 {
			return fmt.Errorf("benchmark task %q not found", taskID)
		}
		task := &run.Tasks[index]
		if err := validateTaskSubmission(run, *task, submission); err != nil {
			repair := repairForError(task.ID, err)
			task.Rejections = append(task.Rejections, Repair{Code: repair.Code, Message: repair.Message})
			if saveErr := SaveRun(workspace, run); saveErr != nil {
				return saveErr
			}
			return repair
		}
		data, err := json.MarshalIndent(submission, "", "  ")
		if err != nil {
			return fmt.Errorf("encode benchmark submission: %w", err)
		}
		data = append(data, '\n')
		digest := sha256.Sum256(data)
		digestString := hex.EncodeToString(digest[:])
		if task.SubmissionSHA != "" {
			if task.SubmissionSHA == digestString {
				copy := *task
				submitted = &copy
				return nil
			}
			return fmt.Errorf("conflicting submission for task %q", task.ID)
		}
		if task.Arm != ArmJudge {
			if err := ensureTrackedFilesUnchanged(*task); err != nil {
				repair := &RepairError{ProtocolVersion: BenchmarkProtocolVersion, TaskID: task.ID, Code: "source_modified", Message: err.Error()}
				task.Rejections = append(task.Rejections, Repair{Code: repair.Code, Message: repair.Message})
				if saveErr := SaveRun(workspace, run); saveErr != nil {
					return saveErr
				}
				return repair
			}
		}
		path := filepath.Join(RunDir(workspace, runID), "tasks", task.ID, "submission.json")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			return fmt.Errorf("write benchmark submission: %w", err)
		}
		task.Executor = submission.Executor
		task.SubmissionPath = path
		task.SubmissionSHA = digestString
		task.State = TaskSubmitted
		task.Worker = ""
		task.LeaseExpiresAt = time.Time{}
		if err := advanceRun(workspace, run, task.ID); err != nil {
			return err
		}
		updatedIndex := findTask(run.Tasks, task.ID)
		copy := run.Tasks[updatedIndex]
		submitted = &copy
		return SaveRun(workspace, run)
	})
	return submitted, err
}

func validateTaskSubmission(run *Run, task Task, submission TaskSubmission) error {
	if submission.ProtocolVersion != BenchmarkProtocolVersion || submission.RunID != run.ID || submission.TaskID != task.ID {
		return fmt.Errorf("submission protocol, run_id, or task_id does not match the task")
	}
	if strings.TrimSpace(submission.Executor.Host) == "" || strings.TrimSpace(submission.Executor.Model) == "" || strings.TrimSpace(submission.Executor.ContextID) == "" {
		return fmt.Errorf("executor host, model, and context_id are required")
	}
	for _, other := range run.Tasks {
		if other.ID == task.ID || other.CaseID != task.CaseID || other.Trial != task.Trial || other.SubmissionSHA == "" {
			continue
		}
		compare := task.Arm == ArmJudge || (other.Arm != ArmJudge && other.Arm != task.Arm)
		if compare {
			if other.Executor.ContextID == submission.Executor.ContextID {
				return fmt.Errorf("paired reviews and judges require a fresh context")
			}
			if other.Executor.Host != submission.Executor.Host || other.Executor.Model != submission.Executor.Model {
				return fmt.Errorf("paired reviews and judges must use the same model and host")
			}
		}
	}
	if task.Arm == ArmJudge {
		if len(submission.Findings) != 0 {
			return fmt.Errorf("judge submissions must contain judgments, not findings")
		}
		seen := map[string]bool{}
		for _, judgment := range submission.Judgments {
			if judgment.Decision != DecisionMatch && judgment.Decision != DecisionNoMatch {
				return fmt.Errorf("unsupported judgment decision %q", judgment.Decision)
			}
			if seen[judgment.PairID] {
				return fmt.Errorf("duplicate judgment for pair %q", judgment.PairID)
			}
			seen[judgment.PairID] = true
		}
		for _, pairID := range task.PairIDs {
			if !seen[pairID] {
				return fmt.Errorf("judge submission must resolve pair %q", pairID)
			}
		}
		if len(seen) != len(task.PairIDs) {
			return fmt.Errorf("judge submission contains an unknown pair")
		}
		for _, judgment := range submission.Judgments {
			if strings.TrimSpace(judgment.Rationale) == "" || judgment.Confidence < 0 || judgment.Confidence > 1 {
				return fmt.Errorf("judge rationale and confidence from 0 to 1 are required")
			}
		}
	} else {
		if len(submission.Judgments) != 0 {
			return fmt.Errorf("reviewer submissions must contain findings, not judgments")
		}
		for index, finding := range submission.Findings {
			if strings.TrimSpace(finding.Explanation) == "" {
				return fmt.Errorf("finding %d requires an explanation", index)
			}
			path := filepath.FromSlash(strings.TrimSpace(finding.File))
			if path == "" || filepath.IsAbs(path) || filepath.Clean(path) == ".." || strings.HasPrefix(filepath.Clean(path), ".."+string(filepath.Separator)) {
				return fmt.Errorf("unsafe finding path at index %d", index)
			}
			full := filepath.Join(task.CheckoutPath, filepath.Clean(path))
			relative, err := filepath.Rel(task.CheckoutPath, full)
			if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				return fmt.Errorf("unsafe finding path at index %d", index)
			}
			data, err := os.ReadFile(full)
			if err != nil {
				return fmt.Errorf("finding %d references an unreadable file", index)
			}
			lineCount := 0
			if len(data) > 0 {
				lineCount = bytes.Count(data, []byte{'\n'})
				if data[len(data)-1] != '\n' {
					lineCount++
				}
			}
			if finding.StartLine < 1 || finding.EndLine < finding.StartLine || finding.EndLine > lineCount {
				return fmt.Errorf("finding %d has an invalid line range", index)
			}
			if finding.Confidence < 0 || finding.Confidence > 1 {
				return fmt.Errorf("finding %d confidence must be from 0 to 1", index)
			}
		}
	}
	return nil
}

func repairForError(taskID string, err error) *RepairError {
	message := err.Error()
	code := "invalid_submission"
	switch {
	case strings.Contains(message, "unsafe finding path"):
		code = "unsafe_finding_path"
	case strings.Contains(message, "line range"):
		code = "invalid_line_range"
	case strings.Contains(message, "unreadable file"):
		code = "unknown_finding_file"
	case strings.Contains(message, "fresh context"):
		code = "context_not_isolated"
	case strings.Contains(message, "same model"):
		code = "model_mismatch"
	case strings.Contains(message, "judgment"):
		code = "invalid_judgment"
	}
	return &RepairError{ProtocolVersion: BenchmarkProtocolVersion, TaskID: taskID, Code: code, Message: message}
}

func findTask(tasks []Task, id string) int {
	for index := range tasks {
		if tasks[index].ID == id {
			return index
		}
	}
	return -1
}

func ensureTrackedFilesUnchanged(task Task) error {
	output, err := runGitCommand(context.Background(), task.CheckoutPath, "status", "--porcelain", "--untracked-files=no")
	if err != nil {
		return fmt.Errorf("verify benchmark checkout: %w", err)
	}
	if strings.TrimSpace(output) != "" {
		return fmt.Errorf("benchmark task modified tracked source files: %s", strings.TrimSpace(output))
	}
	tree, err := trackedSourceSHA256(context.Background(), task.CheckoutPath)
	if err != nil {
		return fmt.Errorf("fingerprint benchmark checkout: %w", err)
	}
	if tree != task.SourceTreeSHA {
		return fmt.Errorf("benchmark task changed the pinned source tree")
	}
	return nil
}

func trackedSourceSHA256(ctx context.Context, checkout string) (string, error) {
	index, err := runGitCommand(ctx, checkout, "ls-files", "--stage", "-z")
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(index))
	return hex.EncodeToString(digest[:]), nil
}

func withRunLock(workspace, runID string, function func() error) error {
	lockPath := filepath.Join(RunDir(workspace, runID), ".lock")
	deadline := time.Now().Add(3 * time.Second)
	for {
		err := os.Mkdir(lockPath, 0o700)
		if err == nil {
			defer os.Remove(lockPath)
			return function()
		}
		if !os.IsExist(err) {
			return fmt.Errorf("lock benchmark run: %w", err)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("benchmark run is busy")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func isLocalRepository(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func runGitCommand(ctx context.Context, repository string, arguments ...string) (string, error) {
	command := exec.CommandContext(ctx, "git", arguments...)
	command.Dir = repository
	output, err := command.CombinedOutput()
	return string(output), err
}

func validIdentifier(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || character == '-' || character == '_' || character == '.' {
			continue
		}
		return false
	}
	return true
}

func newRunID() (string, error) {
	random := make([]byte, 6)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("generate benchmark run id: %w", err)
	}
	return time.Now().UTC().Format("20060102T150405") + "-" + hex.EncodeToString(random), nil
}
