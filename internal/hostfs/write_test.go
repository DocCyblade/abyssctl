package hostfs_test

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
	"testing"

	"github.com/DocCyblade/abyssctl/internal/hostfs"
)

// withUmask sets the process umask for one test. The umask is process-wide, so these tests never run in parallel.
func withUmask(t *testing.T, mask int) {
	t.Helper()
	old := syscall.Umask(mask)
	t.Cleanup(func() { syscall.Umask(old) })
}

func mode(t *testing.T, path string) fs.FileMode {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return fi.Mode().Perm()
}

func content(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}

// onlyEntries fails unless dir holds exactly the named entries, so a left-over .partial file fails it.
func onlyEntries(t *testing.T, dir string, want ...string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir %s: %v", dir, err)
	}
	var got []string
	for _, e := range entries {
		got = append(got, e.Name())
	}
	if len(got) != len(want) {
		t.Fatalf("entries in %s = %q, want %q", dir, got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("entries in %s = %q, want %q", dir, got, want)
		}
	}
}

func TestWriteFile_ExactMode(t *testing.T) {
	cases := []struct {
		name  string
		umask int
		perm  fs.FileMode
	}{
		{"strict umask keeps a looser perm", 0o077, 0o644},
		{"strict umask, group read", 0o077, 0o640},
		{"open umask keeps a strict perm", 0o000, 0o600},
		{"common umask, owner only", 0o022, 0o600},
		{"common umask, world writable asked for", 0o022, 0o666},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withUmask(t, tc.umask)
			dir := t.TempDir()
			path := filepath.Join(dir, "settings.env")
			if err := hostfs.WriteFile(path, []byte("A=1\n"), tc.perm); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}
			if got := mode(t, path); got != tc.perm {
				t.Errorf("mode = %o, want %o", got, tc.perm)
			}
			if got := content(t, path); string(got) != "A=1\n" {
				t.Errorf("content = %q", got)
			}
			onlyEntries(t, dir, "settings.env")
		})
	}
}

func TestWriteFile_Replaces(t *testing.T) {
	withUmask(t, 0o022)
	dir := t.TempDir()
	path := filepath.Join(dir, "compose.yaml")
	if err := os.WriteFile(path, []byte("old content, longer than the new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	if err := hostfs.WriteFile(path, []byte("new\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if got := content(t, path); string(got) != "new\n" {
		t.Errorf("content = %q, want %q", got, "new\n")
	}
	if got := mode(t, path); got != 0o600 {
		t.Errorf("mode = %o, want 600", got)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// Replaced in one step: a rename puts a new file in place rather than rewriting the old one.
	if os.SameFile(before, after) {
		t.Errorf("file was rewritten in place, want it replaced by a rename")
	}
	onlyEntries(t, dir, "compose.yaml")
}

func TestCreate_CommitAndAbort(t *testing.T) {
	t.Run("commit writes the file with exactly perm", func(t *testing.T) {
		withUmask(t, 0o077)
		dir := t.TempDir()
		path := filepath.Join(dir, "backup.tar.gz")
		p, err := hostfs.Create(path, 0o640)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		for _, chunk := range []string{"part one, ", "part two"} {
			if _, err := p.Write([]byte(chunk)); err != nil {
				t.Fatalf("Write: %v", err)
			}
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("target exists before Commit (err %v)", err)
		}
		if err := p.Commit(); err != nil {
			t.Fatalf("Commit: %v", err)
		}
		if got := content(t, path); string(got) != "part one, part two" {
			t.Errorf("content = %q", got)
		}
		if got := mode(t, path); got != 0o640 {
			t.Errorf("mode = %o, want 640", got)
		}
		onlyEntries(t, dir, "backup.tar.gz")

		// Abort after Commit is safe, twice, and keeps the committed file.
		p.Abort()
		p.Abort()
		if got := content(t, path); string(got) != "part one, part two" {
			t.Errorf("content after Abort = %q", got)
		}
		onlyEntries(t, dir, "backup.tar.gz")
	})

	t.Run("abort removes the temp file and leaves the old file", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "cert.pem")
		if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
			t.Fatal(err)
		}
		p, err := hostfs.Create(path, 0o644)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if _, err := p.Write([]byte("new")); err != nil {
			t.Fatalf("Write: %v", err)
		}
		p.Abort()
		p.Abort()
		if got := content(t, path); string(got) != "old" {
			t.Errorf("content = %q, want old", got)
		}
		if got := mode(t, path); got != 0o600 {
			t.Errorf("mode = %o, want 600", got)
		}
		onlyEntries(t, dir, "cert.pem")
		if err := p.Commit(); err == nil {
			t.Errorf("Commit after Abort succeeded, want an error")
		}
		if _, err := p.Write([]byte("x")); err == nil {
			t.Errorf("Write after Abort succeeded, want an error")
		}
		onlyEntries(t, dir, "cert.pem")
	})
}

var tempName = regexp.MustCompile(`^backup\.tar\.gz\.[0-9a-f]{8}\.partial$`)

