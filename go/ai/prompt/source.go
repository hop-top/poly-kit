package prompt

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
)

// Source resolves a template reference. A missing template is an error
// matching ErrNotFound.
type Source interface {
	Load(ref Ref) (*Template, error)
}

// DirSource looks for `name@N.yaml` in each dir in order; the first dir
// holding the file wins and layers are not merged. A file that is present
// but broken is an error: it never falls through to a later dir.
func DirSource(dirs ...string) Source { return dirSource(dirs) }

type dirSource []string

func (d dirSource) Load(ref Ref) (*Template, error) {
	for _, dir := range d {
		data, err := os.ReadFile(filepath.Join(dir, ref.File())) //nolint:gosec // G304: caller-injected prompt dir, name validated by ParseRef
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read prompt %s: %w", ref, err)
		}
		return ParseTemplate(ref, data)
	}
	return nil, notFound(ref)
}

// FSSource reads `name@N.yaml` under root in fsys (root "" or "." is the
// top). Embedded prompts use it: FSSource(embeddedFS, "prompts").
func FSSource(fsys fs.FS, root string) Source { return fsSource{fsys, root} }

type fsSource struct {
	fsys fs.FS
	root string
}

func (s fsSource) Load(ref Ref) (*Template, error) {
	data, err := fs.ReadFile(s.fsys, path.Join(s.root, ref.File()))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, notFound(ref)
	}
	if err != nil {
		return nil, fmt.Errorf("read prompt %s: %w", ref, err)
	}
	return ParseTemplate(ref, data)
}

func notFound(ref Ref) error {
	return fmt.Errorf("%w: prompt %s: no %s in any source", ErrNotFound, ref, ref.File())
}
