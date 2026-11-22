package main_test

import (
	"bytes"
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
