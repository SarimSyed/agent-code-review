// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package benchmark

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type AggregateScore struct {
	Expected  int     `json:"expected"`
	Predicted int     `json:"predicted"`
	Matched   int     `json:"matched"`
	Precision float64 `json:"precision"`
	Recall    float64 `json:"recall"`
	F1        float64 `json:"f1"`
	MacroF1   float64 `json:"macro_f1"`
}

type PairOutcome struct {
	CaseID            string     `json:"case_id"`
	Trial             int        `json:"trial"`
	BaselineF1        float64    `json:"baseline_f1"`
	ACRF1             float64    `json:"acr_f1"`
	Delta             float64    `json:"delta"`
	Baseline          Score      `json:"baseline"`
	ACR               Score      `json:"acr"`
	BaselineJudgments []Judgment `json:"baseline_judgments,omitempty"`
	ACRJudgments      []Judgment `json:"acr_judgments,omitempty"`
}

type ConfidenceInterval struct {
	Low  float64 `json:"low"`
	High float64 `json:"high"`
}

type Report struct {
	ProtocolVersion      string              `json:"protocol_version"`
	RunID                string              `json:"run_id"`
	Complete             bool                `json:"complete"`
	Winner               string              `json:"winner"`
	Baseline             AggregateScore      `json:"baseline"`
	ACR                  AggregateScore      `json:"acr"`
	F1Delta              float64             `json:"f1_delta"`
	Wins                 int                 `json:"acr_wins"`
	Ties                 int                 `json:"ties"`
	Losses               int                 `json:"acr_losses"`
	Coverage             float64             `json:"coverage"`
	Pairs                []PairOutcome       `json:"pairs"`
	Confidence95         *ConfidenceInterval `json:"confidence_95,omitempty"`
	SetupFailures        []SetupFailure      `json:"setup_failures,omitempty"`
	ValidationRejections int                 `json:"validation_rejections"`
}

func GenerateReport(workspace string, run *Run) (Report, error) {
	report := BuildReport(run)
	runRoot := RunDir(workspace, run.ID)
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return Report{}, fmt.Errorf("encode benchmark report: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(filepath.Join(runRoot, "report.json"), data, 0o600); err != nil {
		return Report{}, fmt.Errorf("write benchmark JSON report: %w", err)
	}
	if err := os.WriteFile(filepath.Join(runRoot, "report.md"), []byte(RenderReportMarkdown(report)), 0o600); err != nil {
		return Report{}, fmt.Errorf("write benchmark Markdown report: %w", err)
	}
	return report, nil
}

func BuildReport(run *Run) Report {
	report := Report{ProtocolVersion: BenchmarkProtocolVersion, RunID: run.ID, SetupFailures: append([]SetupFailure(nil), run.SetupFailures...)}
	for _, task := range run.Tasks {
		report.ValidationRejections += len(task.Rejections)
	}
	baselineEvaluations := evaluationsByArm(run.Evaluations, ArmBaseline)
	acrEvaluations := evaluationsByArm(run.Evaluations, ArmACR)
	report.Baseline = aggregateEvaluations(baselineEvaluations)
	report.ACR = aggregateEvaluations(acrEvaluations)
	report.F1Delta = report.ACR.F1 - report.Baseline.F1
	byKey := map[string]map[string]Evaluation{}
	for _, evaluation := range run.Evaluations {
		key := fmt.Sprintf("%s:%d", evaluation.CaseID, evaluation.Trial)
		if byKey[key] == nil {
			byKey[key] = map[string]Evaluation{}
		}
		byKey[key][evaluation.Arm] = evaluation
	}
	completeEvaluations := 0
	for _, evaluation := range run.Evaluations {
		if evaluation.Score.Complete {
			completeEvaluations++
		}
	}
	if len(run.Evaluations) > 0 {
		report.Coverage = float64(completeEvaluations) / float64(len(run.Evaluations))
	}
	for _, arms := range byKey {
		baseline, baselineOK := arms[ArmBaseline]
		acr, acrOK := arms[ArmACR]
		if !baselineOK || !acrOK || !baseline.Score.Complete || !acr.Score.Complete {
			continue
		}
		outcome := PairOutcome{
			CaseID: acr.CaseID, Trial: acr.Trial, BaselineF1: baseline.Score.F1,
			ACRF1: acr.Score.F1, Delta: acr.Score.F1 - baseline.Score.F1,
			Baseline: baseline.Score, ACR: acr.Score,
			BaselineJudgments: append([]Judgment(nil), baseline.Judgments...),
			ACRJudgments:      append([]Judgment(nil), acr.Judgments...),
		}
		report.Pairs = append(report.Pairs, outcome)
		if outcome.Delta > 0 {
			report.Wins++
		} else if outcome.Delta < 0 {
			report.Losses++
		} else {
			report.Ties++
		}
	}
	sort.Slice(report.Pairs, func(i, j int) bool {
		if report.Pairs[i].CaseID != report.Pairs[j].CaseID {
			return report.Pairs[i].CaseID < report.Pairs[j].CaseID
		}
		return report.Pairs[i].Trial < report.Pairs[j].Trial
	})
	report.Complete = len(run.SetupFailures) == 0 && len(run.Evaluations) > 0 && report.Coverage == 1 && len(report.Pairs)*2 == len(run.Evaluations)
	if !report.Complete {
		report.Winner = "incomplete"
	} else if report.F1Delta > 0 {
		report.Winner = ArmACR
	} else if report.F1Delta < 0 {
		report.Winner = ArmBaseline
	} else {
		report.Winner = "tie"
	}
	if len(report.Pairs) >= 10 {
		interval := bootstrapInterval(report.Pairs, run.Seed)
		report.Confidence95 = &interval
	}
	return report
}

