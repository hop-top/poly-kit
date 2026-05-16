package policy

import "github.com/failsafe-go/failsafe-go"

// CountBuilder configures a Count threshold policy. Identical shape
// to VolumeBuilder; kept separate so call-sites read clearly
// (WithMaxOps vs WithMaxBytes) and so future divergence is cheap.
type CountBuilder[R any] interface {
	WithMaxOps(n int64) CountBuilder[R]
	WithReader(r Reader) CountBuilder[R]
	OnExceeded(fn func(n int64)) CountBuilder[R]
	Build() failsafe.Policy[R]
}

// NewCount returns a fresh CountBuilder.
func NewCount[R any]() CountBuilder[R] {
	return &countBuilder[R]{}
}

type countBuilder[R any] struct {
	max        int64
	read       Reader
	onExceeded func(int64)
}

func (b *countBuilder[R]) WithMaxOps(n int64) CountBuilder[R] {
	b.max = n
	return b
}

func (b *countBuilder[R]) WithReader(r Reader) CountBuilder[R] {
	b.read = r
	return b
}

func (b *countBuilder[R]) OnExceeded(fn func(n int64)) CountBuilder[R] {
	b.onExceeded = fn
	return b
}

func (b *countBuilder[R]) Build() failsafe.Policy[R] {
	return build[R](b.max, b.read, b.onExceeded)
}
