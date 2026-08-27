// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

// Package benchmark loads anchored PR-review ground truth and scores agent
// findings without making model or network calls.
package benchmark

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Finding struct {
	ID          string  `json:"id,omitempty"`
	Title       string  `json:"title,omitempty"`
	Description string  `json:"description,omitempty"`
	Explanation string  `json:"explanation,omitempty"`
	Evidence    string  `json:"evidence,omitempty"`
	File        string  `json:"file,omitempty"`
	StartLine   int     `json:"start_line,omitempty"`
	EndLine     int     `json:"end_line,omitempty"`
	Severity    string  `json:"severity,omitempty"`
	Confidence  float64 `json:"confidence,omitempty"`
}

type Case struct {
	ID         string    `json:"id,omitempty"`
	Repository string    `json:"repository"`
	PRURL      string    `json:"pr_url"`
	BaseSHA    string    `json:"base_sha,omitempty"`
	HeadSHA    string    `json:"head_sha,omitempty"`
	Expected   []Finding `json:"expected"`
}

type Score struct {
	Expected           int            `json:"expected"`
	UnscorableExpected int            `json:"unscorable_expected"`
	Predicted          int            `json:"predicted"`
	Matched            int            `json:"matched"`
	UnresolvedPairs    int            `json:"unresolved_pairs"`
	Complete           bool           `json:"complete"`
	Precision          float64        `json:"precision"`
	Recall             float64        `json:"recall"`
	F1                 float64        `json:"f1"`
	Missed             []Finding      `json:"missed"`
	Extra              []Finding      `json:"extra"`
	Matches            []FindingMatch `json:"matches,omitempty"`
}

type qodoCase struct {
	Repository string `json:"repo"`
	PRURL      string `json:"pr_url_to_review"`
	Issues     []struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		File        string `json:"file_path"`
		StartLine   *int   `json:"start_line"`
		EndLine     *int   `json:"end_line"`
	} `json:"issues"`
}

// LoadQodoCase loads one PR from Qodo's JSONL benchmark metadata.
func LoadQodoCase(path, prURL string) (Case, error) {
	file, err := os.Open(path)
	if err != nil {
		return Case{}, fmt.Errorf("open benchmark dataset: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	buffer := make([]byte, 64*1024)
	scanner.Buffer(buffer, 4*1024*1024)
	for scanner.Scan() {
		var raw qodoCase
		if err := json.Unmarshal(scanner.Bytes(), &raw); err != nil {
			return Case{}, fmt.Errorf("decode benchmark case: %w", err)
		}
		if raw.PRURL != prURL {
			continue
		}
		result := Case{Repository: raw.Repository, PRURL: raw.PRURL, Expected: make([]Finding, 0, len(raw.Issues))}
		for _, issue := range raw.Issues {
			finding := Finding{Title: issue.Title, Description: issue.Description, File: cleanPath(issue.File)}
			if issue.StartLine != nil {
				finding.StartLine = *issue.StartLine
			}
			if issue.EndLine != nil {
				finding.EndLine = *issue.EndLine
			}
			result.Expected = append(result.Expected, finding)
		}
		return result, nil
	}
	if err := scanner.Err(); err != nil {
		return Case{}, fmt.Errorf("read benchmark dataset: %w", err)
	}
	return Case{}, fmt.Errorf("benchmark PR %q not found", prURL)
}

// LoadFindings accepts both ACR result JSON and the minimal findings JSON
// schema used to capture native-agent review output.
func LoadFindings(path string) ([]Finding, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open findings: %w", err)
	}
	defer file.Close()
	var document struct {
		Findings []Finding `json:"findings"`
	}
	decoder := json.NewDecoder(file)
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("decode findings: %w", err)
	}
	for i := range document.Findings {
		document.Findings[i].File = cleanPath(document.Findings[i].File)
		if document.Findings[i].EndLine == 0 {
			document.Findings[i].EndLine = document.Findings[i].StartLine
		}
	}
	return document.Findings, nil
}

func cleanPath(path string) string {
	path = strings.TrimSpace(filepath.ToSlash(path))
	return strings.TrimPrefix(path, "./")
}
