// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package benchmark

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const (
	BenchmarkProtocolVersion = "1"
	QodoRevision             = "84dbe5c238400176884cb8dfb589dc308c308567"
	QodoSourceURL            = "https://huggingface.co/datasets/Qodo/PR-Review-Bench/resolve/" + QodoRevision + "/git_code_review_bench_100_w_open_prs.jsonl"
	QodoDatasetID            = "qodo-pr-review-bench"
	QodoLicense              = "MIT"
)

var casePartPattern = regexp.MustCompile(`[^a-z0-9]+`)

type DatasetMetadata struct {
	ID        string `json:"id"`
	Version   string `json:"version"`
	SourceURL string `json:"source_url,omitempty"`
	Revision  string `json:"revision,omitempty"`
	SHA256    string `json:"sha256,omitempty"`
	License   string `json:"license,omitempty"`
}

type Manifest struct {
	ProtocolVersion string          `json:"protocol_version"`
	Dataset         DatasetMetadata `json:"dataset"`
	Cases           []Case          `json:"cases"`
}

type qodoManifestCase struct {
	Repository  string `json:"repo"`
	PRURL       string `json:"pr_url_to_review"`
	IssueCount  int    `json:"num_of_issues"`
	IssueExists *int   `json:"-"`
	Issues      []struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		File        string `json:"file_path"`
		StartLine   *int   `json:"start_line"`
		EndLine     *int   `json:"end_line"`
	} `json:"issues"`
}

// ImportQodo converts Qodo's JSONL data into ACR's stable benchmark manifest.
func ImportQodo(reader io.Reader, metadata DatasetMetadata) (Manifest, error) {
	if strings.TrimSpace(metadata.ID) == "" || strings.TrimSpace(metadata.Version) == "" {
		return Manifest{}, fmt.Errorf("dataset id and version are required")
	}
	manifest := Manifest{ProtocolVersion: BenchmarkProtocolVersion, Dataset: metadata, Cases: make([]Case, 0)}
	seen := map[string]struct{}{}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	row := 0
	for scanner.Scan() {
		row++
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(line, &fields); err != nil {
			return Manifest{}, fmt.Errorf("decode Qodo row %d: %w", row, err)
		}
		var raw qodoManifestCase
		if err := json.Unmarshal(line, &raw); err != nil {
			return Manifest{}, fmt.Errorf("decode Qodo row %d: %w", row, err)
		}
		if strings.TrimSpace(raw.PRURL) == "" {
			return Manifest{}, fmt.Errorf("Qodo row %d: pr_url_to_review is required", row)
		}
		if _, ok := fields["num_of_issues"]; ok && raw.IssueCount != len(raw.Issues) {
			return Manifest{}, fmt.Errorf("Qodo row %d: num_of_issues=%d does not match %d issues", row, raw.IssueCount, len(raw.Issues))
		}
		caseID, repository, err := qodoCaseIdentity(raw.PRURL)
		if err != nil {
			return Manifest{}, fmt.Errorf("Qodo row %d: %w", row, err)
		}
		if _, exists := seen[caseID]; exists {
			return Manifest{}, fmt.Errorf("duplicate benchmark case %q", caseID)
		}
		seen[caseID] = struct{}{}
		benchmarkCase := Case{ID: caseID, Repository: repository, PRURL: raw.PRURL, Expected: make([]Finding, 0, len(raw.Issues))}
		for _, issue := range raw.Issues {
			finding := Finding{Title: strings.TrimSpace(issue.Title), Description: strings.TrimSpace(issue.Description), File: cleanPath(issue.File)}
			if issue.StartLine != nil {
				finding.StartLine = *issue.StartLine
			}
			if issue.EndLine != nil {
				finding.EndLine = *issue.EndLine
			}
			benchmarkCase.Expected = append(benchmarkCase.Expected, finding)
		}
		manifest.Cases = append(manifest.Cases, benchmarkCase)
	}
	if err := scanner.Err(); err != nil {
		return Manifest{}, fmt.Errorf("read Qodo dataset: %w", err)
	}
	if len(manifest.Cases) == 0 {
		return Manifest{}, fmt.Errorf("Qodo dataset contains no cases")
	}
	return manifest, nil
}

