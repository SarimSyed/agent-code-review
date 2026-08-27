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

	"github.com/alibaba/open-code-review/internal/delegation"
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

type UsageAggregate struct {
	Available    bool `json:"available"`
	Tasks        int  `json:"tasks"`
	InputTokens  int  `json:"input_tokens"`
	OutputTokens int  `json:"output_tokens"`
	TotalTokens  int  `json:"total_tokens"`
}

type CommunicationAggregate struct {
	Backends map[string]int `json:"backends,omitempty"`
}

type ProcessAggregate struct {
	Sessions         int            `json:"sessions"`
	PhaseCheckpoints int            `json:"phase_checkpoints"`
	Candidates       int            `json:"candidates"`
	Dropped          int            `json:"dropped"`
	Overrides        int            `json:"overrides"`
	EvidenceFiles    int            `json:"evidence_files"`
	CriticModes      map[string]int `json:"critic_modes"`
}

type Report struct {
	ProtocolVersion       string                  `json:"protocol_version"`
	RunID                 string                  `json:"run_id"`
	Complete              bool                    `json:"complete"`
	Winner                string                  `json:"winner"`
	Baseline              AggregateScore          `json:"baseline"`
	ACR                   AggregateScore          `json:"acr"`
	F1Delta               float64                 `json:"f1_delta"`
	Wins                  int                     `json:"acr_wins"`
	Ties                  int                     `json:"ties"`
	Losses                int                     `json:"acr_losses"`
	Coverage              float64                 `json:"coverage"`
	Pairs                 []PairOutcome           `json:"pairs"`
	Confidence95          *ConfidenceInterval     `json:"confidence_95,omitempty"`
	SetupFailures         []SetupFailure          `json:"setup_failures,omitempty"`
	ValidationRejections  int                     `json:"validation_rejections"`
	TokenEconomy          delegation.TokenEconomy `json:"token_economy"`
	BaselineCommunication CommunicationAggregate  `json:"baseline_communication"`
	ACRCommunication      CommunicationAggregate  `json:"acr_communication"`
	BaselineUsage         UsageAggregate          `json:"baseline_usage"`
	ACRUsage              UsageAggregate          `json:"acr_usage"`
	ACRProcess            ProcessAggregate        `json:"acr_process"`
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
	policy := run.TokenEconomy
	if policy.Mode == "" {
		policy.Mode = delegation.TokenEconomyNormal
	}
	report := Report{
		ProtocolVersion: BenchmarkProtocolVersion, RunID: run.ID,
		SetupFailures: append([]SetupFailure(nil), run.SetupFailures...), TokenEconomy: policy,
		BaselineCommunication: CommunicationAggregate{Backends: map[string]int{}},
		ACRCommunication:      CommunicationAggregate{Backends: map[string]int{}},
		ACRProcess:            ProcessAggregate{CriticModes: map[string]int{}},
	}
	for _, task := range run.Tasks {
		report.ValidationRejections += len(task.Rejections)
		switch task.Arm {
		case ArmBaseline:
			addUsage(&report.BaselineUsage, task.Usage)
			addCommunication(&report.BaselineCommunication, task.Communication)
		case ArmACR:
			addUsage(&report.ACRUsage, task.Usage)
			addCommunication(&report.ACRCommunication, task.Communication)
			addProcess(&report.ACRProcess, task.ReviewAssurance)
		}
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

func addUsage(aggregate *UsageAggregate, usage *Usage) {
	if usage == nil {
		return
	}
	aggregate.Available = true
	aggregate.Tasks++
	aggregate.InputTokens += usage.InputTokens
	aggregate.OutputTokens += usage.OutputTokens
	aggregate.TotalTokens += usage.TotalTokens
}

func addCommunication(aggregate *CommunicationAggregate, communication *delegation.Communication) {
	if communication == nil || communication.Backend == "" {
		return
	}
	aggregate.Backends[communication.Backend]++
}

func addProcess(aggregate *ProcessAggregate, assurance *delegation.ReviewAssurance) {
	if assurance == nil {
		return
	}
	aggregate.Sessions++
	aggregate.PhaseCheckpoints += assurance.PhasesCompleted
	aggregate.Candidates += assurance.Candidates
	aggregate.Dropped += assurance.Dropped
	aggregate.Overrides += assurance.Overrides
	aggregate.EvidenceFiles += assurance.EvidenceFiles
	if assurance.CriticMode != "" {
		aggregate.CriticModes[assurance.CriticMode]++
	}
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
	if report.TokenEconomy.Mode == delegation.TokenEconomyCaveman {
		fmt.Fprintf(&builder, "Communication: Caveman `%s`, applied equally to baseline and ACR. Actual backends: baseline %s; ACR %s.\n\n", report.TokenEconomy.Level, backendLabel(report.BaselineCommunication), backendLabel(report.ACRCommunication))
	} else {
		builder.WriteString("Communication: normal, applied equally to baseline and ACR.\n\n")
	}
	builder.WriteString("| Reviewer | Precision | Recall | F1 | Macro F1 | Matched | Expected | Predicted |\n")
	builder.WriteString("|---|---:|---:|---:|---:|---:|---:|---:|\n")
	fmt.Fprintf(&builder, "| Baseline | %.3f | %.3f | %.3f | %.3f | %d | %d | %d |\n", report.Baseline.Precision, report.Baseline.Recall, report.Baseline.F1, report.Baseline.MacroF1, report.Baseline.Matched, report.Baseline.Expected, report.Baseline.Predicted)
	fmt.Fprintf(&builder, "| ACR | %.3f | %.3f | %.3f | %.3f | %d | %d | %d |\n\n", report.ACR.Precision, report.ACR.Recall, report.ACR.F1, report.ACR.MacroF1, report.ACR.Matched, report.ACR.Expected, report.ACR.Predicted)
	fmt.Fprintf(&builder, "Paired outcomes: %d ACR wins, %d ties, %d losses.\n", report.Wins, report.Ties, report.Losses)
	fmt.Fprintf(&builder, "Validation rejections: %d.\n", report.ValidationRejections)
	builder.WriteString("\n## Host-reported token usage\n\n")
	builder.WriteString("Only exact host-reported counts are shown.\n\n")
	builder.WriteString("| Reviewer | Tasks reporting | Input | Output | Total |\n")
	builder.WriteString("|---|---:|---:|---:|---:|\n")
	fmt.Fprintf(&builder, "| Baseline | %s |\n", usageLabel(report.BaselineUsage))
	fmt.Fprintf(&builder, "| ACR | %s |\n", usageLabel(report.ACRUsage))
	if !report.BaselineUsage.Available || !report.ACRUsage.Available {
		builder.WriteString("\nMissing host-reported usage is unavailable; ACR does not estimate tokens.\n")
	}
	builder.WriteString("\n## ACR process metrics\n\n")
	fmt.Fprintf(&builder, "- Sessions: %d\n", report.ACRProcess.Sessions)
	fmt.Fprintf(&builder, "- Phase checkpoints: %d\n", report.ACRProcess.PhaseCheckpoints)
	fmt.Fprintf(&builder, "- Candidates: %d; dropped: %d; overrides: %d.\n", report.ACRProcess.Candidates, report.ACRProcess.Dropped, report.ACRProcess.Overrides)
	fmt.Fprintf(&builder, "- Frozen evidence files: %d.\n", report.ACRProcess.EvidenceFiles)
	fmt.Fprintf(&builder, "- Critic modes: %s.\n", countMapLabel(report.ACRProcess.CriticModes))
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

func backendLabel(aggregate CommunicationAggregate) string {
	if len(aggregate.Backends) == 0 {
		return "unavailable"
	}
	keys := make([]string, 0, len(aggregate.Backends))
	for backend := range aggregate.Backends {
		keys = append(keys, backend)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, backend := range keys {
		parts = append(parts, fmt.Sprintf("`%s` (%d)", backend, aggregate.Backends[backend]))
	}
	return strings.Join(parts, ", ")
}

func countMapLabel(values map[string]int) string {
	if len(values) == 0 {
		return "unavailable"
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s (%d)", strings.ReplaceAll(key, "_", " "), values[key]))
	}
	return strings.Join(parts, ", ")
}

func usageLabel(usage UsageAggregate) string {
	if !usage.Available {
		return "unavailable | unavailable | unavailable | unavailable"
	}
	return fmt.Sprintf("%d | %d | %d | %d", usage.Tasks, usage.InputTokens, usage.OutputTokens, usage.TotalTokens)
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
