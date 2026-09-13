package client

import (
	"errors"
	"testing"

	"hop.top/kit/go/console/output"
)

// TestSentinelExitTable pins every client sentinel to the reconciled
// kit-wide exit taxonomy: usage-class rejections on 2, auth on 5,
// transient service blips on 6, rate-limiting on kit's band code 64,
// and the two grade verdicts on the conformance band (68/69) so they
// no longer collide with the shared usage slot.
func TestSentinelExitTable(t *testing.T) {
	cases := []struct {
		name           string
		err            error
		wantCode       string
		wantExit       int
		wantTransience string
	}{
		{"service unavailable", ErrServiceUnavailable, CodeServiceUnavailable, output.ExitTransient, output.TransienceTransient},
		{"auth failed", ErrServiceAuthFailed, CodeServiceAuthFailed, 5, output.TransiencePermanent},
		{"service usage", ErrServiceUsage, CodeServiceUsage, 2, output.TransiencePermanent},
		{"cassette pack", ErrCassettePack, CodeCassettePack, 1, output.TransiencePermanent},
		{"cassette too large", ErrCassetteTooLarge, CodeCassetteTooLarge, 2, output.TransiencePermanent},
		{"manifest parse", ErrManifestParse, CodeManifestParse, 2, output.TransiencePermanent},
		{"grade fail", ErrGradeFail, CodeGradeFail, ExitGradeFail, output.TransiencePermanent},
		{"grade ungradable", ErrGradeUngradable, CodeGradeUngradable, ExitGradeUngradable, output.TransiencePermanent},
		{"rate limited", ErrRateLimited, CodeRateLimited, output.ExitRateLimited, output.TransienceTransient},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			conv, ok := c.err.(interface{ AsCLIError() *output.Error })
			if !ok {
				t.Fatal("sentinel must implement AsCLIError")
			}
			env := conv.AsCLIError()
			if env.Code != c.wantCode {
				t.Errorf("Code = %q, want %q", env.Code, c.wantCode)
			}
			if env.ExitCode != c.wantExit {
				t.Errorf("ExitCode = %d, want %d", env.ExitCode, c.wantExit)
			}
			if env.Transience != c.wantTransience {
				t.Errorf("Transience = %q, want %q", env.Transience, c.wantTransience)
			}
		})
	}
}

// TestBandConstants locks the grade-verdict band allocation next to
// kit's existing 64/65 and the conformance tree's 66/67.
func TestBandConstants(t *testing.T) {
	if ExitGradeFail != 68 {
		t.Errorf("ExitGradeFail = %d, want 68", ExitGradeFail)
	}
	if ExitGradeUngradable != 69 {
		t.Errorf("ExitGradeUngradable = %d, want 69", ExitGradeUngradable)
	}
}

// TestBandConstantsMatchKitAllocation pins this package's band slots
// against kit's record of the allocation.
//
// The band is allocated across three trees, so the constant being 68
// here and kit's list saying something else is a collision waiting to
// happen that neither side can see alone. This package declares the
// numbers; kit records who owns what; the two must agree.
func TestBandConstantsMatchKitAllocation(t *testing.T) {
	local := map[string]int{
		CodeGradeFail:       ExitGradeFail,
		CodeGradeUngradable: ExitGradeUngradable,
	}
	matched := 0
	for _, slot := range output.ExtensionBand() {
		want, mine := local[slot.Class]
		if !mine {
			continue
		}
		matched++
		if slot.Exit != want {
			t.Errorf("kit allocates %s to exit %d, this package declares %d",
				slot.Class, slot.Exit, want)
		}
		if slot.Owner != "hop.top/kit/go/conformance/client" {
			t.Errorf("kit records %s as owned by %s, but it is declared here",
				slot.Class, slot.Owner)
		}
	}
	if matched != len(local) {
		t.Errorf("kit's band lists %d of this package's %d slots; a slot declared "+
			"here and missing from envelope.ExtensionBand can be handed to "+
			"another feature", matched, len(local))
	}
}

// TestSharedSlotsUseKitsConstants guards against this package
// re-deriving a number kit already names. Where a sentinel sits on a
// shared class's slot, the value must be kit's constant, not a literal
// that agrees with it today.
func TestSharedSlotsUseKitsConstants(t *testing.T) {
	for _, c := range []struct {
		name string
		err  *sentinel
		want int
	}{
		{"auth failed", ErrServiceAuthFailed, output.ExitUnauthorized},
		{"service usage", ErrServiceUsage, output.ExitUsage},
		{"cassette pack", ErrCassettePack, output.ExitGeneric},
		{"cassette too large", ErrCassetteTooLarge, output.ExitUsage},
		{"manifest parse", ErrManifestParse, output.ExitUsage},
		{"service unavailable", ErrServiceUnreachable, output.ExitTransient},
		{"rate limited", ErrRateLimited, output.ExitRateLimited},
	} {
		if c.err.exit != c.want {
			t.Errorf("%s: exit = %d, want kit's %d", c.name, c.err.exit, c.want)
		}
	}
}

// TestWrappedSentinelKeepsExitAndTransience ensures the constructor-
// wrapped form renders the same envelope class as its base identity.
func TestWrappedSentinelKeepsExitAndTransience(t *testing.T) {
	err := ServiceUnavailableError("post /v1/grade", "connection refused", "check the svc URL")
	if !errors.Is(err, ErrServiceUnavailable) {
		t.Fatal("wrapped sentinel lost identity")
	}
	env := err.(interface{ AsCLIError() *output.Error }).AsCLIError()
	if env.ExitCode != output.ExitTransient {
		t.Errorf("ExitCode = %d, want %d", env.ExitCode, output.ExitTransient)
	}
	if env.Transience != output.TransienceTransient {
		t.Errorf("Transience = %q, want %q", env.Transience, output.TransienceTransient)
	}
}
