package hostfs_test

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DocCyblade/abyssctl/internal/hostfs"
)

// at is a fixed time for the paths that carry a Stamp.
var at = time.Date(2026, 11, 17, 8, 0, 4, 0, time.UTC)

type pathCase struct {
	name string
	got  func(hostfs.Paths) string
	want string // the path with System() and the Data root /srv/abyssctl
}

// fixedCases are the Host layout table's paths outside the Data root, Docker's apt files left out.
var fixedCases = []pathCase{
	{"Binary", hostfs.Paths.Binary, "/usr/local/bin/abyssctl"},
	{"SettingsDir", hostfs.Paths.SettingsDir, "/etc/abyssctl"},
	{"SettingsFile", hostfs.Paths.SettingsFile, "/etc/abyssctl/abyssctl.env"},
	{"LogDir", hostfs.Paths.LogDir, "/var/log/abyssctl"},
	{"LogFile", hostfs.Paths.LogFile, "/var/log/abyssctl/abyssctl.log"},
	{"SystemdDir", hostfs.Paths.SystemdDir, "/etc/systemd/system"},
	{
		"SystemdUnit health.service", func(p hostfs.Paths) string { return p.SystemdUnit("abyssctl-health.service") },
		"/etc/systemd/system/abyssctl-health.service",
	},
	{
		"SystemdUnit health.timer", func(p hostfs.Paths) string { return p.SystemdUnit("abyssctl-health.timer") },
		"/etc/systemd/system/abyssctl-health.timer",
	},
	{
		"SystemdUnit backup.service", func(p hostfs.Paths) string { return p.SystemdUnit("abyssctl-backup.service") },
		"/etc/systemd/system/abyssctl-backup.service",
	},
	{
		"SystemdUnit backup.timer", func(p hostfs.Paths) string { return p.SystemdUnit("abyssctl-backup.timer") },
		"/etc/systemd/system/abyssctl-backup.timer",
	},
	{"MotdScript", hostfs.Paths.MotdScript, "/etc/update-motd.d/90-abyssctl"},
	{"LogrotateRule", hostfs.Paths.LogrotateRule, "/etc/logrotate.d/abyssctl"},
}

// dataRootCases are the Host layout table's paths under the Data root, the four lock files included.
var dataRootCases = []pathCase{
	{"DataRoot", hostfs.Paths.DataRoot, "/srv/abyssctl"},
	{"InstancesDir", hostfs.Paths.InstancesDir, "/srv/abyssctl/instances"},
	{"InstanceDir", name(hostfs.Paths.InstanceDir), "/srv/abyssctl/instances/budget"},
	{"InstanceRecord", name(hostfs.Paths.InstanceRecord), "/srv/abyssctl/instances/budget/instance.env"},
	{"InstanceCompose", name(hostfs.Paths.InstanceCompose), "/srv/abyssctl/instances/budget/compose.yaml"},
	{"InstanceOutcomes", name(hostfs.Paths.InstanceOutcomes), "/srv/abyssctl/instances/budget/outcomes.json"},
	{"InstanceLock", name(hostfs.Paths.InstanceLock), "/srv/abyssctl/instances/budget/.lock"},
	{"InstanceData", name(hostfs.Paths.InstanceData), "/srv/abyssctl/instances/budget/data"},
	{"InstancePreRestore", name(hostfs.Paths.InstancePreRestore), "/srv/abyssctl/instances/budget/data.pre-restore"},
	{
		"UpgradeFailedLog", func(p hostfs.Paths) string { return p.UpgradeFailedLog("budget", at) },
		"/srv/abyssctl/instances/budget/upgrade-failed-20261117T080004Z.log",
	},
	{"CreatingDir", name(hostfs.Paths.CreatingDir), "/srv/abyssctl/instances/.budget.creating"},
	{"CreatingLock", name(hostfs.Paths.CreatingLock), "/srv/abyssctl/instances/.budget.creating/.lock"},
	{"RemovingDir", name(hostfs.Paths.RemovingDir), "/srv/abyssctl/instances/.budget.removing"},
	{"BackupsDir", hostfs.Paths.BackupsDir, "/srv/abyssctl/backups"},
	{"InstanceBackups", name(hostfs.Paths.InstanceBackups), "/srv/abyssctl/backups/budget"},
	{"RemovedBackupsDir", hostfs.Paths.RemovedBackupsDir, "/srv/abyssctl/backups/removed"},
	{
		"RemovedBackups", func(p hostfs.Paths) string { return p.RemovedBackups("budget", at) },
		"/srv/abyssctl/backups/removed/budget-20261117T080004Z",
	},
	{"ProxyDir", hostfs.Paths.ProxyDir, "/srv/abyssctl/proxy"},
	{"Caddyfile", hostfs.Paths.Caddyfile, "/srv/abyssctl/proxy/Caddyfile"},
	{"ProxyCompose", hostfs.Paths.ProxyCompose, "/srv/abyssctl/proxy/compose.yaml"},
	{"ProxyEnv", hostfs.Paths.ProxyEnv, "/srv/abyssctl/proxy/proxy.env"},
	{"SitesDir", hostfs.Paths.SitesDir, "/srv/abyssctl/proxy/sites"},
	{"SiteFile", name(hostfs.Paths.SiteFile), "/srv/abyssctl/proxy/sites/budget.caddy"},
	{"CertsDir", hostfs.Paths.CertsDir, "/srv/abyssctl/proxy/certs"},
	{
		"CertFile", func(p hostfs.Paths) string { return p.CertFile("0123456789ab") },
		"/srv/abyssctl/proxy/certs/0123456789ab.crt",
	},
	{
		"KeyFile", func(p hostfs.Paths) string { return p.KeyFile("0123456789ab") },
		"/srv/abyssctl/proxy/certs/0123456789ab.key",
	},
	{"CaddyData", hostfs.Paths.CaddyData, "/srv/abyssctl/proxy/caddy-data"},
	{"CaddyConfig", hostfs.Paths.CaddyConfig, "/srv/abyssctl/proxy/caddy-config"},
	{"StateDir", hostfs.Paths.StateDir, "/srv/abyssctl/state"},
	{"HealthState", hostfs.Paths.HealthState, "/srv/abyssctl/state/health.json"},
	{"MotdText", hostfs.Paths.MotdText, "/srv/abyssctl/state/motd.txt"},
	{"HostLock", hostfs.Paths.HostLock, "/srv/abyssctl/state/host.lock"},
	{"HealthLock", hostfs.Paths.HealthLock, "/srv/abyssctl/state/health.lock"},
}

