// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package benchmark

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImportQodoCreatesCanonicalManifestWithProvenance(t *testing.T) {
	raw := strings.Join([]string{
		`{"repo":"example/project","pr_url_to_review":"https://github.com/example/project/pull/7","num_of_issues":1,"issues":[{"title":"Lost cleanup","description":"The listener is never removed.","file_path":"src/service.go","start_line":14,"end_line":15}]}`,
		`{"repo":"example/other","pr_url_to_review":"https://github.com/example/other/pull/3","num_of_issues":1,"issues":[{"title":"Missing guard","description":"Nil input panics.","file_path":null,"start_line":null,"end_line":null}]}`,
	}, "\n") + "\n"
	sum := sha256.Sum256([]byte(raw))

	manifest, err := ImportQodo(strings.NewReader(raw), DatasetMetadata{
		ID: "qodo-pr-review-bench", Version: QodoRevision, SourceURL: QodoSourceURL,
		Revision: QodoRevision, SHA256: hex.EncodeToString(sum[:]), License: "MIT",
	})
	if err != nil {
		t.Fatalf("ImportQodo: %v", err)
	}
	if manifest.ProtocolVersion != BenchmarkProtocolVersion || len(manifest.Cases) != 2 {
		t.Fatalf("unexpected manifest: %#v", manifest)
	}
	first := manifest.Cases[0]
	if first.ID != "qodo-example-project-pr-7" || first.Repository != "https://github.com/example/project.git" {
		t.Fatalf("unexpected first case identity: %#v", first)
	}
	if first.Expected[0].File != "src/service.go" || first.Expected[0].StartLine != 14 {
		t.Fatalf("unexpected expected finding: %#v", first.Expected[0])
	}
	if manifest.Cases[1].Expected[0].File != "" {
		t.Fatalf("unanchored issue should be retained: %#v", manifest.Cases[1].Expected[0])
	}
}

func TestImportQodoRejectsMalformedMissingAndDuplicateCases(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "malformed", raw: "{\n", want: "decode Qodo row 1"},
		{name: "missing PR", raw: `{"repo":"example/project","issues":[]}` + "\n", want: "pr_url_to_review"},
		{name: "issue count", raw: `{"repo":"example/project","pr_url_to_review":"https://github.com/example/project/pull/1","num_of_issues":2,"issues":[]}` + "\n", want: "num_of_issues"},
		{name: "duplicate", raw: strings.Repeat(`{"repo":"example/project","pr_url_to_review":"https://github.com/example/project/pull/1","num_of_issues":0,"issues":[]}`+"\n", 2), want: "duplicate benchmark case"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ImportQodo(strings.NewReader(tt.raw), DatasetMetadata{ID: "qodo", Version: "test"})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("ImportQodo error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestFetchQodoPinsSourceAndPersistsVerifiedManifest(t *testing.T) {
	raw := `{"repo":"example/project","pr_url_to_review":"https://github.com/example/project/pull/7","num_of_issues":0,"issues":[]}` + "\n"
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(raw)),
			Request:    request,
		}, nil
	})}

	output := filepath.Join(t.TempDir(), "qodo.json")
	manifest, err := fetchQodoFromURL(context.Background(), client, "https://example.test/dataset.jsonl", output)
	if err != nil {
		t.Fatalf("fetchQodoFromURL: %v", err)
	}
	wantSum := sha256.Sum256([]byte(raw))
	if manifest.Dataset.SHA256 != hex.EncodeToString(wantSum[:]) || manifest.Dataset.Revision != QodoRevision {
		t.Fatalf("provenance was not pinned: %#v", manifest.Dataset)
	}
	loaded, err := LoadManifest(output)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if loaded.Dataset.SHA256 != manifest.Dataset.SHA256 || len(loaded.Cases) != 1 {
		t.Fatalf("persisted manifest mismatch: %#v", loaded)
	}

	data, err := os.ReadFile(output)
	if err != nil || !strings.HasSuffix(string(data), "\n") {
		t.Fatalf("manifest should be readable JSON with LF terminator: %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestLoadManifestRejectsChecksumAndDuplicateCaseTampering(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.json")
	manifest := Manifest{
		ProtocolVersion: BenchmarkProtocolVersion,
		Dataset:         DatasetMetadata{ID: "fixture", Version: "1", SHA256: strings.Repeat("a", 64)},
		Cases: []Case{
			{ID: "same", Repository: "https://github.com/example/project.git", PRURL: "https://github.com/example/project/pull/1"},
			{ID: "same", Repository: "https://github.com/example/project.git", PRURL: "https://github.com/example/project/pull/2"},
		},
	}
	if err := SaveManifest(path, manifest); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}
	_, err := LoadManifest(path)
	if err == nil || !strings.Contains(err.Error(), "duplicate benchmark case") {
		t.Fatalf("LoadManifest error = %v", err)
	}
}
