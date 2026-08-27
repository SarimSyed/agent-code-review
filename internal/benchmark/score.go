// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package benchmark

import (
	"fmt"
	"sort"
	"strings"
)

const (
	DecisionMatch   = "match"
	DecisionNoMatch = "no_match"
)

type FindingAnalysis struct {
	Expected        []Finding       `json:"expected"`
	Predicted       []Finding       `json:"predicted"`
	InvalidExpected []int           `json:"invalid_expected,omitempty"`
	AutoMatches     []FindingMatch  `json:"auto_matches"`
	PendingPairs    []CandidatePair `json:"pending_pairs"`
}

type FindingMatch struct {
	ExpectedIndex  int     `json:"expected_index"`
	PredictedIndex int     `json:"predicted_index"`
	Method         string  `json:"method"`
	Confidence     float64 `json:"confidence"`
}

type CandidatePair struct {
	ID             string  `json:"id"`
	ExpectedIndex  int     `json:"expected_index"`
	PredictedIndex int     `json:"predicted_index"`
	Similarity     float64 `json:"similarity"`
}

type semanticCandidate struct {
	expected   int
	predicted  int
	similarity float64
	strong     bool
}

// AnalyzeFindings performs only high-confidence deterministic matching and
// returns every semantically uncertain pair for blinded adjudication.
func AnalyzeFindings(expected, predicted []Finding) FindingAnalysis {
	analysis := FindingAnalysis{
		Expected: append([]Finding(nil), expected...), Predicted: append([]Finding(nil), predicted...),
		AutoMatches: make([]FindingMatch, 0), PendingPairs: make([]CandidatePair, 0),
	}
	for index := range analysis.Expected {
		normalizeFinding(&analysis.Expected[index])
	}
	for index := range analysis.Predicted {
		normalizeFinding(&analysis.Predicted[index])
	}
	candidates := make([]semanticCandidate, 0)
	strongExpected := map[int]int{}
	strongPredicted := map[int]int{}
	for expectedIndex, target := range analysis.Expected {
		if malformedExpected(target) {
			analysis.InvalidExpected = append(analysis.InvalidExpected, expectedIndex)
			continue
		}
		for predictedIndex, actual := range analysis.Predicted {
			if !validPredictedAnchor(actual) {
				continue
			}
			if anchored(target) && (target.File != actual.File || !rangesOverlap(target, actual)) {
				continue
			}
			similarity, strong := semanticSimilarity(target, actual)
			candidate := semanticCandidate{expected: expectedIndex, predicted: predictedIndex, similarity: similarity, strong: strong && anchored(target)}
			candidates = append(candidates, candidate)
			if candidate.strong {
				strongExpected[expectedIndex]++
				strongPredicted[predictedIndex]++
			}
		}
	}
	matchedExpected := map[int]bool{}
	matchedPredicted := map[int]bool{}
	for _, candidate := range candidates {
		if candidate.strong && strongExpected[candidate.expected] == 1 && strongPredicted[candidate.predicted] == 1 {
			analysis.AutoMatches = append(analysis.AutoMatches, FindingMatch{
				ExpectedIndex: candidate.expected, PredictedIndex: candidate.predicted,
				Method: "deterministic", Confidence: candidate.similarity,
			})
			matchedExpected[candidate.expected] = true
			matchedPredicted[candidate.predicted] = true
		}
	}
	pairNumber := 0
	for _, candidate := range candidates {
		if matchedExpected[candidate.expected] || matchedPredicted[candidate.predicted] {
			continue
		}
		pairNumber++
		analysis.PendingPairs = append(analysis.PendingPairs, CandidatePair{
			ID: fmt.Sprintf("pair-%04d", pairNumber), ExpectedIndex: candidate.expected,
			PredictedIndex: candidate.predicted, Similarity: candidate.similarity,
		})
	}
	return analysis
}

