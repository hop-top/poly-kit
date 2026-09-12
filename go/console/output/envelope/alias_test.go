package envelope_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/output"
	"hop.top/kit/go/console/output/envelope"
)

// The envelope moved out of console/output into this leaf package, and
// console/output re-exports it. That re-export has to be a type ALIAS
// (type Error = envelope.Error), not a defined type: a defined type is a
// distinct type, so errors.As would stop matching an envelope built on
// one side against a target declared on the other, and every adopter
// doing `var e *output.Error; errors.As(err, &e)` would silently start
// missing envelopes produced by library code.
//
// These assertions fail to COMPILE if the alias is ever downgraded to a
// defined type, and fail at runtime if identity is otherwise broken.
// That compile-time failure is the point — it cannot be skipped.

// Assigning a value of one spelling to a variable of the other, with no
// conversion, compiles only while the two name the same type. Declared
// at package level in the var _ form the repo uses elsewhere for
// compile-time contracts: written as locals these trip staticcheck
// ST1023 ("omit type, it will be inferred"), and taking that advice
// would delete the assertion — an inferred type cannot catch the alias
// becoming a defined type.
var (
	_ *output.Error   = (*envelope.Error)(nil)
	_ *envelope.Error = (*output.Error)(nil)
)

// The same identity, observed at runtime: an envelope built through the
// leaf reads back through the parent's spelling as the same pointer.
// The compile-time half of this claim is the var _ pair above; here the
// types are left inferred so staticcheck stays quiet.
func TestAliasIdentityHoldsAcrossBothSpellings(t *testing.T) {
	fromLeaf := envelope.ConflictError("denied")

	var viaParent *output.Error
	require.True(t, errors.As(error(fromLeaf), &viaParent))
	assert.Same(t, fromLeaf, viaParent)

	assert.Equal(t, output.CodeConflict, viaParent.Code)
	assert.Equal(t, 4, viaParent.ExitCode)
}

// An envelope built through the leaf package must be matchable by a
// target declared as *output.Error, which is what adopter code and the
// console RunE middleware actually write.
func TestErrorsAsMatchesLeafEnvelopeThroughParentSpelling(t *testing.T) {
	err := error(envelope.ConflictError("denied by rule"))

	var viaParent *output.Error
	require.True(t, errors.As(err, &viaParent),
		"errors.As with an *output.Error target must match an envelope built "+
			"by the leaf package; if this fails the re-export is a defined "+
			"type rather than an alias")
	assert.Equal(t, output.CodeConflict, viaParent.Code)
	assert.Equal(t, 4, viaParent.ExitCode)
}

// ...and the reverse: an envelope built through the parent package must
// be matchable by a target declared as *envelope.Error.
func TestErrorsAsMatchesParentEnvelopeThroughLeafSpelling(t *testing.T) {
	err := error(output.NotFoundError("missing"))

	var viaLeaf *envelope.Error
	require.True(t, errors.As(err, &viaLeaf))
	assert.Equal(t, envelope.CodeNotFound, viaLeaf.Code)
	assert.Equal(t, 3, viaLeaf.ExitCode)
}

// The retained-error chain has to survive the move, since WrapError is
// how the middleware keeps adopter sentinels matchable across the
// envelope boundary.
func TestErrorsIsSurvivesTheReExport(t *testing.T) {
	sentinel := errors.New("sentinel")

	wrapped := error(output.WrapError(sentinel, output.CodeGeneric, 1))
	assert.True(t, errors.Is(wrapped, sentinel),
		"errors.Is must still reach the wrapped sentinel through the alias")

	var viaLeaf *envelope.Error
	require.True(t, errors.As(wrapped, &viaLeaf))
	assert.Equal(t, sentinel, viaLeaf.Unwrap())
}

// The re-exported constants must carry the same values, not merely the
// same names: adopters compare Code fields against them.
func TestReExportedConstantsAgree(t *testing.T) {
	assert.Equal(t, envelope.CodeUsage, output.CodeUsage)
	assert.Equal(t, envelope.CodeConflict, output.CodeConflict)
	assert.Equal(t, envelope.ExitRateLimited, output.ExitRateLimited)
	assert.Equal(t, envelope.ExitProvenanceMissing, output.ExitProvenanceMissing)
	assert.Equal(t, envelope.TransiencePermanent, output.TransiencePermanent)
	assert.Equal(t, output.TransiencePermanent, output.TransienceForCode(output.CodeConflict))
}
