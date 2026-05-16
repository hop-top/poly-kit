package toolspec

// Source resolves a tool name into its specification.
type Source interface {
	Resolve(tool string) (*ToolSpec, error)
}

// SourceFunc adapts a plain function to the Source interface.
type SourceFunc func(string) (*ToolSpec, error)

// Resolve implements Source.
func (f SourceFunc) Resolve(tool string) (*ToolSpec, error) {
	return f(tool)
}

// ChainSources returns a Source that queries each source in order
// and merges results. Earlier sources take precedence: later sources
// only fill fields that are still empty.
func ChainSources(sources ...Source) Source {
	return SourceFunc(func(tool string) (*ToolSpec, error) {
		var acc *ToolSpec
		for _, src := range sources {
			spec, err := src.Resolve(tool)
			if err != nil {
				return nil, err
			}
			if spec == nil {
				continue
			}
			if acc == nil {
				acc = spec
				continue
			}
			acc = Merge(acc, spec)
		}
		if acc == nil {
			return &ToolSpec{Name: tool}, nil
		}
		return acc, nil
	})
}