func name(m func(hostfs.Paths, string) string) func(hostfs.Paths) string {
	return func(p hostfs.Paths) string { return m(p, "budget") }
}

func withDataRoot(t *testing.T, p hostfs.Paths, dir string) hostfs.Paths {
	t.Helper()
	q, err := p.WithDataRoot(dir)
	if err != nil {
		t.Fatalf("WithDataRoot(%q): %v", dir, err)
	}
	return q
}

func TestPaths_MatchHostLayout(t *testing.T) {
	p := withDataRoot(t, hostfs.System(), hostfs.DefaultDataRoot)
	if !p.HasDataRoot() {
		t.Error("HasDataRoot() = false after WithDataRoot")
	}
	for _, c := range append(append([]pathCase{}, fixedCases...), dataRootCases...) {
		t.Run(c.name, func(t *testing.T) {
			if got := c.got(p); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestWithDataRoot(t *testing.T) {
	p := withDataRoot(t, hostfs.System(), "/data/abyss")
	for _, c := range fixedCases {
		t.Run("fixed/"+c.name, func(t *testing.T) {
			if got := c.got(p); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
	for _, c := range dataRootCases {
		t.Run("data_root/"+c.name, func(t *testing.T) {
			want := "/data/abyss" + strings.TrimPrefix(c.want, "/srv/abyssctl")
			if got := c.got(p); got != want {
				t.Errorf("got %q, want %q", got, want)
			}
		})
	}

	for _, dir := range []string{
		"", "srv/abyssctl", "./srv", "/srv/abyssctl/", "/srv//abyssctl", "/srv/./abyssctl",
		"/srv/x/../abyssctl",
	} {
		t.Run("rejects/"+dir, func(t *testing.T) {
			q, err := hostfs.System().WithDataRoot(dir)
			if err == nil {
				t.Fatalf("WithDataRoot(%q) = nil error", dir)
			}
			if q.HasDataRoot() {
				t.Errorf("WithDataRoot(%q) returned a Paths with a Data root", dir)
			}
		})
	}
}

func TestUnder_EveryPathUnderPrefix(t *testing.T) {
	prefix := t.TempDir()
	p := hostfs.Under(prefix)
	if p.HasDataRoot() {
		t.Error("Under(prefix).HasDataRoot() = true, want false")
	}
	for _, c := range fixedCases {
		t.Run(c.name, func(t *testing.T) {
			want := filepath.Join(prefix, c.want)
			if got := c.got(p); got != want {
				t.Errorf("got %q, want %q", got, want)
			}
		})
	}
}

func TestPaths_NoDataRootPanics(t *testing.T) {
	for _, p := range []struct {
		name  string
		paths hostfs.Paths
	}{{"System", hostfs.System()}, {"Under", hostfs.Under(t.TempDir())}} {
		if p.paths.HasDataRoot() {
			t.Errorf("%s().HasDataRoot() = true, want false", p.name)
		}
		for _, c := range dataRootCases {
			t.Run(p.name+"/"+c.name, func(t *testing.T) {
				defer func() {
					if recover() == nil {
						t.Errorf("%s without a Data root did not panic", c.name)
					}
				}()
				_ = c.got(p.paths)
			})
		}
	}
}
