package main_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/DocCyblade/abyssctl/internal/buildinfo"
)

const (
	bi          = "github.com/DocCyblade/abyssctl/internal/buildinfo"
	stampCommit = "9b1c4e2d7f3a8b6c5d4e3f2a1b0c9d8e7f6a5b4c"
)

// buildBinary builds abyssctl for the native platform into t.TempDir(), stamped with ldflags.
func buildBinary(t *testing.T, ldflags string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "abyssctl")
	cmd := exec.Command("go", "build", "-trimpath", "-ldflags", ldflags, "-o", bin, ".")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return bin
}

// stamped and unstamped build the two binaries the smoke tests run.
func stamped(t *testing.T) string {
	t.Helper()
	return buildBinary(t, "-X "+bi+".Version=0.1.0 -X "+bi+".Commit="+stampCommit)
}

func unstamped(t *testing.T) string {
	t.Helper()
	return buildBinary(t, "")
}

// result is what one run of the binary gave.
type result struct {
	code           int
	stdout, stderr string
}

// run runs bin with args in dir (a new temp folder when dir is "") and returns its exit code and output.
func run(t *testing.T, bin, dir string, args ...string) result {
	t.Helper()
	if dir == "" {
		dir = t.TempDir()
	}
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return result{0, stdout.String(), stderr.String()}
	case errors.As(err, &exit):
		return result{exit.ExitCode(), stdout.String(), stderr.String()}
	}
	t.Fatalf("run %v: %v", args, err)
	return result{}
}

// skipIfRoot skips a test that needs a non-root user.
func skipIfRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("needs a non-root user; journey 8 covers this as root")
	}
}

func TestSmoke_VersionPrintsStampedValues(t *testing.T) {
	bin := buildBinary(t, "-X "+bi+".Version=0.1.0 -X "+bi+".Commit="+stampCommit)

	cmd := exec.Command(bin, "version")
	cmd.Dir = t.TempDir()
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("abyssctl version: %v\nstderr: %s", err, stderr.String())
	}

	caddy := buildinfo.CompiledPins().Caddy
	want := "abyssctl  0.1.0\n" +
		"commit    " + stampCommit + "\n" +
		"go        " + runtime.Version() + "\n" +
		"platform  " + runtime.GOOS + "/" + runtime.GOARCH + "\n" +
		"caddy     " + caddy.Version + " " + caddy.Digest + "\n"
	if got := stdout.String(); got != want {
		t.Errorf("stdout =\n%s\nwant\n%s", got, want)
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
}

func TestSmoke_VersionJSON(t *testing.T) {
	r := run(t, stamped(t), "", "version", "--json")
	if r.code != 0 {
		t.Fatalf("exit = %d, want 0\nstderr: %s", r.code, r.stderr)
	}

	caddy := buildinfo.CompiledPins().Caddy
	want := "{\n" +
		"  \"schema\": 1,\n" +
		"  \"version\": \"0.1.0\",\n" +
		"  \"commit\": \"" + stampCommit + "\",\n" +
		"  \"go\": \"" + runtime.Version() + "\",\n" +
		"  \"os\": \"" + runtime.GOOS + "\",\n" +
		"  \"arch\": \"" + runtime.GOARCH + "\",\n" +
		"  \"caddy\": {\n" +
		"    \"version\": \"" + caddy.Version + "\",\n" +
		"    \"digest\": \"" + caddy.Digest + "\"\n" +
		"  },\n" +
		"  \"warnings\": []\n" +
		"}\n"
	if r.stdout != want {
		t.Errorf("stdout =\n%s\nwant\n%s", r.stdout, want)
	}

	dec := json.NewDecoder(bytes.NewReader([]byte(r.stdout)))
	var doc map[string]any
	if err := dec.Decode(&doc); err != nil {
		t.Fatalf("stdout is not a JSON document: %v", err)
	}
	if dec.More() {
		t.Error("stdout holds more than one JSON document")
	}
	if r.stderr != "" {
		t.Errorf("stderr = %q, want empty", r.stderr)
	}
}

func TestSmoke_ExemptCommandsNeedNoRoot(t *testing.T) {
	skipIfRoot(t)
	builds := map[string]string{"stamped": stamped(t), "unstamped": unstamped(t)}
	cases := [][]string{{"version"}, {"--help"}, {"-h"}, {"help"}, {}, {"host", "setup", "--help"}}
	for build, bin := range builds {
		for _, args := range cases {
			t.Run(fmt.Sprintf("%s %q", build, args), func(t *testing.T) {
				r := run(t, bin, "", args...)
				if r.code != 0 {
					t.Errorf("exit = %d, want 0\nstderr: %s", r.code, r.stderr)
				}
				if r.stdout == "" {
					t.Error("stdout is empty")
				}
			})
		}
	}
}

func TestSmoke_NotRootIsRefused(t *testing.T) {
	skipIfRoot(t)
	dir := t.TempDir()
	r := run(t, stamped(t), dir, "host", "setup")
	if r.code != 2 {
		t.Errorf("exit = %d, want 2", r.code)
	}
	if want := "abyssctl host setup needs root. Run it with sudo.\n"; r.stderr != want {
		t.Errorf("stderr = %q, want %q", r.stderr, want)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("working folder holds %d entries, want none", len(entries))
	}
}

func TestSmoke_UnstampedIsRefused(t *testing.T) {
	r := run(t, unstamped(t), "", "host", "setup")
	if r.code != 2 {
		t.Errorf("exit = %d, want 2", r.code)
	}
	want := "abyssctl is an unstamped development build (" + buildinfo.Unstamped + ") and won't act on a Host.\n" +
		"Build it with: make build VERSION=X.Y.Z\n"
	if r.stderr != want {
		t.Errorf("stderr = %q, want %q", r.stderr, want)
	}
}

func TestSmoke_BadArgumentsExit2(t *testing.T) {
	bin := stamped(t)
	cases := []struct {
		name string
		args []string
	}{
		{"unknown command", []string{"frobnicate"}},
		{"completion is unknown", []string{"completion", "bash"}},
		{"unknown flag", []string{"--frobnicate"}},
		{"unknown flag on a verb", []string{"version", "--frobnicate"}},
		{"unknown flag on a nested verb", []string{"host", "setup", "--frobnicate"}},
		{"argument to a verb that takes none", []string{"version", "extra"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := run(t, bin, "", c.args...)
			if r.code != 2 {
				t.Errorf("exit = %d, want 2\nstderr: %s", r.code, r.stderr)
			}
			if r.stdout != "" {
				t.Errorf("stdout = %q, want empty", r.stdout)
			}
		})
	}
}
