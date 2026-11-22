package cli_test

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/DocCyblade/abyssctl/internal/buildinfo"
	"github.com/DocCyblade/abyssctl/internal/cli"
	"github.com/DocCyblade/abyssctl/internal/hostfs"
	"github.com/DocCyblade/abyssctl/internal/op"
)

func TestStartupRefusals(t *testing.T) {
	cases := []struct {
		name    string
		version string
		euid    int
		want    string
	}{
		{
			name:    "unstamped",
			version: buildinfo.Unstamped,
			euid:    0,
			want: "abyssctl is an unstamped development build (0.0.0-dev) and won't act on a Host.\n" +
				"Build it with: make build VERSION=X.Y.Z\n",
		},
		{
			name:    "badly stamped",
			version: "v0.1",
			euid:    0,
			want: "abyssctl is an unstamped development build (v0.1) and won't act on a Host.\n" +
				"Build it with: make build VERSION=X.Y.Z\n",
		},
		{
			name:    "not root",
			version: "0.1.0",
			euid:    1000,
			want:    "abyssctl host setup needs root. Run it with sudo.\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			saved := buildinfo.Version
			buildinfo.Version = tc.version
			t.Cleanup(func() { buildinfo.Version = saved })

			var stdout, stderr bytes.Buffer
			code := cli.Run(context.Background(), cli.Env{
				Args:   []string{"host", "setup"},
				Stdin:  strings.NewReader(""),
				Stdout: &stdout,
				Stderr: &stderr,
				Euid:   func() int { return tc.euid },
				Paths:  hostfs.Under(t.TempDir()),
				Clock:  op.NewManualClock(time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)),
			})

			if code != 2 {
				t.Errorf("exit = %d, want 2", code)
			}
			if got := stderr.String(); got != tc.want {
				t.Errorf("stderr = %q, want %q", got, tc.want)
			}
			if stdout.Len() != 0 {
				t.Errorf("stdout = %q, want empty", stdout.String())
			}
		})
	}
}
