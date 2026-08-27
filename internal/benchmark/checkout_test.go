// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package benchmark

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareRemoteRepositoryCachesAndVerifiesPinnedCommits(t *testing.T) {
	source, baseSHA, headSHA := benchmarkGitRepository(t)
	bare := filepath.Join(t.TempDir(), "source.git")
	command := exec.Command("git", "clone", "--bare", source, bare)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("clone bare fixture: %v\n%s", err, output)
	}
	repositoryURL := (&url.URL{Scheme: "file", Path: bare}).String()
	benchmarkCase := Case{ID: "case-1", Repository: repositoryURL, BaseSHA: baseSHA, HeadSHA: headSHA}
	cache := t.TempDir()

	prepared, err := prepareRemoteRepository(context.Background(), benchmarkCase, cache)
	if err != nil {
		t.Fatalf("prepareRemoteRepository: %v", err)
	}
	if !strings.HasPrefix(prepared, cache) {
		t.Fatalf("repository was not cached under %s: %s", cache, prepared)
	}
	if output, err := runGitCommand(context.Background(), prepared, "cat-file", "-e", headSHA+"^{commit}"); err != nil {
		t.Fatalf("cached head is unavailable: %v, %s", err, output)
	}

	missing := benchmarkCase
	missing.HeadSHA = strings.Repeat("f", 40)
	if _, err := prepareRemoteRepository(context.Background(), missing, cache); err == nil || !strings.Contains(err.Error(), "head commit") {
		t.Fatalf("missing head error = %v", err)
	}
}

func TestResolveGitHubPRPinsBaseHeadAndCloneURL(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != "https://api.github.com/repos/example/project/pulls/7" {
			t.Fatalf("unexpected GitHub URL: %s", request.URL)
		}
		body := `{"base":{"sha":"` + strings.Repeat("a", 40) + `","repo":{"clone_url":"https://github.com/example/project.git"}},"head":{"sha":"` + strings.Repeat("b", 40) + `","repo":{"clone_url":"https://github.com/contributor/project.git"}}}`
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	})}

	resolved, err := ResolveGitHubPR(context.Background(), client, Case{ID: "case-1", PRURL: "https://github.com/example/project/pull/7"})
	if err != nil {
		t.Fatalf("ResolveGitHubPR: %v", err)
	}
	if resolved.BaseSHA != strings.Repeat("a", 40) || resolved.HeadSHA != strings.Repeat("b", 40) || resolved.Repository != "https://github.com/example/project.git" {
		t.Fatalf("unexpected resolved case: %#v", resolved)
	}
}

func TestResolveGitHubPRRejectsUnsupportedAndIncompleteResponses(t *testing.T) {
	if _, err := ResolveGitHubPR(context.Background(), http.DefaultClient, Case{PRURL: "https://example.test/pull/1"}); err == nil || !strings.Contains(err.Error(), "GitHub pull request URL") {
		t.Fatalf("unsupported URL error = %v", err)
	}
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"base":{},"head":{}}`)), Request: request}, nil
	})}
	if _, err := ResolveGitHubPR(context.Background(), client, Case{PRURL: "https://github.com/example/project/pull/7"}); err == nil || !strings.Contains(err.Error(), "base and head") {
		t.Fatalf("incomplete response error = %v", err)
	}
}

func TestTrackedSourceMutationFailsSubmission(t *testing.T) {
	workspace, run := benchmarkPreparedRun(t)
	task := taskByArm(t, run, ArmBaseline)
	if err := os.WriteFile(filepath.Join(task.CheckoutPath, "review.go"), []byte("package review\n\nfunc Value() int { return 99 }\n"), 0o600); err != nil {
		t.Fatalf("mutate checkout: %v", err)
	}
	submission := TaskSubmission{
		ProtocolVersion: BenchmarkProtocolVersion, RunID: run.ID, TaskID: task.ID,
		Executor: Executor{Host: "codex", Model: "sol", ContextID: "context-1"}, Findings: []Finding{},
	}
	if _, err := SubmitTask(workspace, run.ID, task.ID, submission); err == nil || !strings.Contains(err.Error(), "modified tracked source") {
		t.Fatalf("source mutation error = %v", err)
	}
}
