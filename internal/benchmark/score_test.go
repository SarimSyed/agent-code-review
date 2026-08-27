// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package benchmark

import "testing"

func TestScoreMatchesAnchoredFindingsOneToOne(t *testing.T) {
	expected := []Finding{
		{Title: "branch inverted", File: "src/service.ts", StartLine: 10, EndLine: 10},
		{Title: "missing await", File: "src/service.ts", StartLine: 20, EndLine: 21},
		{Title: "unanchored", File: "src/ignored.ts"},
	}
	predicted := []Finding{
		{File: "src/service.ts", StartLine: 10, EndLine: 11, Explanation: "The branch is inverted."},
		{File: "src/service.ts", StartLine: 20, EndLine: 20, Explanation: "Missing await lets the promise escape."},
		{File: "src/other.ts", StartLine: 2, EndLine: 2, Explanation: "False positive."},
	}

	score := ScoreFindings(expected, predicted)
	if score.Expected != 2 || score.UnscorableExpected != 1 || score.Predicted != 3 || score.Matched != 2 {
		t.Fatalf("unexpected score counts: %#v", score)
	}
	if score.Precision != 2.0/3.0 || score.Recall != 1 || score.F1 != 0.8 {
		t.Fatalf("unexpected metrics: %#v", score)
	}
	if len(score.Missed) != 0 || len(score.Extra) != 1 || score.Extra[0].File != "src/other.ts" {
		t.Fatalf("unexpected match details: %#v", score)
	}
}

func TestAnalyzeDoesNotMatchWrongBugAtTheCorrectLine(t *testing.T) {
	expected := []Finding{{Title: "Authorization bypass", Description: "The role check accepts guests.", File: "auth.go", StartLine: 20, EndLine: 20}}
	predicted := []Finding{{Title: "Memory leak", Explanation: "The buffer is never released.", File: "auth.go", StartLine: 20, EndLine: 20}}

	analysis := AnalyzeFindings(expected, predicted)
	if len(analysis.AutoMatches) != 0 || len(analysis.PendingPairs) != 1 {
		t.Fatalf("wrong defect was automatically matched: %#v", analysis)
	}
	score := ResolveAnalysis(analysis, [][]Judgment{
		{{PairID: analysis.PendingPairs[0].ID, Decision: DecisionNoMatch, Confidence: 0.99}},
		{{PairID: analysis.PendingPairs[0].ID, Decision: DecisionNoMatch, Confidence: 0.98}},
	})
	if score.Matched != 0 || len(score.Missed) != 1 || len(score.Extra) != 1 {
		t.Fatalf("wrong defect should be missed plus extra: %#v", score)
	}
}

func TestAnalyzeAutoMatchesOnlyMutuallyUniqueStrongSemanticPairs(t *testing.T) {
	expected := []Finding{{Title: "Missing await", Description: "The promise escapes before completion.", File: "job.ts", StartLine: 8, EndLine: 8}}
	predicted := []Finding{{Title: "Missing await", Explanation: "The promise escapes before completion.", File: "job.ts", StartLine: 8, EndLine: 8}}

	analysis := AnalyzeFindings(expected, predicted)
	if len(analysis.AutoMatches) != 1 || len(analysis.PendingPairs) != 0 {
		t.Fatalf("strong unique pair was not auto-matched: %#v", analysis)
	}
}

func TestAnalyzeSendsUnanchoredGroundTruthToAdjudication(t *testing.T) {
	expected := []Finding{{Title: "Lost cleanup", Description: "The listener remains registered."}}
	predicted := []Finding{{Title: "Listener leak", Explanation: "The listener remains registered after shutdown.", File: "service.go", StartLine: 40, EndLine: 40}}

	analysis := AnalyzeFindings(expected, predicted)
	if len(analysis.PendingPairs) != 1 || analysis.PendingPairs[0].ExpectedIndex != 0 {
		t.Fatalf("unanchored issue was not queued for adjudication: %#v", analysis)
	}
}

func TestResolveAnalysisRequiresTwoVotesAndUsesTiebreaker(t *testing.T) {
	analysis := AnalyzeFindings(
		[]Finding{{Title: "Lost cleanup", File: "service.go", StartLine: 40, EndLine: 40}},
		[]Finding{{Title: "Listener leak", Explanation: "Shutdown leaves the listener registered.", File: "service.go", StartLine: 40, EndLine: 40}},
	)
	if len(analysis.PendingPairs) != 1 {
		t.Fatalf("expected one ambiguous pair: %#v", analysis)
	}
	pairID := analysis.PendingPairs[0].ID
	disagreement := ResolveAnalysis(analysis, [][]Judgment{
		{{PairID: pairID, Decision: DecisionMatch, Rationale: "same lifecycle defect", Confidence: 0.9}},
		{{PairID: pairID, Decision: DecisionNoMatch, Rationale: "different symptom", Confidence: 0.8}},
	})
	if disagreement.UnresolvedPairs != 1 || disagreement.Complete {
		t.Fatalf("disagreement should require a tiebreaker: %#v", disagreement)
	}
	resolved := ResolveAnalysis(analysis, [][]Judgment{
		{{PairID: pairID, Decision: DecisionMatch, Confidence: 0.9}},
		{{PairID: pairID, Decision: DecisionNoMatch, Confidence: 0.8}},
		{{PairID: pairID, Decision: DecisionMatch, Confidence: 0.95}},
	})
	if !resolved.Complete || resolved.Matched != 1 {
		t.Fatalf("tiebreaker did not resolve the pair: %#v", resolved)
	}
}

func TestResolveAnalysisEnforcesOneToOneMatches(t *testing.T) {
	analysis := FindingAnalysis{
		Expected:  []Finding{{Title: "first"}, {Title: "second"}},
		Predicted: []Finding{{Title: "one report"}},
		PendingPairs: []CandidatePair{
			{ID: "pair-a", ExpectedIndex: 0, PredictedIndex: 0, Similarity: 0.7},
			{ID: "pair-b", ExpectedIndex: 1, PredictedIndex: 0, Similarity: 0.6},
		},
	}
	judges := [][]Judgment{
		{{PairID: "pair-a", Decision: DecisionMatch, Confidence: 0.9}, {PairID: "pair-b", Decision: DecisionMatch, Confidence: 0.8}},
		{{PairID: "pair-a", Decision: DecisionMatch, Confidence: 0.9}, {PairID: "pair-b", Decision: DecisionMatch, Confidence: 0.8}},
	}
	score := ResolveAnalysis(analysis, judges)
	if score.Matched != 1 || len(score.Missed) != 1 || len(score.Extra) != 0 {
		t.Fatalf("one prediction matched more than once: %#v", score)
	}
}

func TestScorePrefersSemanticMatchWhenExpectedRangesOverlap(t *testing.T) {
	expected := []Finding{
		{Title: "Slack listener removed", Description: "Slack notification events are no longer registered.", File: "boot.js", StartLine: 351, EndLine: 377},
		{Title: "Scheduling API URL missing", Description: "The scheduler needs an API URL.", File: "boot.js", StartLine: 368, EndLine: 368},
	}
	predicted := []Finding{
		{File: "boot.js", StartLine: 368, EndLine: 368, Explanation: "Scheduling initialization has no API URL."},
		{File: "boot.js", StartLine: 352, EndLine: 352, Explanation: "The Slack listener is no longer registered."},
	}

	score := ScoreFindings(expected, predicted)
	if score.Matched != 2 || len(score.Missed) != 0 || len(score.Extra) != 0 {
		t.Fatalf("overlapping ranges should match their corresponding defects: %#v", score)
	}
}
