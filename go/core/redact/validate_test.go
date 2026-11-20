package redact_test

import (
	"strings"
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

// digitCases: per rule, a real-shaped value that must be redacted and
// token-embedded forms that must round-trip.
var digitCases = []struct {
	rule  string
	real  string
	label string // label-joined form, still redacted
	kept  []string
}{
	{"us-ssn", "123-45-6789", "ssn-123-45-6789", []string{
		"ORD123-45-6789", "123-45-6789ab",
		"000-12-3456", "666-12-3456", "900-12-3456", "999-12-3456",
		"123-00-6789", "123-45-0000",
	}},
	{"us-itin", "912-70-1234", "itin-912-70-1234", []string{
		"ORD912-70-1234", "912-70-1234ab",
		// groups outside 50-65, 70-88, 90-92, 94-99
		"912-49-1234", "912-66-1234", "912-69-1234", "912-89-1234", "912-93-1234", "912-00-1234",
	}},
	{"phone-us", "415-555-2671", "tel-415-555-2671", []string{
		"ORD415-555-2671", "415-555-2671ab",
	}},
	{"phone-e164", "+14155552671", "tel_+14155552671", []string{
		"id9+14155552671", "+14155552671abc",
	}},
	{"us-passport", "123456789", "passport-123456789", []string{
		"id123456789", "123456789ab", "bffab4c3-9d88-43ab-4111-123456789012",
	}},
	{"us-driver-license", "D1234567", "dl-D1234567", []string{
		"ORD1234567", "D1234567ab",
	}},
}

func TestDigitRules_TokenBoundary(t *testing.T) {
	r := cardRedactor(t)
	for _, c := range digitCases {
		t.Run(c.rule, func(t *testing.T) {
			in := "ref " + c.real + " end"
			assert.Equal(t, "ref ***REDACTED*** end", r.Apply(in), "real value")
			assert.Equal(t, "***REDACTED***", r.Apply(c.real))
			assert.Equal(t, strings.Replace(c.label, c.real, "***REDACTED***", 1), r.Apply(c.label), "label joined")
			for _, k := range c.kept {
				assert.Equal(t, k, r.Apply(k), "must round-trip: %s", k)
				in := "ref " + k + " end"
				assert.Equal(t, in, r.Apply(in), "must round-trip: %s", in)
			}
		})
	}
}

// ITIN group boundaries from the IRS ranges 50-65, 70-88, 90-92, 94-99.
func TestITIN_GroupRanges(t *testing.T) {
	r := cardRedactor(t)
	for _, g := range []string{"50", "65", "70", "88", "90", "92", "94", "99"} {
		in := "912-" + g + "-1234"
		assert.Equal(t, "***REDACTED***", r.Apply(in), in)
	}
}

// 9xx is not an SSN area, so an SSN-shaped 9xx number is only ever an ITIN.
func TestSSN_AreaBoundaries(t *testing.T) {
	r := cardRedactor(t)
	for _, in := range []string{"001-01-0001", "665-12-3456", "667-12-3456", "899-12-3456"} {
		assert.Equal(t, "***REDACTED***", r.Apply(in), in)
	}
}
