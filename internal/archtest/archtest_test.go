// Package archtest holds the repo's architecture tests and no code: the import rules and the lint's banned calls.
package archtest_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

const mod = "github.com/DocCyblade/abyssctl"

// allowedModules are the only modules go.mod may require directly; their own requirements may appear too.
var allowedModules = []string{"github.com/spf13/cobra", "modernc.org/sqlite", "golang.org/x/term"}

// importRules are the call-graph rules. A spec that adds a rule adds a row here.
var importRules = []struct {
	name  string
	check func(g graph) []string
}{
	{"go.mod requires only the allowed modules", func(g graph) []string { return g.requiresOnly(allowedModules) }},
	{"only internal/cli and its gendocs import Cobra", onlyImportedBy(
		under("github.com/spf13/cobra"), internal("cli"), internal("cli/gendocs"))},
	{"only internal/backup imports SQLite", onlyImportedBy(under("modernc.org/sqlite"), internal("backup"))},
	{"internal/buildinfo imports only the standard library", importsOnly(internal("buildinfo"))},
	{"internal/hostfs imports only the standard library", importsOnly(internal("hostfs"))},
	{"internal/op imports only the standard library", importsOnly(internal("op"))},
	{"only cmd/abyssctl and internal/cli/gendocs import internal/cli", onlyImportedBy(
		is(internal("cli")), mod+"/cmd/abyssctl", internal("cli/gendocs"))},
	{"release imports only the standard library and internal/buildinfo", importsOnly(
		mod+"/release", internal("buildinfo"))},
	{"no package imports release", onlyImportedBy(under(mod + "/release"))},
}

// TestImportRules checks importRules against `go list -deps -json ./...` of the repo.
func TestImportRules(t *testing.T) {
	g := loadGraph(t)
	if _, ok := g.pkgs[internal("cli")]; !ok {
		t.Fatalf("go list found no %s; is the package graph empty?", internal("cli"))
	}
	for _, r := range importRules {
		t.Run(r.name, func(t *testing.T) {
			for _, v := range r.check(g) {
				t.Error(v)
			}
		})
	}
}

func internal(p string) string { return mod + "/internal/" + p }

// A matcher picks import paths.
type matcher func(path string) bool

// is matches one package.
func is(p string) matcher { return func(path string) bool { return path == p } }

// under matches a package and every package below it.
func under(p string) matcher {
	return func(path string) bool { return path == p || strings.HasPrefix(path, p+"/") }
}

type pkg struct {
	ImportPath string
	Standard   bool
	Imports    []string
	Module     *struct{ Path string }
}

type graph struct {
	pkgs map[string]pkg
	// requires is go.mod's require list; modGraph is `go mod graph`, from "path@version" to its requirements.
	requires []string
	modGraph map[string][]string
}

// own reports whether a package is the repo's.
func (g graph) own(p pkg) bool { return p.Module != nil && p.Module.Path == mod }

// onlyImportedBy fails when a package of the repo other than the allowed ones imports a matched package.
func onlyImportedBy(target matcher, allowed ...string) func(graph) []string {
	return func(g graph) []string {
		var bad []string
		for _, p := range g.sorted() {
			if !g.own(p) || slices.Contains(allowed, p.ImportPath) {
				continue
			}
			for _, imp := range p.Imports {
				if target(imp) {
					bad = append(bad, p.ImportPath+" imports "+imp)
				}
			}
		}
		return bad
	}
}

// importsOnly fails when the package imports anything other than the standard library and the allowed packages.
// It checks direct imports; a package that isn't in the tree yet passes.
func importsOnly(path string, allowed ...string) func(graph) []string {
	return func(g graph) []string {
		var bad []string
		for _, imp := range g.pkgs[path].Imports {
			if !g.pkgs[imp].Standard && !slices.Contains(allowed, imp) {
				bad = append(bad, path+" imports "+imp)
			}
		}
		return bad
	}
}

// requiresOnly fails when go.mod requires a module that is neither allowed nor required, directly or not, by an
// allowed module.
func (g graph) requiresOnly(allowed []string) []string {
	reach := map[string]bool{}
	var walk func(node string)
	walk = func(node string) {
		for _, next := range g.modGraph[node] {
			if p := modPath(next); !reach[p] {
				reach[p] = true
				walk(next)
			}
		}
	}
	for node := range g.modGraph {
		if slices.Contains(allowed, modPath(node)) {
			walk(node)
		}
	}
	var bad []string
	for _, r := range g.requires {
		if !slices.Contains(allowed, r) && !reach[r] {
			bad = append(bad, "go.mod requires "+r)
		}
	}
	return bad
}

func modPath(node string) string { p, _, _ := strings.Cut(node, "@"); return p }

func (g graph) sorted() []pkg {
	var out []pkg
	for _, p := range g.pkgs {
		out = append(out, p)
	}
	slices.SortFunc(out, func(a, b pkg) int { return strings.Compare(a.ImportPath, b.ImportPath) })
	return out
}

func loadGraph(t *testing.T) graph {
	t.Helper()
	root := repoRoot(t)
	g := graph{pkgs: map[string]pkg{}, modGraph: map[string][]string{}}

	dec := json.NewDecoder(bytes.NewReader(goCmd(t, root, "list", "-deps", "-json", "./...")))
	for {
		var p pkg
		if err := dec.Decode(&p); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatalf("go list -deps -json: %v", err)
		}
		g.pkgs[p.ImportPath] = p
	}

	var gomod struct{ Require []struct{ Path string } }
	if err := json.Unmarshal(goCmd(t, root, "mod", "edit", "-json"), &gomod); err != nil {
		t.Fatalf("go mod edit -json: %v", err)
	}
	for _, r := range gomod.Require {
		g.requires = append(g.requires, r.Path)
	}

	for line := range strings.Lines(string(goCmd(t, root, "mod", "graph"))) {
		from, to, ok := strings.Cut(strings.TrimSpace(line), " ")
		if ok {
			g.modGraph[from] = append(g.modGraph[from], to)
		}
	}
	return g
}

func goCmd(t *testing.T, dir string, args ...string) []byte {
	t.Helper()
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go %s: %v\n%s", strings.Join(args, " "), err, stderr.String())
	}
	return out
}

// repoRoot is the main module's folder.
func repoRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}").Output()
	if err != nil {
		t.Fatalf("go list -m: %v", err)
	}
	return strings.TrimSpace(string(out))
}