// ResolveAnalysis applies a two-vote majority, with a third vote serving as a
// tiebreaker, then chooses the strongest one-to-one set of approved pairs.
func ResolveAnalysis(analysis FindingAnalysis, judgeSubmissions [][]Judgment) Score {
	result := Score{
		Predicted: len(analysis.Predicted), Missed: make([]Finding, 0), Extra: make([]Finding, 0),
		Matches: append([]FindingMatch(nil), analysis.AutoMatches...),
	}
	matchedExpected := map[int]bool{}
	matchedPredicted := map[int]bool{}
	for _, match := range analysis.AutoMatches {
		matchedExpected[match.ExpectedIndex] = true
		matchedPredicted[match.PredictedIndex] = true
	}
	type approvedPair struct {
		pair       CandidatePair
		confidence float64
	}
	approved := make([]approvedPair, 0)
	unresolved := map[string]bool{}
	for _, pair := range analysis.PendingPairs {
		votes := make([]Judgment, 0, len(judgeSubmissions))
		for _, submission := range judgeSubmissions {
			for _, judgment := range submission {
				if judgment.PairID == pair.ID && (judgment.Decision == DecisionMatch || judgment.Decision == DecisionNoMatch) {
					votes = append(votes, judgment)
					break
				}
			}
		}
		matches, noMatches, confidence := 0, 0, 0.0
		for _, vote := range votes {
			if vote.Decision == DecisionMatch {
				matches++
			} else {
				noMatches++
			}
			confidence += vote.Confidence
		}
		if len(votes) > 0 {
			confidence /= float64(len(votes))
		}
		if matches >= 2 {
			approved = append(approved, approvedPair{pair: pair, confidence: confidence})
		} else if noMatches < 2 {
			unresolved[pair.ID] = true
		}
	}
	sort.SliceStable(approved, func(i, j int) bool {
		if approved[i].confidence != approved[j].confidence {
			return approved[i].confidence > approved[j].confidence
		}
		if approved[i].pair.Similarity != approved[j].pair.Similarity {
			return approved[i].pair.Similarity > approved[j].pair.Similarity
		}
		return approved[i].pair.ID < approved[j].pair.ID
	})
	for _, candidate := range approved {
		if matchedExpected[candidate.pair.ExpectedIndex] || matchedPredicted[candidate.pair.PredictedIndex] {
			continue
		}
		matchedExpected[candidate.pair.ExpectedIndex] = true
		matchedPredicted[candidate.pair.PredictedIndex] = true
		result.Matches = append(result.Matches, FindingMatch{
			ExpectedIndex: candidate.pair.ExpectedIndex, PredictedIndex: candidate.pair.PredictedIndex,
			Method: "adjudicated", Confidence: candidate.confidence,
		})
	}
	unresolvedExpected := map[int]bool{}
	for _, pair := range analysis.PendingPairs {
		if unresolved[pair.ID] && !matchedExpected[pair.ExpectedIndex] && !matchedPredicted[pair.PredictedIndex] {
			result.UnresolvedPairs++
			unresolvedExpected[pair.ExpectedIndex] = true
		}
	}
	invalidExpected := map[int]bool{}
	for _, index := range analysis.InvalidExpected {
		invalidExpected[index] = true
	}
	for index, finding := range analysis.Expected {
		if invalidExpected[index] || unresolvedExpected[index] {
			result.UnscorableExpected++
			continue
		}
		result.Expected++
		if !matchedExpected[index] {
			result.Missed = append(result.Missed, finding)
		}
	}
	for index, finding := range analysis.Predicted {
		if !matchedPredicted[index] {
			result.Extra = append(result.Extra, finding)
		}
	}
	result.Matched = len(result.Matches)
	if result.Predicted > 0 {
		result.Precision = float64(result.Matched) / float64(result.Predicted)
	}
	if result.Expected > 0 {
		result.Recall = float64(result.Matched) / float64(result.Expected)
	}
	if result.Precision+result.Recall > 0 {
		result.F1 = 2 * result.Precision * result.Recall / (result.Precision + result.Recall)
	}
	result.Complete = result.UnresolvedPairs == 0 && len(analysis.InvalidExpected) == 0
	return result
}

// ScoreFindings preserves the original deterministic command while refusing
// to guess at ambiguous semantic pairs.
func ScoreFindings(expected, predicted []Finding) Score {
	return ResolveAnalysis(AnalyzeFindings(expected, predicted), nil)
}

func normalizeFinding(finding *Finding) {
	finding.File = cleanPath(finding.File)
	if finding.EndLine == 0 && finding.StartLine > 0 {
		finding.EndLine = finding.StartLine
	}
}

func anchored(finding Finding) bool {
	return finding.File != "" && finding.StartLine > 0 && finding.EndLine >= finding.StartLine
}

func malformedExpected(finding Finding) bool {
	if finding.File == "" && finding.StartLine == 0 && finding.EndLine == 0 {
		return false
	}
	return !anchored(finding)
}

func validPredictedAnchor(finding Finding) bool {
	return anchored(finding)
}

func rangesOverlap(left, right Finding) bool {
	return left.StartLine <= right.EndLine && right.StartLine <= left.EndLine
}

func semanticSimilarity(expected, predicted Finding) (float64, bool) {
	expectedText := expected.Title + " " + expected.Description
	predictedText := predicted.Title + " " + predicted.Description + " " + predicted.Explanation + " " + predicted.Evidence
	expectedTokens := reviewTokens(expectedText)
	predictedTokens := reviewTokens(predictedText)
	shared := 0
	for token := range expectedTokens {
		if predictedTokens[token] {
			shared++
		}
	}
	union := len(expectedTokens) + len(predictedTokens) - shared
	similarity := 0.0
	if union > 0 {
		similarity = float64(shared) / float64(union)
	}
	normalizedTitle := normalizePhrase(expected.Title)
	titleContained := len(normalizedTitle) >= 5 && strings.Contains(normalizePhrase(predictedText), normalizedTitle)
	return similarity, titleContained || (shared >= 2 && similarity >= 0.35)
}

func reviewTokens(text string) map[string]bool {
	stop := map[string]bool{
		"and": true, "are": true, "but": true, "for": true, "from": true,
		"has": true, "have": true, "its": true, "not": true, "the": true,
		"this": true, "that": true, "with": true, "into": true, "when": true,
	}
	tokens := map[string]bool{}
	for _, token := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	}) {
		if strings.HasSuffix(token, "ed") && len(token) > 5 {
			token = strings.TrimSuffix(token, "ed")
		}
		if len(token) >= 3 && !stop[token] {
			tokens[token] = true
		}
	}
	return tokens
}

func normalizePhrase(text string) string {
	return strings.Join(strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	}), " ")
}
