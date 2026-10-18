// SPDX-License-Identifier: Apache-2.0

package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CemTbi/Signalbox/internal/cli"
)

const diffBase = `openapi: 3.1.1
info: {title: Orders, version: 1.0.0}
paths:
  /orders:
    get:
      parameters:
        - {name: limit, in: query, schema: {type: integer}}
      responses: {'200': {description: OK}}
`

// The parameter became required, and a new endpoint was added.
const diffHead = `openapi: 3.1.1
info: {title: Orders, version: 1.1.0}
paths:
  /orders:
    get:
      parameters:
        - {name: limit, in: query, required: true, schema: {type: integer}}
      responses: {'200': {description: OK}}
  /refunds:
    post:
      responses: {'201': {description: OK}}
`

func diffFiles(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	base, head := filepath.Join(dir, "base.yaml"), filepath.Join(dir, "head.yaml")
	if err := os.WriteFile(base, []byte(diffBase), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(head, []byte(diffHead), 0o600); err != nil {
		t.Fatal(err)
	}
	return base, head
}

func TestDiffReportsBreakingChanges(t *testing.T) {
	base, head := diffFiles(t)
	code, out, _ := run(t, nil, "diff", base, head)
	if code != cli.ExitFindings {
		t.Fatalf("exit %d, want %d\n%s", code, cli.ExitFindings, out)
	}
	for _, want := range []string{
		head + ":7:", "error request-parameter-became-required: GET /orders: the `query` request parameter `limit` became required",
		"info endpoint-added: POST /refunds:",
		// 1.0.0 to 1.1.0 breaks clients without a new major version.
		"info api-major-version-not-bumped:",
		"1 breaking change, 0 warnings, 2 info",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output doesn't contain %q:\n%s", want, out)
		}
	}
}

func TestDiffThresholds(t *testing.T) {
	base, head := diffFiles(t)
	if code, out, _ := run(t, nil, "diff", "--fail-on", "none", base, head); code != cli.ExitOK {
		t.Errorf("--fail-on none: exit %d\n%s", code, out)
	}
	// Only the addition is left; info doesn't fail by default.
	if code, out, _ := run(t, nil, "diff", "--min-level", "error", base, head); code != cli.ExitFindings || strings.Contains(out, "endpoint-added") {
		t.Errorf("--min-level error: exit %d\n%s", code, out)
	}
	if code, _, _ := run(t, nil, "diff", base, base); code != cli.ExitOK {
		t.Errorf("same document: exit %d", code)
	}
	if code, _, _ := run(t, nil, "diff", "--fail-on", "fatal", base, head); code != cli.ExitUsage {
		t.Errorf("bad --fail-on: exit %d", code)
	}
}

func TestDiffFormats(t *testing.T) {
	base, head := diffFiles(t)

	_, out, _ := run(t, nil, "diff", "--format", "json", base, head)
	var report struct {
		Head    string `json:"head"`
		Changes []struct {
			ID   string              `json:"id"`
			Head *struct{ Line int } `json:"head"`
		} `json:"changes"`
		Counts struct{ Errors int } `json:"counts"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("JSON: %v\n%s", err, out)
	}
	if report.Head != head || report.Counts.Errors != 1 || len(report.Changes) != 3 {
		t.Errorf("report = %+v", report)
	}

	_, out, _ = run(t, nil, "diff", "--format", "github", base, head)
	if !strings.Contains(out, "::error file="+strings.ReplaceAll(head, ":", "%3A")+",line=7,") || !strings.Contains(out, "title=request-parameter-became-required::GET /orders") {
		t.Errorf("GitHub output:\n%s", out)
	}

	_, out, _ = run(t, nil, "diff", "--format", "markdown", base, head)
	if !strings.Contains(out, "### API changes: 1 breaking change") || !strings.Contains(out, "| 🔴 Breaking | `GET /orders` |") {
		t.Errorf("Markdown output:\n%s", out)
	}

	if code, _, _ := run(t, nil, "diff", "--format", "xml", base, head); code != cli.ExitUsage {
		t.Errorf("unknown format: exit %d", code)
	}
}

func TestDiffReadsOneDocumentFromStdin(t *testing.T) {
	_, head := diffFiles(t)
	code, out, _ := run(t, strings.NewReader(diffBase), "diff", "-", head)
	if code != cli.ExitFindings || !strings.Contains(out, "stdin → "+head) {
		t.Errorf("exit %d\n%s", code, out)
	}
	if code, _, errOut := run(t, strings.NewReader(diffBase), "diff", "-", "-"); code != cli.ExitUsage {
		t.Errorf("both from stdin: exit %d %s", code, errOut)
	}
}
