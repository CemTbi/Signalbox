// SPDX-License-Identifier: Apache-2.0

// Package archtest enforces the import rules of the modular monolith from
// docs/adr/0001-single-binary-with-roles.md, so the module boundaries hold
// as the code grows.
package archtest

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const module = "github.com/CemTbi/Signalbox"

// imports maps each package in the repository to the repository packages
// its non-test files import.
func imports(t *testing.T) map[string][]string {
	t.Helper()
	root := filepath.Join("..", "..")
	fset := token.NewFileSet()
	out := map[string][]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch name := d.Name(); {
			case p != root && strings.HasPrefix(name, "."), name == "node_modules", name == "testdata", name == "web":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, p, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, filepath.Dir(p))
		if err != nil {
			return err
		}
		pkg := path.Join(module, filepath.ToSlash(rel))
		for _, imp := range f.Imports {
			ip, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				return err
			}
			if strings.HasPrefix(ip, module+"/") {
				out[pkg] = append(out[pkg], ip)
			}
		}
		if _, ok := out[pkg]; !ok {
			out[pkg] = nil
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// rule returns a violation message, or "" when pkg may import imp.
type rule func(pkg, imp string) string

var rules = []rule{
	// pkg/ is public API: other projects can import it, so it can't depend
	// on internal code.
	func(pkg, imp string) string {
		if under(pkg, "pkg") && under(imp, "internal") {
			return "public packages in pkg/ must not import internal packages"
		}
		return ""
	},
	// Platform packages are infrastructure that every module uses, so they
	// can't depend on a module.
	func(pkg, imp string) string {
		if under(pkg, "internal/platform") && under(imp, "internal/modules") {
			return "platform packages must not import modules"
		}
		return ""
	},
	// Modules expose their root package. Another module may use that, never
	// the packages inside it.
	func(pkg, imp string) string {
		from, to := moduleOf(pkg), moduleOf(imp)
		if from != "" && to != "" && from != to && imp != module+"/internal/modules/"+to {
			return "modules may only import another module's root package"
		}
		return ""
	},
	// Modules don't know how they are served.
	func(pkg, imp string) string {
		if (under(pkg, "internal/modules") || under(pkg, "internal/platform")) &&
			(under(imp, "internal/httpapi") || under(imp, "internal/app") || under(imp, "internal/cli") || under(imp, "cmd")) {
			return "modules and platform packages must not import the HTTP API, the app or commands"
		}
		return ""
	},
}

func TestImportRules(t *testing.T) {
	graph := imports(t)
	if len(graph) < 10 {
		t.Fatalf("found only %d packages; is the repository root right?", len(graph))
	}
	var pkgs []string
	for pkg := range graph {
		pkgs = append(pkgs, pkg)
	}
	sort.Strings(pkgs)
	for _, pkg := range pkgs {
		for _, imp := range graph[pkg] {
			for _, r := range rules {
				if msg := r(pkg, imp); msg != "" {
					t.Errorf("%s imports %s: %s", strings.TrimPrefix(pkg, module+"/"), strings.TrimPrefix(imp, module+"/"), msg)
				}
			}
		}
	}
}

func TestRulesCatchViolations(t *testing.T) {
	bad := [][2]string{
		{"pkg/specengine", "internal/app"},
		{"internal/platform/postgres", "internal/modules/identity"},
		{"internal/modules/workspaces", "internal/modules/identity/store"},
		{"internal/modules/identity", "internal/httpapi"},
	}
	for _, b := range bad {
		pkg, imp := module+"/"+b[0], module+"/"+b[1]
		caught := false
		for _, r := range rules {
			if r(pkg, imp) != "" {
				caught = true
			}
		}
		if !caught {
			t.Errorf("no rule stops %s importing %s", b[0], b[1])
		}
	}
	if msg := rules[2](module+"/internal/modules/workspaces", module+"/internal/modules/identity"); msg != "" {
		t.Errorf("a module may import another module's root package, but: %s", msg)
	}
}

func under(pkg, dir string) bool {
	p := strings.TrimPrefix(pkg, module+"/")
	return p == dir || strings.HasPrefix(p, dir+"/")
}

// moduleOf returns the module a package belongs to, or "".
func moduleOf(pkg string) string {
	rest, ok := strings.CutPrefix(pkg, module+"/internal/modules/")
	if !ok {
		return ""
	}
	name, _, _ := strings.Cut(rest, "/")
	return name
}
