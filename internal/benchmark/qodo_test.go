// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package benchmark

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadQodoCaseAndFindings(t *testing.T) {
	dir := t.TempDir()
	dataset := filepath.Join(dir, "qodo.jsonl")
	if err := os.WriteFile(dataset, []byte("{\"repo\":\"example\",\"pr_url_to_review\":\"https://example.test/pr/1\",\"issues\":[{\"title\":\"bad branch\",\"description\":\"logic inverted\",\"file_path\":\"src/a.go\",\"start_line\":4,\"end_line\":4}]}\n"), 0o600); err != nil {
		t.Fatalf("write dataset: %v", err)
	}
	findings := filepath.Join(dir, "findings.json")
	if err := os.WriteFile(findings, []byte("{\"findings\":[{\"file\":\"src/a.go\",\"start_line\":4,\"end_line\":4,\"explanation\":\"branch is inverted\"}]}"), 0o600); err != nil {
		t.Fatalf("write findings: %v", err)
	}

	caseData, err := LoadQodoCase(dataset, "https://example.test/pr/1")
	if err != nil || caseData.Repository != "example" || len(caseData.Expected) != 1 {
		t.Fatalf("LoadQodoCase() = %#v, %v", caseData, err)
	}
	actual, err := LoadFindings(findings)
	if err != nil || len(actual) != 1 || actual[0].File != "src/a.go" {
		t.Fatalf("LoadFindings() = %#v, %v", actual, err)
	}
}

func TestLoadFindingsAcceptsACRResultEnvelope(t *testing.T) {
	path := filepath.Join(t.TempDir(), "result.json")
	contents := "{\"protocol_version\":\"1\",\"session_id\":\"test\",\"findings\":[{\"unit_id\":\"unit-0001\",\"file\":\"src/a.go\",\"start_line\":4,\"end_line\":4,\"severity\":\"high\",\"category\":\"bug\",\"explanation\":\"branch is inverted\",\"evidence\":\"the condition is reversed\",\"confidence\":0.9}],\"rejected\":[],\"summary\":{\"accepted\":1}}"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write ACR result: %v", err)
	}
	findings, err := LoadFindings(path)
	if err != nil || len(findings) != 1 || findings[0].File != "src/a.go" {
		t.Fatalf("LoadFindings() = %#v, %v", findings, err)
	}
}
