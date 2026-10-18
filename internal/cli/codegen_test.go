// SPDX-License-Identifier: Apache-2.0

package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CemTbi/Signalbox/internal/cli"
)

const codegenSpec = `openapi: 3.1.1
info: {title: Orders API, version: 1.0.0}
paths:
  /orders:
    get:
      operationId: listOrders
      responses:
        '200':
          description: OK
          content:
            application/json:
              schema: {type: array, prefixItems: [{type: string}]}
`

func TestCodegenWritesThePackage(t *testing.T) {
	dir := t.TempDir()
	spec := filepath.Join(dir, "openapi.yaml")
	if err := os.WriteFile(spec, []byte(codegenSpec), 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "gen")
	code, stdout, stderr := run(t, nil, "codegen", "--target", "go-client", "--out", out, spec)
	if code != cli.ExitOK {
		t.Fatalf("exit %d\n%s%s", code, stdout, stderr)
	}
	gen := filepath.Join(out, "orders", "orders.gen.go")
	if data, err := os.ReadFile(gen); err != nil || !strings.Contains(string(data), "package orders") {
		t.Fatalf("%s: %v", gen, err)
	}
	if !strings.Contains(stdout, "wrote "+gen) {
		t.Errorf("stdout:\n%s", stdout)
	}
	if !strings.Contains(stderr, spec+":12:37: warning: prefixItems isn't expressed") {
		t.Errorf("stderr:\n%s", stderr)
	}

	// Generating again replaces the code, but keeps a changed README.
	readme := filepath.Join(out, "orders", "README.md")
	if err := os.WriteFile(readme, []byte("ours"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := run(t, nil, "codegen", "-t", "go-client", "-o", out, "--fail-on-warning", spec); code != cli.ExitFindings {
		t.Errorf("--fail-on-warning: exit %d", code)
	}
	if data, _ := os.ReadFile(readme); string(data) != "ours" {
		t.Errorf("README = %q", data)
	}
}

func TestCodegenFlags(t *testing.T) {
	dir := t.TempDir()
	spec := filepath.Join(dir, "openapi.yaml")
	if err := os.WriteFile(spec, []byte(codegenSpec), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := run(t, nil, "codegen", "--target", "cobol-client", spec); code != cli.ExitUsage {
		t.Errorf("unknown target: exit %d", code)
	}
	if code, _, _ := run(t, nil, "codegen", "--target", "go-server", "--package", "Bad-Name", "--out", dir, spec); code != cli.ExitUsage {
		t.Errorf("bad package: exit %d", code)
	}
	if code, _, _ := run(t, nil, "codegen", spec); code != cli.ExitUsage {
		t.Errorf("no target: exit %d", code)
	}
	code, _, stderr := run(t, nil, "codegen", "--target", "go-server", "--package", "shop", "--out", dir, spec)
	if _, err := os.Stat(filepath.Join(dir, "shop", "shop.gen.go")); code != cli.ExitOK || err != nil {
		t.Errorf("go-server: exit %d, %v\n%s", code, err, stderr)
	}
}
