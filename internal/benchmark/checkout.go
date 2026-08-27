// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package benchmark

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ResolveGitHubPR resolves mutable PR metadata to immutable base and head SHAs.
func ResolveGitHubPR(ctx context.Context, client *http.Client, benchmarkCase Case) (Case, error) {
	parsed, err := url.Parse(benchmarkCase.PRURL)
	if err != nil || !strings.EqualFold(parsed.Host, "github.com") {
		return Case{}, fmt.Errorf("a GitHub pull request URL is required")
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) != 4 || parts[2] != "pull" || parts[0] == "" || parts[1] == "" || parts[3] == "" {
		return Case{}, fmt.Errorf("a GitHub pull request URL is required")
	}
	apiURL := "https://api.github.com/repos/" + url.PathEscape(parts[0]) + "/" + url.PathEscape(parts[1]) + "/pulls/" + url.PathEscape(parts[3])
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return Case{}, fmt.Errorf("create GitHub PR request: %w", err)
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if token := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return Case{}, fmt.Errorf("resolve GitHub PR: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return Case{}, fmt.Errorf("resolve GitHub PR: HTTP %s: %s", response.Status, strings.TrimSpace(string(body)))
	}
	var payload struct {
		Base struct {
			SHA  string `json:"sha"`
			Repo struct {
				CloneURL string `json:"clone_url"`
			} `json:"repo"`
		} `json:"base"`
		Head struct {
			SHA  string `json:"sha"`
			Repo struct {
				CloneURL string `json:"clone_url"`
			} `json:"repo"`
		} `json:"head"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 2<<20))
	if err := decoder.Decode(&payload); err != nil {
		return Case{}, fmt.Errorf("decode GitHub PR metadata: %w", err)
	}
	if payload.Base.SHA == "" || payload.Head.SHA == "" {
		return Case{}, fmt.Errorf("GitHub PR response does not contain base and head SHAs")
	}
	benchmarkCase.BaseSHA = payload.Base.SHA
	benchmarkCase.HeadSHA = payload.Head.SHA
	if payload.Base.Repo.CloneURL != "" {
		benchmarkCase.Repository = payload.Base.Repo.CloneURL
	} else if payload.Head.Repo.CloneURL != "" {
		benchmarkCase.Repository = payload.Head.Repo.CloneURL
	}
	if benchmarkCase.Repository == "" {
		benchmarkCase.Repository = "https://github.com/" + parts[0] + "/" + parts[1] + ".git"
	}
	return benchmarkCase, nil
}

func prepareRemoteRepository(ctx context.Context, benchmarkCase Case, cacheDirectory string) (string, error) {
	if strings.TrimSpace(benchmarkCase.Repository) == "" {
		return "", fmt.Errorf("benchmark case %s has no repository URL", benchmarkCase.ID)
	}
	if cacheDirectory == "" {
		userCache, err := os.UserCacheDir()
		if err != nil {
			return "", fmt.Errorf("resolve user cache: %w", err)
		}
		cacheDirectory = filepath.Join(userCache, "acr", "benchmarks", "repos")
	}
	cacheDirectory, err := filepath.Abs(cacheDirectory)
	if err != nil {
		return "", fmt.Errorf("resolve repository cache: %w", err)
	}
	if err := os.MkdirAll(cacheDirectory, 0o700); err != nil {
		return "", fmt.Errorf("create repository cache: %w", err)
	}
	digest := sha256.Sum256([]byte(benchmarkCase.Repository))
	cachePath := filepath.Join(cacheDirectory, hex.EncodeToString(digest[:12])+".git")
	if _, err := os.Stat(cachePath); os.IsNotExist(err) {
		temporaryRoot, err := os.MkdirTemp(cacheDirectory, ".clone-*")
		if err != nil {
			return "", fmt.Errorf("create clone directory: %w", err)
		}
		defer os.RemoveAll(temporaryRoot)
		temporaryRepository := filepath.Join(temporaryRoot, "repository.git")
		command := exec.CommandContext(ctx, "git", "clone", "--mirror", "--", benchmarkCase.Repository, temporaryRepository)
		if output, err := command.CombinedOutput(); err != nil {
			return "", fmt.Errorf("clone benchmark repository: %w: %s", err, strings.TrimSpace(string(output)))
		}
		if err := os.Rename(temporaryRepository, cachePath); err != nil {
			if _, statErr := os.Stat(cachePath); statErr != nil {
				return "", fmt.Errorf("install repository cache: %w", err)
			}
		}
	} else if err != nil {
		return "", fmt.Errorf("inspect repository cache: %w", err)
	} else {
		if output, updateErr := runGitCommand(ctx, cachePath, "remote", "update", "--prune"); updateErr != nil {
			return "", fmt.Errorf("update benchmark repository: %w: %s", updateErr, strings.TrimSpace(output))
		}
	}
	if err := verifyCachedCommit(ctx, cachePath, benchmarkCase.BaseSHA, "base"); err != nil {
		return "", err
	}
	if err := verifyCachedCommit(ctx, cachePath, benchmarkCase.HeadSHA, "head"); err != nil {
		return "", err
	}
	return cachePath, nil
}

func verifyCachedCommit(ctx context.Context, repository, revision, label string) error {
	if strings.TrimSpace(revision) == "" {
		return fmt.Errorf("%s commit is not pinned", label)
	}
	if _, err := runGitCommand(ctx, repository, "cat-file", "-e", revision+"^{commit}"); err == nil {
		return nil
	}
	if output, err := runGitCommand(ctx, repository, "fetch", "origin", revision); err != nil {
		return fmt.Errorf("fetch %s commit %s: %w: %s", label, revision, err, strings.TrimSpace(output))
	}
	if _, err := runGitCommand(ctx, repository, "cat-file", "-e", revision+"^{commit}"); err != nil {
		return fmt.Errorf("%s commit %s is unavailable", label, revision)
	}
	return nil
}