func aggregateEvaluations(evaluations []Evaluation) AggregateScore {
	result := AggregateScore{}
	for _, evaluation := range evaluations {
		result.Expected += evaluation.Score.Expected
		result.Predicted += evaluation.Score.Predicted
		result.Matched += evaluation.Score.Matched
		result.MacroF1 += evaluation.Score.F1
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
	if len(evaluations) > 0 {
		result.MacroF1 /= float64(len(evaluations))
	}
	return result
}

func evaluationsByArm(evaluations []Evaluation, arm string) []Evaluation {
	result := make([]Evaluation, 0)
	for _, evaluation := range evaluations {
		if evaluation.Arm == arm {
			result = append(result, evaluation)
		}
	}
	return result
}

func bootstrapInterval(pairs []PairOutcome, seed int64) ConfidenceInterval {
	const samples = 2000
	random := rand.New(rand.NewSource(seed))
	means := make([]float64, samples)
	for sample := range means {
		total := 0.0
		for range pairs {
			total += pairs[random.Intn(len(pairs))].Delta
		}
		means[sample] = total / float64(len(pairs))
	}
	sort.Float64s(means)
	return ConfidenceInterval{Low: means[int(0.025*samples)], High: means[int(0.975*samples)-1]}
}

func RenderReportMarkdown(report Report) string {
	resultLabel := map[string]string{ArmACR: "ACR wins", ArmBaseline: "Baseline wins", "tie": "Tie", "incomplete": "Incomplete"}[report.Winner]
	var builder strings.Builder
	fmt.Fprintf(&builder, "# ACR Benchmark Report\n\n**Result: %s** · F1 delta: %+.3f · Coverage: %.1f%%\n\n", resultLabel, report.F1Delta, report.Coverage*100)
	builder.WriteString("| Reviewer | Precision | Recall | F1 | Macro F1 | Matched | Expected | Predicted |\n")
	builder.WriteString("|---|---:|---:|---:|---:|---:|---:|---:|\n")
	fmt.Fprintf(&builder, "| Baseline | %.3f | %.3f | %.3f | %.3f | %d | %d | %d |\n", report.Baseline.Precision, report.Baseline.Recall, report.Baseline.F1, report.Baseline.MacroF1, report.Baseline.Matched, report.Baseline.Expected, report.Baseline.Predicted)
	fmt.Fprintf(&builder, "| ACR | %.3f | %.3f | %.3f | %.3f | %d | %d | %d |\n\n", report.ACR.Precision, report.ACR.Recall, report.ACR.F1, report.ACR.MacroF1, report.ACR.Matched, report.ACR.Expected, report.ACR.Predicted)
	fmt.Fprintf(&builder, "Paired outcomes: %d ACR wins, %d ties, %d losses.\n", report.Wins, report.Ties, report.Losses)
	fmt.Fprintf(&builder, "Validation rejections: %d.\n", report.ValidationRejections)
	if report.Confidence95 != nil {
		fmt.Fprintf(&builder, "\nBootstrap 95%% confidence interval for paired F1 delta: [%.3f, %.3f].\n", report.Confidence95.Low, report.Confidence95.High)
	} else {
		builder.WriteString("\nFewer than 10 complete pairs; results are descriptive and no significance claim is made.\n")
	}
	if len(report.SetupFailures) > 0 {
		builder.WriteString("\n## Setup failures\n\n")
		for _, failure := range report.SetupFailures {
			fmt.Fprintf(&builder, "- `%s`: %s\n", failure.CaseID, failure.Message)
		}
	}
	if len(report.Pairs) > 0 {
		builder.WriteString("\n## Per-case evidence\n")
		for _, pair := range report.Pairs {
			fmt.Fprintf(&builder, "\n### `%s` trial %d\n\nBaseline F1 %.3f; ACR F1 %.3f; delta %+.3f.\n", pair.CaseID, pair.Trial, pair.BaselineF1, pair.ACRF1, pair.Delta)
			renderScoreEvidence(&builder, "Baseline", pair.Baseline)
			renderScoreEvidence(&builder, "ACR", pair.ACR)
			renderJudgments(&builder, pair.BaselineJudgments)
			renderJudgments(&builder, pair.ACRJudgments)
		}
	}
	return builder.String()
}

func renderJudgments(builder *strings.Builder, judgments []Judgment) {
	for _, judgment := range judgments {
		fmt.Fprintf(builder, "\n- Adjudication `%s`: %s (%.2f) — %s\n", judgment.PairID, judgment.Decision, judgment.Confidence, judgment.Rationale)
	}
}

func renderScoreEvidence(builder *strings.Builder, label string, score Score) {
	if len(score.Missed) == 0 && len(score.Extra) == 0 {
		return
	}
	fmt.Fprintf(builder, "\n%s:\n", label)
	for _, finding := range score.Missed {
		fmt.Fprintf(builder, "\n- Missed: %s\n", findingLabel(finding))
	}
	for _, finding := range score.Extra {
		fmt.Fprintf(builder, "\n- Extra: %s\n", findingLabel(finding))
	}
}

func findingLabel(finding Finding) string {
	label := strings.TrimSpace(finding.Title)
	if label == "" {
		label = strings.TrimSpace(finding.Explanation)
	}
	if finding.File != "" && finding.StartLine > 0 {
		return fmt.Sprintf("`%s:%d` — %s", finding.File, finding.StartLine, label)
	}
	return label
}
