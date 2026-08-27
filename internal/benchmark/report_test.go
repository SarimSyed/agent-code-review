// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package benchmark

import (
	"fmt"
	"strings"
	"testing"

	"github.com/alibaba/open-code-review/internal/delegation"
)

func TestBuildReportSeparatesCommunicationPolicyAndHostUsage(t *testing.T) {
	run := &Run{
		ProtocolVersion: BenchmarkProtocolVersion,
		ID:              "run-usage",
		TokenEconomy:    delegation.TokenEconomy{Mode: delegation.TokenEconomyCaveman, Level: delegation.CavemanFull},
		Tasks: []Task{
			{ID: "baseline", Arm: ArmBaseline, Usage: &Usage{InputTokens: 100, OutputTokens: 20, TotalTokens: 120}},
			{ID: "acr", Arm: ArmACR, Usage: &Usage{InputTokens: 200, OutputTokens: 40, TotalTokens: 240}, ReviewAssurance: &delegation.ReviewAssurance{PhasesCompleted: 5, Candidates: 2, Dropped: 1, Overrides: 1, EvidenceFiles: 3, CriticMode: delegation.CriticIndependent}},
		},
	}
	report := BuildReport(run)
	if report.TokenEconomy != run.TokenEconomy {
		t.Fatalf("report policy = %#v", report.TokenEconomy)
	}
	if !report.BaselineUsage.Available || report.BaselineUsage.TotalTokens != 120 || !report.ACRUsage.Available || report.ACRUsage.TotalTokens != 240 {
		t.Fatalf("usage aggregates = baseline %#v, ACR %#v", report.BaselineUsage, report.ACRUsage)
	}
	if report.ACRProcess.Sessions != 1 || report.ACRProcess.PhaseCheckpoints != 5 || report.ACRProcess.Candidates != 2 || report.ACRProcess.CriticModes[delegation.CriticIndependent] != 1 {
		t.Fatalf("ACR process aggregate = %#v", report.ACRProcess)
	}
	markdown := RenderReportMarkdown(report)
	if !strings.Contains(markdown, "Caveman") || !strings.Contains(markdown, "240") || !strings.Contains(markdown, "host-reported") || !strings.Contains(markdown, "Phase checkpoints") {
		t.Fatalf("Markdown omits policy or usage:\n%s", markdown)
	}
}

func TestBuildReportMarksMissingTokenUsageUnavailable(t *testing.T) {
	report := BuildReport(&Run{ProtocolVersion: BenchmarkProtocolVersion, ID: "run-no-usage"})
	if report.BaselineUsage.Available || report.ACRUsage.Available {
		t.Fatalf("missing usage reported available: %#v", report)
	}
	if !strings.Contains(RenderReportMarkdown(report), "unavailable") {
		t.Fatalf("Markdown does not label missing usage")
	}
}

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
