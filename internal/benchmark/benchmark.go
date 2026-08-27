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
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	Explanation string `json:"explanation,omitempty"`
	File        string `json:"file,omitempty"`
	StartLine   int    `json:"start_line,omitempty"`
	EndLine     int    `json:"end_line,omitempty"`
}

type Case struct {
	Repository string    `json:"repository"`
	PRURL      string    `json:"pr_url"`
	Expected   []Finding `json:"expected"`
}

type Score struct {
	Expected           int       `json:"expected"`
	UnscorableExpected int       `json:"unscorable_expected"`
	Predicted          int       `json:"predicted"`
	Matched            int       `json:"matched"`
	Precision          float64   `json:"precision"`
	Recall             float64   `json:"recall"`
	F1                 float64   `json:"f1"`
	Missed             []Finding `json:"missed"`
	Extra              []Finding `json:"extra"`
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

// ScoreFindings matches each predicted finding to at most one ground-truth
// issue with the same file and an overlapping positive line range.
func ScoreFindings(expected, predicted []Finding) Score {
	result := Score{Missed: make([]Finding, 0), Extra: make([]Finding, 0), Predicted: len(predicted)}
	eligible := make([]Finding, 0, len(expected))
	for _, finding := range expected {
		finding.File = cleanPath(finding.File)
		if finding.StartLine < 1 || finding.EndLine < finding.StartLine || finding.File == "" {
			result.UnscorableExpected++
			continue
		}
		eligible = append(eligible, finding)
	}
	result.Expected = len(eligible)
	matched := make([]bool, len(eligible))
	for _, finding := range predicted {
		finding.File = cleanPath(finding.File)
		if finding.EndLine == 0 {
			finding.EndLine = finding.StartLine
		}
		match := -1
		bestScore := -1 << 30
		for i, target := range eligible {
			if matched[i] || target.File != finding.File || finding.StartLine < 1 || finding.EndLine < finding.StartLine {
				continue
			}
			if finding.StartLine <= target.EndLine && target.StartLine <= finding.EndLine {
				score := findingMatchScore(finding, target)
				if score > bestScore {
					match, bestScore = i, score
				}
			}
		}
		if match < 0 {
			result.Extra = append(result.Extra, finding)
			continue
		}
		matched[match] = true
		result.Matched++
	}
	for i, target := range eligible {
		if !matched[i] {
			result.Missed = append(result.Missed, target)
		}
	}
	if result.Predicted > 0 {
		result.Precision = float64(result.Matched) / float64(result.Predicted)
	}
	if result.Expected > 0 {
		result.Recall = float64(result.Matched) / float64(result.Expected)
	}
	if result.Precision+result.Recall > 0 {
		result.F1 = 2 * result.Precision * result.Recall / (result.Precision + result.Recall)
	}
	return result
}

// findingMatchScore resolves overlapping ground-truth line ranges. Textual
// overlap is the primary signal; a narrower annotated range wins only when
// the two findings carry the same textual evidence.
func findingMatchScore(predicted, expected Finding) int {
	shared := sharedTokens(
		predicted.Title+" "+predicted.Description+" "+predicted.Explanation,
		expected.Title+" "+expected.Description,
	)
	width := expected.EndLine - expected.StartLine
	if width < 0 {
		width = 0
	}
	return shared*10000 - width
}

func sharedTokens(left, right string) int {
	leftTokens := reviewTokens(left)
	rightTokens := reviewTokens(right)
	shared := 0
	for token := range leftTokens {
		if rightTokens[token] {
			shared++
		}
	}
	return shared
}

func reviewTokens(text string) map[string]bool {
	stop := map[string]bool{
		"and": true, "are": true, "but": true, "for": true, "from": true,
		"has": true, "have": true, "its": true, "not": true, "the": true,
		"this": true, "that": true, "with": true,
	}
	returnTokens := map[string]bool{}
	for _, token := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	}) {
		if len(token) >= 3 && !stop[token] {
			returnTokens[token] = true
		}
	}
	return returnTokens
}

func cleanPath(path string) string {
	path = strings.TrimSpace(filepath.ToSlash(path))
	return strings.TrimPrefix(path, "./")
}
