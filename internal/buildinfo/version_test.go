package buildinfo_test

import (
	"os"
	"strings"
	"testing"

	"github.com/DocCyblade/abyssctl/internal/buildinfo"
)

func TestCheckStamped(t *testing.T) {
	tests := []struct {
		version string
		ok      bool
	}{
		{"0.0.0-dev", false},
		{"", false},
		{"v0.1.0", false},
		{"0.1", false},
		{"0.1.0", true},
		{"0.2.0-dev.3", true},
	}
	for _, tt := range tests {
		t.Run(tt.version, func(t *testing.T) {
			old := buildinfo.Version
			t.Cleanup(func() { buildinfo.Version = old })
			buildinfo.Version = tt.version
			err := buildinfo.CheckStamped()
			if tt.ok && err != nil {
				t.Errorf("CheckStamped with Version %q: %v", tt.version, err)
			}
			if !tt.ok && err == nil {
				t.Errorf("CheckStamped accepted Version %q", tt.version)
			}
		})
	}
}

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		name string
		a, b string
		want int
	}{
		{"equal", "1.2.3", "1.2.3", 0},
		{"major", "1.0.0", "2.0.0", -1},
		{"minor", "1.2.0", "1.10.0", -1},
		{"patch numeric not lexical", "1.0.10", "1.0.9", 1},
		{"pre-release before its release", "1.0.0-rc.1", "1.0.0", -1},
		{"release after its pre-release", "1.0.0", "1.0.0-alpha", 1},
		{"pre-release before an earlier core", "0.2.0-dev.3", "0.1.0", 1},
		{"numeric identifiers compare as numbers", "1.0.0-alpha.2", "1.0.0-alpha.10", -1},
		{"numeric identifier before alphanumeric", "1.0.0-alpha.1", "1.0.0-alpha.beta", -1},
		{"alphanumeric identifiers compare in ASCII order", "1.0.0-beta", "1.0.0-alpha", 1},
		{"longer pre-release after its prefix", "1.0.0-alpha", "1.0.0-alpha.1", -1},
		{"build metadata ignored", "1.0.0+b1", "1.0.0+b2", 0},
		{"build metadata ignored against no build", "1.0.0", "1.0.0+20260101", 0},
		{"build metadata ignored on a pre-release", "1.0.0-rc.1+x", "1.0.0-rc.1", 0},
		// SemVer 2.0 §11's example chain, one step at a time.
		{"spec chain 1", "1.0.0-alpha", "1.0.0-alpha.1", -1},
		{"spec chain 2", "1.0.0-alpha.1", "1.0.0-alpha.beta", -1},
		{"spec chain 3", "1.0.0-alpha.beta", "1.0.0-beta", -1},
		{"spec chain 4", "1.0.0-beta", "1.0.0-beta.2", -1},
		{"spec chain 5", "1.0.0-beta.2", "1.0.0-beta.11", -1},
		{"spec chain 6", "1.0.0-beta.11", "1.0.0-rc.1", -1},
		{"spec chain 7", "1.0.0-rc.1", "1.0.0", -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := buildinfo.CompareVersions(tt.a, tt.b)
			if err != nil {
				t.Fatalf("CompareVersions(%q, %q): %v", tt.a, tt.b, err)
			}
			if got != tt.want {
				t.Errorf("CompareVersions(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
			}
		})
	}

	invalid := []struct {
		name string
		a, b string
	}{
		{"leading v on the left", "v1.0.0", "1.0.0"},
		{"leading v on the right", "1.0.0", "v1.0.0"},
		{"two parts", "1.0", "1.0.0"},
		{"four parts", "1.0.0", "1.0.0.0"},
		{"leading zero", "01.0.0", "1.0.0"},
		{"leading zero in a numeric pre-release", "1.0.0-01", "1.0.0"},
		{"empty pre-release", "1.0.0-", "1.0.0"},
		{"empty build", "1.0.0", "1.0.0+"},
		{"empty identifier", "1.0.0-a..b", "1.0.0"},
		{"bad character", "1.0.0-a_b", "1.0.0"},
		{"empty", "", "1.0.0"},
	}
	for _, tt := range invalid {
		t.Run(tt.name, func(t *testing.T) {
			if got, err := buildinfo.CompareVersions(tt.a, tt.b); err == nil {
				t.Errorf("CompareVersions(%q, %q) = %d, want an error", tt.a, tt.b, got)
			}
		})
	}
}

func TestPlainVersion(t *testing.T) {
	data, err := os.ReadFile("testdata/plain_versions.txt")
	if err != nil {
		t.Fatal(err)
	}
	cases := 0
	for i, text := range strings.Split(string(data), "\n") {
		line := i + 1
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		fields := strings.Fields(text)
		if len(fields) != 2 || (fields[1] != "true" && fields[1] != "false") {
			t.Fatalf("plain_versions.txt:%d: %q is not \"<version> <true|false>\"", line, text)
		}
		version, want := fields[0], fields[1] == "true"
		cases++
		t.Run(version, func(t *testing.T) {
			if got := buildinfo.PlainVersion(version); got != want {
				t.Errorf("PlainVersion(%q) = %v, want %v (plain_versions.txt:%d)", version, got, want, line)
			}
		})
	}
	if cases == 0 {
		t.Fatal("plain_versions.txt has no cases")
	}
}
