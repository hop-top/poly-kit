package cli

import (
	"errors"
	"testing"

	"hop.top/kit/go/console/output"
)

// The middleware fills Transience on envelopes that reach it without a
// class, using the code-derived default, so every rendered structured
// error carries transient|permanent|unknown even when the adopter's
// AsCLIError passthrough predates the field.
func TestToCLIErrorDefaultsTransience(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			"bare error wraps as GENERIC/unknown",
			errors.New("boom"),
			output.TransienceUnknown,
		},
		{
			"passthrough with standard code gets code default",
			&transienceStub{env: &output.Error{
				Code: output.CodeConflict, Message: "dup", ExitCode: 4,
			}},
			output.TransiencePermanent,
		},
		{
			"passthrough with adopter code stays unknown",
			&transienceStub{env: &output.Error{
				Code: "ADOPTER_SPECIFIC", Message: "m", ExitCode: 9,
			}},
			output.TransienceUnknown,
		},
		{
			"adopter-set class is never overridden",
			&transienceStub{env: &output.Error{
				Code: output.CodeConflict, Message: "dup", ExitCode: 4,
				Transience: output.TransienceTransient,
			}},
			output.TransienceTransient,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := toCLIError(tc.err)
			if got == nil {
				t.Fatal("toCLIError returned nil")
			}
			if got.Transience != tc.want {
				t.Errorf("Transience = %q, want %q", got.Transience, tc.want)
			}
		})
	}
}

// Defaulting must copy, not mutate: adopters commonly return a shared
// package-level envelope from AsCLIError.
func TestToCLIErrorTransienceDoesNotMutateSharedEnvelope(t *testing.T) {
	shared := &output.Error{Code: output.CodeConflict, Message: "dup", ExitCode: 4}
	_ = toCLIError(&transienceStub{env: shared})
	if shared.Transience != "" {
		t.Errorf("shared envelope mutated: Transience = %q, want empty", shared.Transience)
	}
}

type transienceStub struct{ env *output.Error }

func (s *transienceStub) Error() string             { return s.env.Message }
func (s *transienceStub) AsCLIError() *output.Error { return s.env }
