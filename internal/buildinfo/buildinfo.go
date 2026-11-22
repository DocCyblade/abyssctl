// Package buildinfo holds what is stamped into the binary at build time or compiled in: the -X variables,
// the pins from pins.json, and the build's Info.
package buildinfo

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"runtime"
	"sync"
)

// Set with -ldflags "-X github.com/DocCyblade/abyssctl/internal/buildinfo.<Name>=<value>".
// The defaults are the release values, except Version and Commit, which every release build stamps.
var (
	Version     = Unstamped                              // SemVer without a leading "v", e.g. "0.1.0"
	Commit      = "unknown"                              // full git commit hash
	ActualRepo  = "docker.io/actualbudget/actual-server" // image name, registry host included
	CaddyRepo   = "docker.io/library/caddy"
	RegistryURL = "https://registry-1.docker.io" // base URL of the registry API for both repos
	PushoverURL = "https://api.pushover.net"     // base URL of the Pushover API
)

// Unstamped is the Version of a build that was not stamped with make build VERSION=X.Y.Z.
const Unstamped = "0.0.0-dev"

// ImagePin is an image version and the digest of its multi-arch index.
type ImagePin struct {
	Version string
	Digest  string // "sha256:<64 hex>"
}

// Pins are the values compiled in from pins.json.
type Pins struct {
	Caddy            ImagePin
	DockerEngineMin  string // e.g. "25.0.5"
	DockerComposeMin string // e.g. "2.24"
}

//go:embed pins.json
var pinsJSON []byte

var compiledPins = sync.OnceValue(func() Pins {
	p, err := ParsePins(pinsJSON)
	if err != nil {
		panic("buildinfo: the embedded pins.json is broken: " + err.Error())
	}
	return p
})

// CompiledPins returns the pins parsed once from the embedded pins.json. It panics if the file does not parse,
// which can only be a broken commit.
func CompiledPins() Pins { return compiledPins() }

var digestRE = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// ParsePins parses a pins.json document. All four values are required and unknown keys are an error.
func ParsePins(data []byte) (Pins, error) {
	var raw struct {
		Caddy *struct {
			Version string `json:"version"`
			Digest  string `json:"digest"`
		} `json:"caddy"`
		DockerEngineMin  string `json:"docker_engine_min"`
		DockerComposeMin string `json:"docker_compose_min"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&raw); err != nil {
		return Pins{}, fmt.Errorf("parse pins: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return Pins{}, errors.New("parse pins: data after the JSON object")
	}
	switch {
	case raw.Caddy == nil:
		return Pins{}, errors.New("parse pins: caddy is missing")
	case raw.Caddy.Version == "":
		return Pins{}, errors.New("parse pins: caddy.version is missing")
	case raw.Caddy.Digest == "":
		return Pins{}, errors.New("parse pins: caddy.digest is missing")
	case !digestRE.MatchString(raw.Caddy.Digest):
		return Pins{}, fmt.Errorf("parse pins: caddy.digest %q is not sha256:<64 hex>", raw.Caddy.Digest)
	case raw.DockerEngineMin == "":
		return Pins{}, errors.New("parse pins: docker_engine_min is missing")
	case raw.DockerComposeMin == "":
		return Pins{}, errors.New("parse pins: docker_compose_min is missing")
	}
	return Pins{
		Caddy:            ImagePin{Version: raw.Caddy.Version, Digest: raw.Caddy.Digest},
		DockerEngineMin:  raw.DockerEngineMin,
		DockerComposeMin: raw.DockerComposeMin,
	}, nil
}

// Info is what `abyssctl version` reports.
type Info struct {
	Version, Commit, GoVersion, OS, Arch string
	Caddy                                ImagePin
}

// Current returns the stamped variables, the Caddy pin and the Go runtime's version and platform.
func Current() Info {
	return Info{
		Version:   Version,
		Commit:    Commit,
		GoVersion: runtime.Version(),
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
		Caddy:     CompiledPins().Caddy,
	}
}
