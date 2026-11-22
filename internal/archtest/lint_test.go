package archtest_test

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"testing"
)

// wantRE marks a line of the fixture module where the lint must report a banned call.
var wantRE = regexp.MustCompile(`// want "rule: (\w+)"`)

// TestLintForbids runs the repo's .golangci.yml over testdata/forbid/, a module that makes every banned call in
// a module package, in the packages each call is allowed in, in a _test.go file, and in journey/ and release/.
// The fixture's `// want` comments are the expected findings; any other forbidigo finding is a failure.
func TestLintForbids(t *testing.T) {
	root := repoRoot(t)
	lint := filepath.Join(root, "bin", "golangci-lint")
	if _, err := os.Stat(lint); err != nil {
		t.Fatalf("%s not found: run `make tools` first (%v)", lint, err)
	}

	dir := t.TempDir()
	copyFile(t, filepath.Join(root, ".golangci.yml"), filepath.Join(dir, ".golangci.yml"))
	copyTree(t, filepath.Join(root, "internal", "archtest", "testdata", "forbid"), dir)

	want := wantedFindings(t, dir)
	if len(want) == 0 {
		t.Fatal("the fixture module marks no banned calls")
	}
	got := forbidigoFindings(t, lint, dir)

	for _, f := range want {
		if !slices.Contains(got, f) {
			t.Errorf("make lint does not report %s", f)
		}
	}
	for _, f := range got {
		if !slices.Contains(want, f) {
			t.Errorf("make lint reports %s, which the rules allow", f)
		}
	}
}

// A finding is "file:line rule: name", with the file relative to the fixture module.
func wantedFindings(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".go" {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		sc := bufio.NewScanner(f)
		for line := 1; sc.Scan(); line++ {
			if m := wantRE.FindStringSubmatch(sc.Text()); m != nil {
				out = append(out, finding(rel, line, m[1]))
			}
		}
		return sc.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

var ruleRE = regexp.MustCompile(`rule: (\w+)`)

func forbidigoFindings(t *testing.T, lint, dir string) []string {
	t.Helper()
	report := filepath.Join(t.TempDir(), "report.json")
	cmd := exec.Command(lint, "run", "--output.json.path", report, "--max-issues-per-linter", "0",
		"--max-same-issues", "0", "./...")
	cmd.Dir = dir
	// The fixture's go line is older than the repo's toolchain; lint it with the toolchain running this test,
	// so nothing is downloaded.
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN="+runtime.Version(), "GOFLAGS=")
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		t.Fatalf("golangci-lint: %v\n%s", err, out)
	}
	b, err := os.ReadFile(report)
	if err != nil {
		t.Fatalf("golangci-lint wrote no report: %v\n%s", err, out)
	}
	var issues struct {
		Issues []struct {
			FromLinter string
			Text       string
			Pos        struct {
				Filename string
				Line     int
			}
		}
	}
	if err := json.Unmarshal(b, &issues); err != nil {
		t.Fatalf("golangci-lint report: %v\n%s", err, out)
	}
	var found []string
	for _, is := range issues.Issues {
		if is.FromLinter != "forbidigo" {
			continue
		}
		rule := "?"
		if m := ruleRE.FindStringSubmatch(is.Text); m != nil {
			rule = m[1]
		}
		found = append(found, finding(is.Pos.Filename, is.Pos.Line, rule))
	}
	return found
}

func finding(file string, line int, rule string) string {
	return fmt.Sprintf("%s:%d rule: %s", filepath.ToSlash(file), line, rule)
}

func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o700)
		}
		copyFile(t, path, filepath.Join(dst, rel))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, b, 0o600); err != nil {
		t.Fatal(err)
	}
}
