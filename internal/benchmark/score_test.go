// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package benchmark

import "testing"

func TestScoreMatchesAnchoredFindingsOneToOne(t *testing.T) {
	expected := []Finding{
		{Title: "wrong condition", File: "src/service.ts", StartLine: 10, EndLine: 10},
		{Title: "missing await", File: "src/service.ts", StartLine: 20, EndLine: 21},
		{Title: "unanchored", File: "src/ignored.ts"},
	}
	predicted := []Finding{
		{File: "src/service.ts", StartLine: 10, EndLine: 11, Explanation: "The branch is inverted."},
		{File: "src/service.ts", StartLine: 20, EndLine: 20, Explanation: "The promise is not awaited."},
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
