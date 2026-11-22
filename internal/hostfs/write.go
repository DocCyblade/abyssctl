package hostfs

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// tempTries bounds the O_EXCL retries when a random temp name is already taken.
const tempTries = 10

// errDone is returned by a Pending used after Commit or Abort.
var errDone = errors.New("hostfs: write already committed or aborted")

// Pending is a file being written by Create. Its bytes go to a temp file in the target's own folder, and Commit
// puts it over the target in one step.
type Pending struct {
	target string
	perm   fs.FileMode
	file   *os.File // nil after Commit or Abort
	tmp    string
}

// WriteFile writes data to path in one step: a reader sees the old file or the new one with exactly perm, never
// part of one. It does not create folders or change owners. On any error path is untouched.
func WriteFile(path string, data []byte, perm fs.FileMode) error {
	p, err := Create(path, perm)
	if err != nil {
		return err
	}
	if _, err := p.Write(data); err != nil {
		p.Abort()
		return err
	}
	return p.Commit()
}

// Create starts a write to path through a temp file named <base>.<8 random hex>.partial in path's own folder.
// The caller ends it with Commit, or with Abort, which is safe to defer. It does not create folders.
func Create(path string, perm fs.FileMode) (*Pending, error) {
	dir, base := filepath.Split(path)
	for range tempTries {
		var b [4]byte
		if _, err := rand.Read(b[:]); err != nil {
			return nil, fmt.Errorf("write %s: %w", path, err)
		}
		tmp := filepath.Join(dir, base+"."+hex.EncodeToString(b[:])+".partial")
		f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("write %s: %w", path, err)
		}
		return &Pending{target: path, perm: perm, file: f, tmp: tmp}, nil
	}
	return nil, fmt.Errorf("write %s: no free temp name after %d tries", path, tempTries)
}

// Write appends b to the temp file.
func (p *Pending) Write(b []byte) (int, error) {
	if p.file == nil {
		return 0, errDone
	}
	return p.file.Write(b)
}

// Path returns the temp file's path, so the writer can read it back before Commit.
func (p *Pending) Path() string { return p.tmp }

// Commit fsyncs the temp file, sets its mode to exactly perm, renames it over the target and fsyncs the folder.
// On an error before the rename the temp file is removed and the target is untouched.
func (p *Pending) Commit() error {
	if p.file == nil {
		return errDone
	}
	if err := p.finish(); err != nil {
		p.Abort()
		return fmt.Errorf("write %s: %w", p.target, err)
	}
	p.file = nil
	if err := syncDir(filepath.Dir(p.target)); err != nil {
		return fmt.Errorf("write %s: sync folder: %w", p.target, err)
	}
	return nil
}

func (p *Pending) finish() error {
	if err := p.file.Sync(); err != nil {
		return err
	}
	// Chmod, not the create mode, so the umask never loosens or tightens perm.
	if err := p.file.Chmod(p.perm); err != nil {
		return err
	}
	if err := p.file.Close(); err != nil {
		return err
	}
	return os.Rename(p.tmp, p.target)
}

// Abort removes the temp file. It is safe after Commit and safe to call twice.
func (p *Pending) Abort() {
	if p.file == nil {
		return
	}
	_ = p.file.Close()
	_ = os.Remove(p.tmp)
	p.file = nil
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	err = d.Sync()
	if cerr := d.Close(); err == nil {
		err = cerr
	}
	return err
}

// EnsureDir makes path a folder with exactly perm: it creates missing parents at 0700, creates the folder if it is
// missing and sets its mode whatever the umask. It does not change owners.
func EnsureDir(path string, perm fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("make folder %s: %w", path, err)
	}
	if err := os.Mkdir(path, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("make folder %s: %w", path, err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("make folder %s: %w", path, err)
	}
	if !fi.IsDir() {
		return fmt.Errorf("make folder %s: not a folder", path)
	}
	if err := os.Chmod(path, perm); err != nil {
		return fmt.Errorf("make folder %s: %w", path, err)
	}
	return nil
}
