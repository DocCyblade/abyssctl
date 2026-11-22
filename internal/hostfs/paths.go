// Package hostfs holds the paths value, every path the tool touches, and the one way to write a file.
package hostfs

import (
	"fmt"
	"path/filepath"
	"time"
)

// DefaultDataRoot is the Data root Bootstrap picks when no --data-root is given.
const DefaultDataRoot = "/srv/abyssctl"

// Paths is every path the tool touches on the Host. Its methods only join strings: no disk access, no name
// checks. A Data root method on a Paths without a Data root panics.
type Paths struct {
	prefix   string // put in front of every fixed path; "" for the real Host
	dataRoot string // "" until WithDataRoot
}

// System returns the real fixed paths, with no Data root yet.
func System() Paths { return Paths{} }

// Under returns a Paths with every fixed path moved under prefix, and no Data root yet. Tests only.
func Under(prefix string) Paths { return Paths{prefix: prefix} }

// WithDataRoot returns p with its Data root set to dir, which must be absolute and clean.
func (p Paths) WithDataRoot(dir string) (Paths, error) {
	if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir {
		return Paths{}, fmt.Errorf("data root %q is not an absolute, clean path", dir)
	}
	p.dataRoot = dir
	return p, nil
}

// HasDataRoot reports whether p has a Data root.
func (p Paths) HasDataRoot() bool { return p.dataRoot != "" }

func (p Paths) fixed(elem ...string) string {
	return filepath.Join(append([]string{p.prefix, "/"}, elem...)...)
}

func (p Paths) inDataRoot(elem ...string) string {
	if p.dataRoot == "" {
		panic("hostfs: a Data root path was asked for before WithDataRoot")
	}
	return filepath.Join(append([]string{p.dataRoot}, elem...)...)
}

// Fixed paths (not under the Data root).

func (p Paths) Binary() string                 { return p.fixed("usr", "local", "bin", "abyssctl") }
func (p Paths) SettingsDir() string            { return p.fixed("etc", "abyssctl") }
func (p Paths) SettingsFile() string           { return filepath.Join(p.SettingsDir(), "abyssctl.env") }
func (p Paths) LogDir() string                 { return p.fixed("var", "log", "abyssctl") }
func (p Paths) LogFile() string                { return filepath.Join(p.LogDir(), "abyssctl.log") }
func (p Paths) SystemdDir() string             { return p.fixed("etc", "systemd", "system") }
func (p Paths) SystemdUnit(file string) string { return filepath.Join(p.SystemdDir(), file) }
func (p Paths) MotdScript() string             { return p.fixed("etc", "update-motd.d", "90-abyssctl") }
func (p Paths) LogrotateRule() string          { return p.fixed("etc", "logrotate.d", "abyssctl") }

// Under the Data root.

func (p Paths) DataRoot() string               { return p.inDataRoot() }
func (p Paths) InstancesDir() string           { return p.inDataRoot("instances") }
func (p Paths) InstanceDir(name string) string { return p.inDataRoot("instances", name) }
func (p Paths) InstanceRecord(name string) string {
	return filepath.Join(p.InstanceDir(name), "instance.env")
}

func (p Paths) InstanceCompose(name string) string {
	return filepath.Join(p.InstanceDir(name), "compose.yaml")
}

func (p Paths) InstanceOutcomes(name string) string {
	return filepath.Join(p.InstanceDir(name), "outcomes.json")
}

func (p Paths) InstanceLock(name string) string { return filepath.Join(p.InstanceDir(name), ".lock") }

func (p Paths) InstanceData(name string) string { return filepath.Join(p.InstanceDir(name), "data") }

func (p Paths) InstancePreRestore(name string) string {
	return filepath.Join(p.InstanceDir(name), "data.pre-restore")
}

func (p Paths) UpgradeFailedLog(name string, at time.Time) string {
	return filepath.Join(p.InstanceDir(name), "upgrade-failed-"+Stamp(at)+".log")
}

func (p Paths) CreatingDir(name string) string {
	return p.inDataRoot("instances", "."+name+".creating")
}

func (p Paths) CreatingLock(name string) string { return filepath.Join(p.CreatingDir(name), ".lock") }

func (p Paths) RemovingDir(name string) string {
	return p.inDataRoot("instances", "."+name+".removing")
}
func (p Paths) BackupsDir() string                 { return p.inDataRoot("backups") }
func (p Paths) InstanceBackups(name string) string { return p.inDataRoot("backups", name) }
func (p Paths) RemovedBackupsDir() string          { return p.inDataRoot("backups", "removed") }

func (p Paths) RemovedBackups(name string, at time.Time) string {
	return p.inDataRoot("backups", "removed", name+"-"+Stamp(at))
}

func (p Paths) ProxyDir() string            { return p.inDataRoot("proxy") }
func (p Paths) Caddyfile() string           { return p.inDataRoot("proxy", "Caddyfile") }
func (p Paths) ProxyCompose() string        { return p.inDataRoot("proxy", "compose.yaml") }
func (p Paths) ProxyEnv() string            { return p.inDataRoot("proxy", "proxy.env") }
func (p Paths) SitesDir() string            { return p.inDataRoot("proxy", "sites") }
func (p Paths) SiteFile(name string) string { return p.inDataRoot("proxy", "sites", name+".caddy") }
func (p Paths) CertsDir() string            { return p.inDataRoot("proxy", "certs") }
func (p Paths) CertFile(id string) string   { return p.inDataRoot("proxy", "certs", id+".crt") }
func (p Paths) KeyFile(id string) string    { return p.inDataRoot("proxy", "certs", id+".key") }
func (p Paths) CaddyData() string           { return p.inDataRoot("proxy", "caddy-data") }
func (p Paths) CaddyConfig() string         { return p.inDataRoot("proxy", "caddy-config") }
func (p Paths) StateDir() string            { return p.inDataRoot("state") }
func (p Paths) HealthState() string         { return p.inDataRoot("state", "health.json") }
func (p Paths) MotdText() string            { return p.inDataRoot("state", "motd.txt") }
func (p Paths) HostLock() string            { return p.inDataRoot("state", "host.lock") }
func (p Paths) HealthLock() string          { return p.inDataRoot("state", "health.lock") }
