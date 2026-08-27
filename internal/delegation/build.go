// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package delegation

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	allowedext "github.com/alibaba/open-code-review/internal/config/allowlist"
	"github.com/alibaba/open-code-review/internal/config/rules"
	"github.com/alibaba/open-code-review/internal/delegation/source"
	"github.com/alibaba/open-code-review/internal/gitcmd"
	"github.com/alibaba/open-code-review/internal/model"
)

const maxFilesPerUnit = 10

type BuildOptions struct {
	RepoDir      string
	From         string
	To           string
	Commit       string
	Paths        []string
	RulePath     string
	Background   string
	Profile      string
	TokenEconomy TokenEconomy
	MaxGitProcs  int
}

func Build(ctx context.Context, options BuildOptions) (*Request, error) {
	if len(options.Paths) > 0 && (options.From != "" || options.To != "" || options.Commit != "") {
		return nil, fmt.Errorf("--path cannot be combined with diff refs")
	}
	if (options.From == "") != (options.To == "") {
		return nil, fmt.Errorf("--from and --to must be provided together")
	}
	if options.Commit != "" && options.From != "" {
		return nil, fmt.Errorf("--commit cannot be combined with --from/--to")
	}

	repo, err := resolveBuildRoot(options.RepoDir, len(options.Paths) == 0)
	if err != nil {
		return nil, err
	}
	runner := gitcmd.New(options.MaxGitProcs)
	contentRef := options.To
	if options.Commit != "" {
		contentRef = options.Commit
	}
	resolver, filter, err := rules.NewResolver(repo, options.RulePath, rules.ResolverOptions{Ref: contentRef, Runner: runner})
	if err != nil {
		return nil, fmt.Errorf("load review rules: %w", err)
	}

	if len(options.Paths) > 0 {
		options.Paths = normalizeScanPaths(options.Paths)
		return buildScan(ctx, repo, options, runner, resolver, filter)
	}
	return buildDiff(ctx, repo, options, runner, resolver, filter)
}

func normalizeScanPaths(paths []string) []string {
	normalized := make([]string, 0, len(paths))
	for _, path := range paths {
		path = strings.TrimSpace(filepath.ToSlash(path))
		if path == "." || path == "./" || path == "" {
			continue
		}
		normalized = append(normalized, path)
	}
	return normalized
}

func buildDiff(ctx context.Context, repo string, options BuildOptions, runner *gitcmd.Runner, resolver rules.Resolver, filter *rules.FileFilter) (*Request, error) {
	provider := diffProvider(repo, options, runner)
	diffs, err := provider.GetDiff(ctx)
	if err != nil {
		return nil, fmt.Errorf("load diff: %w", err)
	}
	files := make([]PreparedFile, 0, len(diffs))
	immutableRef := options.Commit != "" || options.From != ""
	for _, item := range diffs {
		path := effectiveDiffPath(item)
		if !item.IsDeleted && !item.IsBinary && reviewablePath(path, filter) {
			content := item.NewFileContent
			files = append(files, PreparedFile{
				Path: path, Diff: item.Diff, SnapshotContent: &content, ImmutableRef: immutableRef,
			})
		}
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no reviewable files found")
	}
	resolved := provider.ResolveInput(ctx)
	revision := resolved.ExactRange
	if revision == "" {
		revision = resolved.ResolvedHead
	}
	if revision == "" {
		revision = resolved.ResolvedBase
	}
	return Prepare(repo, PrepareInput{
		Mode: ModeDiff, Revision: revision, Background: options.Background,
		Profile: options.Profile, TokenEconomy: options.TokenEconomy,
		Units: groupPreparedFiles(files, resolver),
	})
}

func buildScan(ctx context.Context, repo string, options BuildOptions, runner *gitcmd.Runner, resolver rules.Resolver, filter *rules.FileFilter) (*Request, error) {
	provider := source.NewScanProvider(repo, options.Paths, runner, source.DefaultMaxFileSizeBytes)
	items, err := provider.Enumerate(ctx)
	if err != nil {
		return nil, fmt.Errorf("enumerate scan files: %w", err)
	}
	files := make([]PreparedFile, 0, len(items))
	for _, item := range items {
		if !item.IsBinary && reviewablePath(item.Path, filter) {
			content := item.Content
			files = append(files, PreparedFile{Path: item.Path, SnapshotContent: &content})
		}
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no reviewable files found")
	}
	return Prepare(repo, PrepareInput{
		Mode: ModeScan, Revision: gitRevision(ctx, repo), Background: options.Background,
		Profile: options.Profile, TokenEconomy: options.TokenEconomy,
		Units: groupPreparedFiles(files, resolver),
	})
}

func groupPreparedFiles(files []PreparedFile, resolver rules.Resolver) []PreparedUnit {
	type ruleGroup struct {
		rule  string
		files []PreparedFile
	}
	groupsByRule := map[string]*ruleGroup{}
	order := make([]string, 0)
	for _, file := range files {
		rule := resolver.Resolve(file.Path)
		group := groupsByRule[rule]
		if group == nil {
			group = &ruleGroup{rule: rule}
			groupsByRule[rule] = group
			order = append(order, rule)
		}
		group.files = append(group.files, file)
	}
	units := make([]PreparedUnit, 0, len(files))
	unitNo := 1
	for _, rule := range order {
		group := groupsByRule[rule]
		for start := 0; start < len(group.files); start += maxFilesPerUnit {
			end := min(start+maxFilesPerUnit, len(group.files))
			units = append(units, PreparedUnit{
				ID: fmt.Sprintf("unit-%04d", unitNo), Rule: group.rule,
				Files: append([]PreparedFile(nil), group.files[start:end]...),
			})
			unitNo++
		}
	}
	return units
}

func diffProvider(repo string, options BuildOptions, runner *gitcmd.Runner) *source.Provider {
	switch {
	case options.Commit != "":
		return source.NewCommitProvider(repo, options.Commit, runner)
	case options.From != "":
		return source.NewProvider(repo, options.From, options.To, runner)
	default:
		return source.NewWorkspaceProvider(repo, runner)
	}
}

func reviewablePath(path string, filter *rules.FileFilter) bool {
	if filter != nil && filter.IsUserExcluded(path) {
		return false
	}
	if filter != nil && filter.HasInclude() && filter.IsUserIncluded(path) {
		return true
	}
	base := filepath.Base(path)
	ext := ""
	if dot := strings.LastIndex(base, "."); dot > 0 {
		ext = strings.ToLower(base[dot:])
	}
	if ext != "" && !allowedext.IsAllowedExt(ext) {
		return false
	}
	return !allowedext.IsExcludedPath(path)
}

func effectiveDiffPath(item model.Diff) string {
	if item.NewPath == "/dev/null" {
		return item.OldPath
	}
	return item.NewPath
}

func resolveBuildRoot(input string, requireGit bool) (string, error) {
	if input == "" {
		input = "."
	}
	abs, err := filepath.Abs(input)
	if err != nil {
		return "", fmt.Errorf("resolve repository: %w", err)
	}
	if !requireGit {
		return abs, nil
	}
	cmd := exec.Command("git", "-C", abs, "rev-parse", "--show-toplevel")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%s is not a git work tree", abs)
	}
	return strings.TrimSpace(string(out)), nil
}

func gitRevision(ctx context.Context, repo string) string {
	cmd := exec.CommandContext(ctx, "git", "-C", repo, "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
