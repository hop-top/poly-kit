package util

// To returns a pointer to v.
func To[T any](v T) *T { return &v }

// Or returns *p if non-nil, otherwise def.
func Or[T any](p *T, def T) T {
	if p != nil {
		return *p
	}
	return def
}

// Zero returns a pointer to the zero value of T.
func Zero[T any]() *T {
	var z T
	return &z
}
