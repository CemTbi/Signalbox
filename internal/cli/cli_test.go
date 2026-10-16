// SPDX-License-Identifier: Apache-2.0

package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CemTbi/Signalbox/internal/cli"
)

func fixture(name string) string {
	return filepath.Join("..", "..", "pkg", "specengine", "testdata", name)
}

// run executes the CLI and returns its exit code and output.
func run(t *testing.T, stdin io.Reader, args ...string) (int, string, string) {
	t.Helper()
	if stdin == nil {
		stdin = strings.NewReader("")
	}
	var out, errOut bytes.Buffer
	cmd := cli.NewRootCommand(stdin, &out, &errOut)
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(context.Background())
	return cli.ExitCode(err), out.String(), errOut.String()
}

func TestLintValidDocumentPasses(t *testing.T) {
	code, out, _ := run(t, nil, "lint", fixture("valid-3.1.yaml"))
	if code != cli.ExitOK {
		t.Fatalf("exit %d, want %d\n%s", code, cli.ExitOK, out)
	}
	if !strings.Contains(out, "(OpenAPI 3.1.1): 0 errors, 7 warnings, 0 info, 0 hints") {
		t.Errorf("missing summary line:\n%s", out)
	}
}

func TestLintInvalidDocumentFails(t *testing.T) {
	code, out, _ := run(t, nil, "lint", fixture("unresolved-ref.yaml"))
	if code != cli.ExitFindings {
		t.Fatalf("exit %d, want %d\n%s", code, cli.ExitFindings, out)
	}
	want := fixture("unresolved-ref.yaml") + ":14:17: error unresolved-reference: Component `#/components/schemas/Missing` does not exist"
	if !strings.Contains(out, want) {
		t.Errorf("output does not contain %q:\n%s", want, out)
	}
}

func TestLintFailOnThreshold(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want int
	}{
		{"warnings fail with --fail-on warning", []string{"--fail-on", "warning", fixture("valid-3.1.yaml")}, cli.ExitFindings},
		{"errors pass with --fail-on none", []string{"--fail-on", "none", fixture("unresolved-ref.yaml")}, cli.ExitOK},
		{"no lint rules leaves only validation", []string{"--ruleset", "none", "--fail-on", "warning", fixture("valid-3.0.yaml")}, cli.ExitOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if code, out, _ := run(t, nil, append([]string{"lint"}, tt.args...)...); code != tt.want {
				t.Errorf("exit %d, want %d\n%s", code, tt.want, out)
			}
		})
	}
}

func TestLintJSONOutput(t *testing.T) {
	_, out, _ := run(t, nil, "lint", "--format", "json", fixture("unresolved-ref.yaml"))
	var report struct {
		File    string `json:"file"`
		Format  string `json:"format"`
		Version string `json:"version"`
		Counts  struct {
			Errors int `json:"errors"`
		} `json:"counts"`
		Diagnostics []struct {
			Code  string `json:"code"`
			Range struct {
				Start struct{ Line, Column int } `json:"start"`
			} `json:"range"`
		} `json:"diagnostics"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	if report.Format != "openapi-3.1" || report.Counts.Errors != 1 || len(report.Diagnostics) == 0 {
		t.Errorf("unexpected report: %+v", report)
	}
}

func TestLintGitHubAnnotations(t *testing.T) {
	_, out, _ := run(t, nil, "lint", "--format", "github", "--min-severity", "error", fixture("unresolved-ref.yaml"))
	if !strings.Contains(out, "::error file=") || !strings.Contains(out, ",line=14,col=17,title=unresolved-reference::Component") {
		t.Errorf("no error annotation:\n%s", out)
	}
	if strings.Contains(out, "::warning") {
		t.Errorf("--min-severity error still printed warnings:\n%s", out)
	}
}

func TestLintReadsStandardInput(t *testing.T) {
	data, err := os.ReadFile(fixture("valid-3.1.json"))
	if err != nil {
		t.Fatal(err)
	}
	code, out, _ := run(t, bytes.NewReader(data), "lint", "-")
	if code != cli.ExitOK || !strings.HasPrefix(lastLine(out), "stdin (OpenAPI 3.1.0)") {
		t.Errorf("exit %d, output:\n%s", code, out)
	}
}

func TestLintUsageErrors(t *testing.T) {
	tests := map[string][]string{
		"missing file":    {"lint", "does-not-exist.yaml"},
		"unknown format":  {"lint", "--format", "xml", fixture("valid-3.0.yaml")},
		"unknown ruleset": {"lint", "--ruleset", "strict", fixture("valid-3.0.yaml")},
		"bad fail-on":     {"lint", "--fail-on", "fatal", fixture("valid-3.0.yaml")},
		"no file":         {"lint"},
	}
	for name, args := range tests {
		t.Run(name, func(t *testing.T) {
			if code, _, _ := run(t, nil, args...); code != cli.ExitUsage {
				t.Errorf("exit %d, want %d", code, cli.ExitUsage)
			}
		})
	}
}

func TestVersion(t *testing.T) {
	code, out, _ := run(t, nil, "version")
	if code != cli.ExitOK || !strings.HasPrefix(out, "signalbox 0.0.0-dev (") {
		t.Errorf("exit %d, output %q", code, out)
	}
	_, out, _ = run(t, nil, "version", "--json")
	var info map[string]any
	if err := json.Unmarshal([]byte(out), &info); err != nil || info["version"] != "0.0.0-dev" {
		t.Errorf("version --json = %q (%v)", out, err)
	}
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}
