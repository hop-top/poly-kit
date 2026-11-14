package file

import (
	"os"
	"path/filepath"
	"runtime"
)

// syncDir fsyncs a directory so a rename into it is durable. Test seam.
var syncDir = func(dir string) error {
	if runtime.GOOS == "windows" {
		return nil // directory handles cannot be fsynced on windows
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	if err := d.Sync(); err != nil {
		_ = d.Close()
		return err
	}
	return d.Close()
}

// writeAtomic replaces path with data via a temp file in the same
// directory: write, fsync, chmod, rename, fsync of the directory. A failure
// before the rename removes the temp file and leaves the previous content
// untouched. A symlink at path is resolved and the write lands next to its
// target, so the link survives. The mode of an existing file is preserved;
// a new file gets 0600.
func writeAtomic(path string, data []byte) (err error) {
	if li, lerr := os.Lstat(path); lerr == nil && li.Mode()&os.ModeSymlink != 0 {
		if path, err = filepath.EvalSymlinks(path); err != nil {
			return err
		}
	}
	mode := os.FileMode(0600)
	if info, serr := os.Stat(path); serr == nil {
		mode = info.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(name)
		}
	}()
	if _, err = tmp.Write(data); err != nil {
		return err
	}
	if err = tmp.Chmod(mode); err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = os.Rename(name, path); err != nil {
		return err
	}
	return syncDir(filepath.Dir(path))
}