func qodoCaseIdentity(prURL string) (string, string, error) {
	parsed, err := url.Parse(prURL)
	if err != nil || !strings.EqualFold(parsed.Host, "github.com") {
		return "", "", fmt.Errorf("unsupported PR URL %q", prURL)
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) != 4 || parts[2] != "pull" {
		return "", "", fmt.Errorf("unsupported PR URL %q", prURL)
	}
	if _, err := strconv.Atoi(parts[3]); err != nil {
		return "", "", fmt.Errorf("invalid pull request number in %q", prURL)
	}
	owner := strings.ToLower(parts[0])
	repository := strings.ToLower(parts[1])
	id := strings.Trim(casePartPattern.ReplaceAllString("qodo-"+owner+"-"+repository+"-pr-"+parts[3], "-"), "-")
	return id, "https://github.com/" + parts[0] + "/" + parts[1] + ".git", nil
}

func SaveManifest(path string, manifest Manifest) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create manifest directory: %w", err)
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode benchmark manifest: %w", err)
	}
	data = append(data, '\n')
	temporary, err := os.CreateTemp(filepath.Dir(path), ".acr-manifest-*")
	if err != nil {
		return fmt.Errorf("create manifest temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("secure manifest temporary file: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return fmt.Errorf("write benchmark manifest: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close benchmark manifest: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace benchmark manifest: %w", err)
	}
	return nil
}

func LoadManifest(path string) (Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("read benchmark manifest: %w", err)
	}
	var manifest Manifest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode benchmark manifest: %w", err)
	}
	if err := validateManifest(manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func validateManifest(manifest Manifest) error {
	if manifest.ProtocolVersion != BenchmarkProtocolVersion {
		return fmt.Errorf("unsupported benchmark manifest protocol %q", manifest.ProtocolVersion)
	}
	if strings.TrimSpace(manifest.Dataset.ID) == "" || strings.TrimSpace(manifest.Dataset.Version) == "" {
		return fmt.Errorf("benchmark dataset id and version are required")
	}
	if manifest.Dataset.SHA256 != "" {
		decoded, err := hex.DecodeString(manifest.Dataset.SHA256)
		if err != nil || len(decoded) != sha256.Size {
			return fmt.Errorf("benchmark dataset sha256 must be 64 hexadecimal characters")
		}
	}
	seen := map[string]struct{}{}
	for index, benchmarkCase := range manifest.Cases {
		if strings.TrimSpace(benchmarkCase.ID) == "" || strings.TrimSpace(benchmarkCase.PRURL) == "" || strings.TrimSpace(benchmarkCase.Repository) == "" {
			return fmt.Errorf("benchmark case %d requires id, repository, and pr_url", index)
		}
		if _, exists := seen[benchmarkCase.ID]; exists {
			return fmt.Errorf("duplicate benchmark case %q", benchmarkCase.ID)
		}
		seen[benchmarkCase.ID] = struct{}{}
	}
	return nil
}

func FetchQodo(ctx context.Context, output string) (Manifest, error) {
	return fetchQodoFromURL(ctx, http.DefaultClient, QodoSourceURL, output)
}

func fetchQodoFromURL(ctx context.Context, client *http.Client, sourceURL, output string) (Manifest, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return Manifest{}, fmt.Errorf("create Qodo request: %w", err)
	}
	response, err := client.Do(request)
	if err != nil {
		return Manifest{}, fmt.Errorf("download Qodo dataset: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Manifest{}, fmt.Errorf("download Qodo dataset: HTTP %s", response.Status)
	}
	const maximumDatasetSize = 64 << 20
	raw, err := io.ReadAll(io.LimitReader(response.Body, maximumDatasetSize+1))
	if err != nil {
		return Manifest{}, fmt.Errorf("read Qodo dataset: %w", err)
	}
	if len(raw) > maximumDatasetSize {
		return Manifest{}, fmt.Errorf("Qodo dataset exceeds %d bytes", maximumDatasetSize)
	}
	sum := sha256.Sum256(raw)
	manifest, err := ImportQodo(bytes.NewReader(raw), DatasetMetadata{
		ID: QodoDatasetID, Version: QodoRevision, SourceURL: sourceURL,
		Revision: QodoRevision, SHA256: hex.EncodeToString(sum[:]), License: QodoLicense,
	})
	if err != nil {
		return Manifest{}, err
	}
	if err := SaveManifest(output, manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}
