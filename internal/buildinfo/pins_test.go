package buildinfo_test

import (
	"regexp"
	"testing"

	"github.com/DocCyblade/abyssctl/internal/buildinfo"
)

var digestRE = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

func TestCompiledPins_Valid(t *testing.T) {
	p := buildinfo.CompiledPins()
	if p.Caddy.Version == "" {
		t.Error("caddy.version is empty")
	}
	if !digestRE.MatchString(p.Caddy.Digest) {
		t.Errorf("caddy.digest = %q, want sha256:<64 hex>", p.Caddy.Digest)
	}
	if p.DockerEngineMin == "" {
		t.Error("docker_engine_min is empty")
	}
	if p.DockerComposeMin == "" {
		t.Error("docker_compose_min is empty")
	}
}

const validDigest = "sha256:4851e54a46b206517c574821bb5cc67f170d2156041e9fbc4c9193b45e84a75b"

func TestParsePins_Rejects(t *testing.T) {
	tests := []struct {
		name string
		json string
	}{
		{"missing caddy", `{"docker_engine_min":"25.0.5","docker_compose_min":"2.24"}`},
		{"missing caddy version", `{"caddy":{"digest":"` + validDigest + `"},"docker_engine_min":"25.0.5","docker_compose_min":"2.24"}`},
		{"missing caddy digest", `{"caddy":{"version":"2.11.9"},"docker_engine_min":"25.0.5","docker_compose_min":"2.24"}`},
		{"missing docker_engine_min", `{"caddy":{"version":"2.11.9","digest":"` + validDigest + `"},"docker_compose_min":"2.24"}`},
		{"missing docker_compose_min", `{"caddy":{"version":"2.11.9","digest":"` + validDigest + `"},"docker_engine_min":"25.0.5"}`},
		{"unknown top-level key", `{"caddy":{"version":"2.11.9","digest":"` + validDigest + `"},"docker_engine_min":"25.0.5","docker_compose_min":"2.24","extra":"x"}`},
		{"unknown caddy key", `{"caddy":{"version":"2.11.9","digest":"` + validDigest + `","tag":"x"},"docker_engine_min":"25.0.5","docker_compose_min":"2.24"}`},
		{"digest not sha256", `{"caddy":{"version":"2.11.9","digest":"md5:abc"},"docker_engine_min":"25.0.5","docker_compose_min":"2.24"}`},
		{"digest too short", `{"caddy":{"version":"2.11.9","digest":"sha256:abcd"},"docker_engine_min":"25.0.5","docker_compose_min":"2.24"}`},
		{"not json", `caddy: 2.11.9`},
		{"trailing data", `{"caddy":{"version":"2.11.9","digest":"` + validDigest + `"},"docker_engine_min":"25.0.5","docker_compose_min":"2.24"} {}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := buildinfo.ParsePins([]byte(tt.json)); err == nil {
				t.Errorf("ParsePins accepted %s", tt.json)
			}
		})
	}

	t.Run("accepts a complete file", func(t *testing.T) {
		got, err := buildinfo.ParsePins([]byte(`{"caddy":{"version":"2.11.9","digest":"` + validDigest + `"},"docker_engine_min":"25.0.5","docker_compose_min":"2.24"}`))
		if err != nil {
			t.Fatalf("ParsePins: %v", err)
		}
		want := buildinfo.Pins{
			Caddy:            buildinfo.ImagePin{Version: "2.11.9", Digest: validDigest},
			DockerEngineMin:  "25.0.5",
			DockerComposeMin: "2.24",
		}
		if got != want {
			t.Errorf("ParsePins = %+v, want %+v", got, want)
		}
	})
}
