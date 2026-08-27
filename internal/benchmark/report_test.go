// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package benchmark

import (
	"fmt"
	"strings"
	"testing"
)

func TestBuildReportRetainsPerCaseEvidenceAndBootstrapsTenPairs(t *testing.T) {
	run := &Run{ProtocolVersion: BenchmarkProtocolVersion, ID: "run-1", Seed: 7}
	for index := 0; index < 10; index++ {
		caseID := fmt.Sprintf("case-%02d", index)
		run.Evaluations = append(run.Evaluations,
			Evaluation{ID: "b-" + caseID, TaskID: "b-task-" + caseID, CaseID: caseID, Trial: 1, Arm: ArmBaseline, Score: Score{
				Expected: 1, Predicted: 1, Matched: 0, Complete: true, Missed: []Finding{{Title: "Seeded bug"}}, Extra: []Finding{{Title: "False alarm"}},
			}},
			Evaluation{ID: "a-" + caseID, TaskID: "a-task-" + caseID, CaseID: caseID, Trial: 1, Arm: ArmACR, Score: Score{
				Expected: 1, Predicted: 1, Matched: 1, Precision: 1, Recall: 1, F1: 1, Complete: true,
			}},
		)
		run.Tasks = append(run.Tasks,
			Task{ID: "b-task-" + caseID, CaseID: caseID, Trial: 1, Arm: ArmBaseline, State: TaskScored},
			Task{ID: "a-task-" + caseID, CaseID: caseID, Trial: 1, Arm: ArmACR, State: TaskScored},
		)
	}
	report := BuildReport(run)
	if report.Winner != ArmACR || report.Confidence95 == nil || len(report.Pairs) != 10 {
		t.Fatalf("unexpected aggregate report: %#v", report)
	}
	if len(report.Pairs[0].Baseline.Missed) != 1 || len(report.Pairs[0].Baseline.Extra) != 1 {
		t.Fatalf("per-case evidence was discarded: %#v", report.Pairs[0])
	}
	markdown := RenderReportMarkdown(report)
	if !strings.Contains(markdown, "## Per-case evidence") || !strings.Contains(markdown, "Seeded bug") || !strings.Contains(markdown, "False alarm") {
		t.Fatalf("Markdown omits review evidence:\n%s", markdown)
	}
}
