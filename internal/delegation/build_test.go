// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package delegation

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildWorkspacePreparesReviewableDiffWithoutProviderConfig(t *testing.T) {
	repo := initBuildTestRepo(t)
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	if err := os.WriteFile(filepath.Join(repo, "app.go"), []byte("package app\nfunc Value() int { return 2 }\n"), 0o644); err != nil {
		t.Fatalf("modify source: %v", err)
	}

	request, err := Build(context.Background(), BuildOptions{RepoDir: repo})
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}
	if request.Mode != ModeDiff || request.Repository.Revision == "" {
		t.Fatalf("unexpected request metadata: %#v", request)
	}
	if len(request.Units) != 1 || len(request.Units[0].Files) != 1 {
		t.Fatalf("unexpected units: %#v", request.Units)
	}
	file := request.Units[0].Files[0]
	if file.Path != "app.go" || !strings.Contains(file.Diff, "+func Value() int { return 2 }") {
		t.Fatalf("unexpected diff file: %#v", file)
	}
	if request.Units[0].Rule == "" {
		t.Fatal("resolved review rule is empty")
	}
}

func TestBuildScanSupportsNonGitDirectoryAndPathSelection(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "src"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "src", "app.go"), []byte("package app\n"), 0o644); err != nil {
		t.Fatalf("write app: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("ignore this selection\n"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}

	request, err := Build(context.Background(), BuildOptions{RepoDir: repo, Paths: []string{"src"}})
	if err != nil {
		t.Fatalf("Build(scan) error: %v", err)
	}
	if request.Mode != ModeScan || len(request.Units) != 1 || len(request.Units[0].Files) != 1 {
		t.Fatalf("unexpected scan request: %#v", request)
	}
	if got := request.Units[0].Files[0]; got.Path != "src/app.go" || got.Diff != "" {
		t.Fatalf("unexpected scan file: %#v", got)
	}
}

func TestBuildScanDotReviewsWholeDirectoryAndExcludesSessionArtifacts(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "app.go"), []byte("package app\n"), 0o644); err != nil {
		t.Fatalf("write app: %v", err)
	}
	artifactDir := filepath.Join(repo, ".acr", "sessions", "old")
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		t.Fatalf("mkdir artifact: %v", err)
	}
	if err := os.WriteFile(filepath.Join(artifactDir, "request.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write artifact: %v", err)
	}

	request, err := Build(context.Background(), BuildOptions{RepoDir: repo, Paths: []string{"."}})
	if err != nil {
		t.Fatalf("Build(scan all) error: %v", err)
	}
	var paths []string
	for _, unit := range request.Units {
		for _, file := range unit.Files {
			paths = append(paths, file.Path)
		}
	}
	if len(paths) != 1 || paths[0] != "app.go" {
		t.Fatalf("scan paths = %#v, want only app.go", paths)
	}
}

func TestBuildSupportsRangeAndCommitTargets(t *testing.T) {
	repo := initBuildTestRepo(t)
	base := gitOutput(t, repo, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(repo, "app.go"), []byte("package app\nfunc Value() int { return 2 }\n"), 0o644); err != nil {
		t.Fatalf("modify source: %v", err)
	}
	runGit(t, repo, "add", "app.go")
	runGit(t, repo, "commit", "-m", "change value")
	head := gitOutput(t, repo, "rev-parse", "HEAD")

	for name, options := range map[string]BuildOptions{
		"range":  {RepoDir: repo, From: base, To: head},
		"commit": {RepoDir: repo, Commit: head},
	} {
		t.Run(name, func(t *testing.T) {
			request, err := Build(context.Background(), options)
			if err != nil {
				t.Fatalf("Build() error: %v", err)
			}
			if request.Mode != ModeDiff || request.Repository.Revision == "" || len(request.Units) != 1 {
				t.Fatalf("unexpected request: %#v", request)
			}
			if got := request.Units[0].Files[0]; got.Path != "app.go" || !strings.Contains(got.Diff, "+func Value() int { return 2 }") {
				t.Fatalf("unexpected target file: %#v", got)
			}
		})
	}
}

func TestBuildWorkspaceIncludesUntrackedAndRenamedFiles(t *testing.T) {
	repo := initBuildTestRepo(t)
	runGit(t, repo, "mv", "app.go", "renamed.go")
	if err := os.WriteFile(filepath.Join(repo, "new.go"), []byte("package app\nfunc New() {}\n"), 0o644); err != nil {
		t.Fatalf("write untracked source: %v", err)
	}

	request, err := Build(context.Background(), BuildOptions{RepoDir: repo})
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}
	paths := map[string]bool{}
	for _, unit := range request.Units {
		for _, file := range unit.Files {
			paths[file.Path] = true
		}
	}
	if !paths["renamed.go"] || !paths["new.go"] {
		t.Fatalf("prepared paths = %#v, want renamed.go and new.go", paths)
	}
}

func TestBuildCommitSnapshotsTargetWhenWorktreeHasAdvanced(t *testing.T) {
	repo := initBuildTestRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "app.go"), []byte("package app\nfunc Value() int { return 2 }\n"), 0o644); err != nil {
		t.Fatalf("write target revision: %v", err)
	}
	runGit(t, repo, "add", "app.go")
	runGit(t, repo, "commit", "-m", "target revision")
	target := gitOutput(t, repo, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(repo, "app.go"), []byte("package app\nfunc Value() int { return 3 }\n"), 0o644); err != nil {
		t.Fatalf("advance worktree: %v", err)
	}

	request, err := Build(context.Background(), BuildOptions{RepoDir: repo, Commit: target})
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}
	file := request.Units[0].Files[0]
	if !strings.Contains(file.Content, "return 2") || file.ValidateWorkspace {
		t.Fatalf("unexpected immutable snapshot: %#v", file)
	}
	result, err := Submit(repo, request.SessionID, Submission{
		ProtocolVersion: ProtocolVersion, SessionID: request.SessionID, Findings: []Finding{},
	})
	if err != nil || len(result.Rejected) != 0 {
		t.Fatalf("Submit() = %#v, %v; immutable target should remain valid", result, err)
	}
}

func TestBuildRejectsEmptyReviewSelection(t *testing.T) {
	repo := initBuildTestRepo(t)
	if _, err := Build(context.Background(), BuildOptions{RepoDir: repo}); err == nil || !strings.Contains(err.Error(), "no reviewable files") {
		t.Fatalf("Build() error = %v, want no reviewable files", err)
	}
}

func initBuildTestRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	commands := [][]string{
		{"init"},
		{"config", "user.email", "acr@example.test"},
		{"config", "user.name", "ACR Test"},
	}
	for _, args := range commands {
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "app.go"), []byte("package app\nfunc Value() int { return 1 }\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	for _, args := range [][]string{{"add", "app.go"}, {"commit", "-m", "initial"}} {
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	return repo
}

func runGit(t *testing.T, repo string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
}

func gitOutput(t *testing.T, repo string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}
