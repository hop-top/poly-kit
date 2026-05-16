package policy

import "github.com/failsafe-go/failsafe-go"

// VolumeBuilder configures a Volume threshold policy.
type VolumeBuilder[R any] interface {
	// WithMaxBytes sets the cumulative byte cap. Required.
	WithMaxBytes(n int64) VolumeBuilder[R]

	// WithReader supplies the closure the policy uses to read the
	// current cumulative byte counter. Required.
	WithReader(r Reader) VolumeBuilder[R]

	// OnExceeded registers a hook called once when the counter
	// crosses the cap on a PreExecute check. The hook receives the
	// observed value (>= max). Optional.
	OnExceeded(fn func(n int64)) VolumeBuilder[R]

	// Build returns a failsafe.Policy ready for composition.
	Build() failsafe.Policy[R]
}

// NewVolume returns a fresh VolumeBuilder.
func NewVolume[R any]() VolumeBuilder[R] {
	return &volumeBuilder[R]{}
}

type volumeBuilder[R any] struct {
	max        int64
	read       Reader
	onExceeded func(int64)
}

func (b *volumeBuilder[R]) WithMaxBytes(n int64) VolumeBuilder[R] {
	b.max = n
	return b
}

func (b *volumeBuilder[R]) WithReader(r Reader) VolumeBuilder[R] {
	b.read = r
	return b
}

func (b *volumeBuilder[R]) OnExceeded(fn func(n int64)) VolumeBuilder[R] {
	b.onExceeded = fn
	return b
}

func (b *volumeBuilder[R]) Build() failsafe.Policy[R] {
	return build[R](b.max, b.read, b.onExceeded)
}
