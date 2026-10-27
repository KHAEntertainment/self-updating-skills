// Atomic file writing (architecture D10): temp file in the target's
// directory, target's permissions, fsync, rename, fsync the directory.
// Within one apply the sidecar is written first, then SKILL.md: the sidecar
// is the source and SKILL.md is derived from it, so a crash between the two
// renames is repaired by the next render (check reports DRIFT meanwhile).
package render

import (
	"fmt"
	"os"
	"path/filepath"
)

// renameFn is a seam so tests can simulate a failed second rename.
var renameFn = os.Rename

// WriteFileAtomic writes data to path atomically: the temp file is created
// in the same directory with the target's mode (0644 for a new file),
// written, fsynced, renamed over the target, and the directory fsynced.
func WriteFileAtomic(path string, data []byte) error {
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode()
	} else if !os.IsNotExist(err) {
		return err
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() {
		tmp.Close()
		os.Remove(tmpName)
	}
	if err := tmp.Chmod(mode); err != nil {
		cleanup()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := renameFn(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return syncDir(dir)
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// SkillName is the skill entry file name.
const SkillName = "SKILL.md"

// Apply writes the sidecar first, then SKILL.md, each only when the bytes
// differ (skip unchanged). It returns which files changed.
func Apply(dir string, sc *Sidecar, skillOut []byte) (skillChanged, sidecarChanged bool, err error) {
	sidecarPath := filepath.Join(dir, SidecarName)
	wantSidecar := sc.Canonical()
	cur, err := os.ReadFile(sidecarPath)
	if err != nil && !os.IsNotExist(err) {
		return false, false, fmt.Errorf("read %s: %w", sidecarPath, err)
	}
	if string(cur) != string(wantSidecar) {
		if err := WriteFileAtomic(sidecarPath, wantSidecar); err != nil {
			return false, false, fmt.Errorf("write %s: %w", sidecarPath, err)
		}
		sidecarChanged = true
	}
	skillPath := filepath.Join(dir, SkillName)
	raw, err := os.ReadFile(skillPath)
	if err != nil {
		return false, sidecarChanged, fmt.Errorf("read %s: %w", skillPath, err)
	}
	if string(raw) != string(skillOut) {
		if err := WriteFileAtomic(skillPath, skillOut); err != nil {
			return false, sidecarChanged, fmt.Errorf("write %s: %w", skillPath, err)
		}
		skillChanged = true
	}
	return skillChanged, sidecarChanged, nil
}
