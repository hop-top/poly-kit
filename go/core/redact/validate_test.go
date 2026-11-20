package redact_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"hop.top/kit/go/core/redact"
)

// cardRedactor returns a private redactor on the embedded policy: Default() is a
// singleton other tests switch to another strategy.
func cardRedactor(t *testing.T) *redact.Redactor {
	t.Helper()
	r, err := redact.NewEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	r, err = r.SetReplacement(redact.Mask)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// A digit run inside a larger token (UUID, glued alphanumerics) is an
// identifier, not a card, whether or not it passes Luhn.
func TestCreditCard_NotMatchedInsideToken(t *testing.T) {
	r := cardRedactor(t)
	for _, in := range []string{
		// UUID, Luhn-valid 4+12 digit tail: only the UUID check saves it.
		"bffab4c3-9d88-43ab-4111-111111111111",
		"evt-bffab4c3-9d88-43ab-4111-111111111111",
		"BFFAB4C3-9D88-43A9-4111-111111111111",
		"xbffab4c3-9d88-43ab-4111-111111111111",
		`{"id":"bffab4c3-9d88-43ab-4111-111111111111"}`,
		// UUID with an all-digit tail that is not Luhn-valid.
		"evt-bffab4c3-9d88-4349-9131-824401265021",
		`{"id":"evt-bffab4c3-9d88-4349-9131-824401265021"}`,
		// Alphanumerics glued to a Luhn-valid run.
		"abc4111111111111111",
		"4111111111111111abc",
		"deadbeef4111111111111111cafe",
		// Longer digit run: the 19-digit window is followed by a digit.
		"41111111111111111111",
	} {
		assert.Equal(t, in, r.Apply(in), "must round-trip: %s", in)
	}
}

func TestCreditCard_StillMatchesRealCards(t *testing.T) {
	r := cardRedactor(t)
	for _, c := range []struct{ in, digits string }{
		{"card 4111 1111 1111 1111 on file", "4111 1111 1111 1111"},
		{"card 4111-1111-1111-1111.", "4111-1111-1111-1111"},
		{`{"pan":"4111111111111111"}`, "4111111111111111"},
		{"amex 378282246310005", "378282246310005"},
		// An alphabetic label joined by a hyphen or underscore is a
		// boundary: the number is still redacted.
		{"card-4111111111111111", "4111111111111111"},
		{"cc_4111111111111111", "4111111111111111"},
		{"4111111111111111_cc", "4111111111111111"},
		{"4111111111111111-card", "4111111111111111"},
	} {
		out := r.Apply(c.in)
		assert.NotContains(t, out, c.digits, "must redact: %s", c.in)
		assert.Contains(t, out, "***REDACTED***", c.in)
	}
}

// A label keeps its text: only the number is replaced.
func TestCreditCard_LabelJoinedKeepsLabel(t *testing.T) {
	r := cardRedactor(t)
	assert.Equal(t, "card-***REDACTED***", r.Apply("card-4111111111111111"))
	assert.Equal(t, "cc_***REDACTED***", r.Apply("cc_4111111111111111"))
}

// Luhn: a plain 16-digit run failing the checksum is not a card.
func TestCreditCard_NonLuhnRunNotRedacted(t *testing.T) {
	r := cardRedactor(t)
	for _, in := range []string{
		"ref 1234 5678 9012 3456",
		"card-1234567890123456",
		// Luhn-valid but only 12 digits: below the card floor.
		"id 000000000000",
	} {
		assert.Equal(t, in, r.Apply(in), in)
	}
}