func TestCreate_TempName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "backup.tar.gz")
	seen := map[string]bool{}
	for range 3 {
		p, err := hostfs.Create(path, 0o600)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		tmp := p.Path()
		if filepath.Dir(tmp) != dir {
			t.Errorf("temp file in %s, want %s", filepath.Dir(tmp), dir)
		}
		if !tempName.MatchString(filepath.Base(tmp)) {
			t.Errorf("temp name %q, want <base>.<8 hex>.partial", filepath.Base(tmp))
		}
		if seen[tmp] {
			t.Errorf("temp name %q repeated", tmp)
		}
		seen[tmp] = true

		if _, err := p.Write([]byte("archive")); err != nil {
			t.Fatalf("Write: %v", err)
		}
		// The writer can read its temp file back through Path() before Commit.
		if got := content(t, p.Path()); !bytes.Equal(got, []byte("archive")) {
			t.Errorf("temp content = %q, want archive", got)
		}
		if p.Path() != tmp {
			t.Errorf("Path() changed before Commit: %q, then %q", tmp, p.Path())
		}
		if err := p.Commit(); err != nil {
			t.Fatalf("Commit: %v", err)
		}
	}
	onlyEntries(t, dir, "backup.tar.gz")
}

func TestWriteFile_ErrorLeavesOldFile(t *testing.T) {
	cases := []struct {
		name string
		// setup makes the write at path fail and returns the folder entries expected afterwards.
		setup func(t *testing.T, dir, path string)
		want  []string
	}{
		{
			name: "rename fails over a non-empty folder",
			setup: func(t *testing.T, dir, path string) {
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(path, "keep"), []byte("old"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			want: []string{"target"},
		},
		{
			name: "folder not writable",
			setup: func(t *testing.T, dir, path string) {
				if os.Geteuid() == 0 {
					t.Skip("root ignores folder modes")
				}
				if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(dir, 0o500); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
			},
			want: []string{"target"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "folder")
			if err := os.Mkdir(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "target")
			tc.setup(t, dir, path)
			old := snapshot(t, path)

			if err := hostfs.WriteFile(path, []byte("new"), 0o644); err == nil {
				t.Fatalf("WriteFile succeeded, want an error")
			}

			if got := snapshot(t, path); got != old {
				t.Errorf("target changed: %q, want %q", got, old)
			}
			onlyEntries(t, dir, tc.want...)
		})
	}
}

// snapshot describes what is at path: a file's mode and content, or a folder's mode and entry names.
func snapshot(t *testing.T, path string) string {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if !fi.IsDir() {
		return fi.Mode().String() + " " + string(content(t, path))
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatal(err)
	}
	s := fi.Mode().String()
	for _, e := range entries {
		s += " " + e.Name()
	}
	return s
}

func TestWriteFile_MissingFolderFails(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "missing", "settings.env")
	if err := hostfs.WriteFile(path, []byte("A=1\n"), 0o600); err == nil {
		t.Errorf("WriteFile into a missing folder succeeded, want an error")
	}
	if _, err := hostfs.Create(path, 0o600); err == nil {
		t.Errorf("Create into a missing folder succeeded, want an error")
	}
	onlyEntries(t, root)
}

func TestEnsureDir(t *testing.T) {
	cases := []struct {
		name     string
		umask    int
		existing fs.FileMode // 0: the folder does not exist yet
		perm     fs.FileMode
	}{
		{"creates a missing folder, strict umask", 0o077, 0, 0o755},
		{"creates a missing folder, open umask", 0o000, 0, 0o700},
		{"creates a missing folder, common umask", 0o022, 0, 0o770},
		{"tightens an existing folder", 0o022, 0o755, 0o700},
		{"loosens an existing folder", 0o077, 0o700, 0o755},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withUmask(t, tc.umask)
			root := t.TempDir()
			path := filepath.Join(root, "abyss", "instances", "budget")
			if tc.existing != 0 {
				if err := os.MkdirAll(path, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(path, tc.existing); err != nil {
					t.Fatal(err)
				}
			}

			if err := hostfs.EnsureDir(path, tc.perm); err != nil {
				t.Fatalf("EnsureDir: %v", err)
			}

			fi, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if !fi.IsDir() {
				t.Fatalf("%s is not a folder", path)
			}
			if got := fi.Mode().Perm(); got != tc.perm {
				t.Errorf("mode = %o, want %o", got, tc.perm)
			}
			if tc.existing == 0 {
				for _, parent := range []string{filepath.Join(root, "abyss"), filepath.Join(root, "abyss", "instances")} {
					if got := mode(t, parent); got != 0o700 {
						t.Errorf("parent %s mode = %o, want 700", parent, got)
					}
				}
			}
			// Calling it again is a no-op.
			if err := hostfs.EnsureDir(path, tc.perm); err != nil {
				t.Fatalf("EnsureDir again: %v", err)
			}
			if got := mode(t, path); got != tc.perm {
				t.Errorf("mode after second call = %o, want %o", got, tc.perm)
			}
		})
	}
}
