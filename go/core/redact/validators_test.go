package redact_test

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"testing"

	charmlog "charm.land/log/v2"
	"github.com/stretchr/testify/assert"
	"hop.top/kit/go/core/redact"
)

func TestValidateIBAN(t *testing.T) {
	// Examples from the SWIFT IBAN registry (ISO 13616-2).
	valid := []string{
		"GB82 WEST 1234 5698 7654 32", "GB82WEST12345698765432",
		"gb82west12345698765432",
		"DE89 3704 0044 0532 0130 00", "FR14 2004 1010 0505 0001 3M02 606",
		"NL91 ABNA 0417 1643 00", "ES91 2100 0418 4502 0005 1332",
		"IT60 X054 2811 1010 0000 0123 456", "CH93 0076 2011 6238 5295 7",
		"BE68 5390 0754 7034", "NO93 8601 1117 947",
		"AT61 1904 3002 3457 3201", "PL61 1090 1014 0000 0712 1981 2874",
		"SE45 5000 0000 0583 9825 7466", "BR18 0036 0305 0000 1000 9795 493C1",
		"SA03 8000 0000 6080 1016 7519", "TR33 0006 1005 1978 6457 8413 26",
		"BI42 1000 0100 0100 0033 2045 181", // Burundi: 27, not 16
		// Territory prefixes, check under the parent's code or their own.
		"GF14 2004 1010 0505 0001 3M02 606", "GF41 2004 1010 0505 0001 3M02 606",
		"MQ14 2004 1010 0505 0001 3M02 606", "JE82WEST12345698765432",
		"JE46WEST12345698765432", "GG82WEST12345698765432", "IM82WEST12345698765432",
	}
	invalid := []string{
		"",
		"GB82 WEST 1234 5698 7654 33", // check digits off by one
		"GB83WEST12345698765432",      // wrong check digits
		"GB82WEST1234569876543",       // one short of the GB length
		"GB82WEST123456987654321",     // one over
		"XX82WEST12345698765432",      // unknown country
		// Valid mod-97 check, wrong for the country: only the length
		// and country table reject these.
		"GB88WEST1234569876543", "GB49WEST123456987654321",
		"XX57WEST12345698765432", "DE5137040044053201300",
		"G882WEST12345698765432",      // digit where a letter belongs
		"GBA2WEST12345698765432",      // letter in the check digits
		"GB82-WEST-1234-5698-7654-32", // hyphens are not IBAN separators
		"GB82 WEST 1234 5698 7654 3!",
		"DE89 3704 0044 0532 0130 01",
		"1234",
		// Check digits 00, 01 and 99 are never issued, though mod-97 holds.
		"GB00WEST00000000000065", "GB01WEST00000000000047", "GB99WEST00000000000029",
		"GF14 2004 1010 0505 0001 3M02 60", // territory, wrong length for FR
		"AX82WEST12345698765432",           // AX is held to FI's 18
	}
	for _, s := range valid {
		assert.True(t, redact.ValidateIBAN(s), s)
	}
	for _, s := range invalid {
		assert.False(t, redact.ValidateIBAN(s), s)
	}
}

func TestValidatePhone(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"+1 415 555 2671", true}, // E.164 example for the US
		{"+44 20 7946 0958", true},
		{"(415) 555-2671", true},
		{"415.555.2671", true},
		{"+33 1 23 45 67 89", true},
		{"0044 20 7946 0958", true},
		{"12345678", true},        // 8 digits: lower bound
		{"123456789012345", true}, // 15 digits: upper bound
		{"+123456789012345", true},
		{"1234567", false},           // 7 digits
		{"1234567890123456", false},  // 16 digits
		{"+1234567890123456", false}, // 16 digits after +
		{"", false},
		{"+", false},
		{"call 415 555 2671", false},
		{"415 555 2671 x12", false},
		{"415+5552671", false},
		{"((415)) 555 2671", false},
		{"(415 555 2671", false},
		{"415) 555 2671", false},
		{"00 1234567", false}, // only 7 digits after the 00 prefix
		{"+683 4002", true},   // 7 digits with +: real E.164 (Niue)
		{"+290 1234", true},   // Saint Helena
		{"+123456", false},    // 6 digits
		{"683 4002", false},   // 7 digits without +
		{"+0044 20 7946 0958", false},
		{"+00 44 20 7946 0958", false},
		{"000044 20 7946 0958", false}, // 00 stripped once; 0 country code
		{"() 415 555 2671", false},
		{"(415)() 555 2671", false},
		{"+1 () 415 555 2671", false},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, redact.ValidatePhone(c.in), c.in)
	}
}

func TestNormalizeE164(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"+1 (415) 555-2671", "+14155552671", true},
		{"+44 20 7946 0958", "+442079460958", true},
		{"0044 20 7946 0958", "+442079460958", true},
		{" +33 1 23 45 67 89 ", "+33123456789", true},
		{"+14155552671", "+14155552671", true},
		{"(415) 555-2671", "", false},  // national: no country code
		{"+0 415 555 2671", "", false}, // country codes never start with 0
		{"+683 4002", "+6834002", true},
		{"+290 1234", "+2901234", true},
		{"+123456", "", false},     // 6 digits
		{"00 683 4002", "", false}, // 7 digits after 00
		{"+0044 20 7946 0958", "", false},
		{"+00 44 20 7946 0958", "", false},
		{"() +1 415", "", false},
		{"+1 () 415 555 2671", "", false},
		{"+1234567890123456", "", false}, // 16 digits
		{"+1 415 555 267x", "", false},   // stray letter
		{"", "", false},
	}
	for _, c := range cases {
		got, ok := redact.NormalizeE164(c.in)
		assert.Equal(t, c.ok, ok, c.in)
		assert.Equal(t, c.want, got, c.in)
	}
}

func TestValidateNIR(t *testing.T) {
	valid := []string{
		"1 84 12 76 451 089 46",
		"184127645108946", "1.84.12.76.451.089.46", "1 84.12 76.451 089.46",
		"2 55 08 14 168 025 38",
		// Corsica: 2A counts as 19, 2B as 18 in the key computation.
		"1 75 12 2A 004 004 01",
		"2 75 03 2B 001 045 77",
		"2 75 03 2b 001 045 77",
	}
	invalid := []string{
		"",
		"1 84 12 76 451 089 47",  // wrong key
		"1 84 12 76 451 089 00",  // key 00 never valid
		"1 75 12 2A 004 004 02",  // Corsica, wrong key
		"3 84 12 76 451 089 43",  // first digit 3 (key valid)
		"4 84 12 76 451 089 90",  // first digit 4 (key valid)
		"0 84 12 76 451 089 96",  // first digit 0 (key valid)
		"5 84 12 76 451 089 40",  // first digit 5 (key valid)
		"1 84 00 76 451 089 48",  // month 00 (key valid)
		"1 84 13 76 451 089 62",  // month 13 (key valid)
		"1 84 12 76 451 089 4",   // 14 characters
		"1 84 12 76 451 089 466", // 16 characters
		"1 84 12 76 451 08A 46",  // letter outside the department
		"1 84 12 7A 451 089 46",  // A outside Corsica
		"1 75 12 2C 004 004 01",  // not 2A or 2B
		"1 75 12 3A 004 004 01",  // A after a non-2 department
	}
	for _, s := range valid {
		assert.True(t, redact.ValidateNIR(s), s)
	}
	for _, s := range invalid {
		assert.False(t, redact.ValidateNIR(s), s)
	}
}

func TestValidateSIN(t *testing.T) {
	valid := []string{
		"130 692 544", "130-692-544", "130692544", // Luhn-valid, leading 1
		"193 456 787",
		"732 000 005", // leading 7
	}
	invalid := []string{
		"",
		"046 454 287",   // checksum off by one
		"123 456 789",   // Luhn-invalid
		"000 000 000",   // Luhn-valid but leading 0
		"046 454 286",   // the published sample SIN: Luhn-valid, leading 0 is unassigned
		"046 454 28",    // 8 digits
		"046 454 2860",  // 10 digits
		"046 454 28A",   // letter
		"800 000 002",   // Luhn-valid, leading 8
		"046.454.286",   // dots are not SIN separators
		"046 454 286 7", // trailing group
	}
	for _, s := range valid {
		assert.True(t, redact.ValidateSIN(s), s)
	}
	for _, s := range invalid {
		assert.False(t, redact.ValidateSIN(s), s)
	}
}

// ---- WithValidator ----

func ibanRedactor(t *testing.T, opts ...redact.Option) *redact.Redactor {
	t.Helper()
	r := redact.New(opts...)
	_, err := r.AddRule("iban", `\b[A-Z]{2}\d{2}[A-Z0-9]{11,30}\b`, "")
	assert.NoError(t, err)
	return r
}

func TestWithValidator_FiltersMatches(t *testing.T) {
	r := ibanRedactor(t, redact.WithValidator("iban", redact.ValidateIBAN))
	good, bad := "pay GB82WEST12345698765432 now", "pay GB82WEST12345698765433 now"
	assert.Equal(t, "pay ***REDACTED*** now", r.Apply(good))
	assert.Equal(t, bad, r.Apply(bad), "failed checksum: not an IBAN")
	assert.Len(t, r.Scan(good), 1)
	assert.Empty(t, r.Scan(bad))
	assert.Len(t, r.Spans(good), 1)
	assert.Empty(t, r.Spans(bad))
	assert.Equal(t, []byte("pay GB82WEST12345698765433 now"), r.ApplyBytes([]byte(bad)))
}

func TestWithValidator_NoValidatorRedactsAll(t *testing.T) {
	r := ibanRedactor(t)
	assert.NotEqual(t, "pay GB82WEST12345698765433", r.Apply("pay GB82WEST12345698765433"))
}

func TestWithValidator_ReceivesMatchedText(t *testing.T) {
	var got []string
	r := ibanRedactor(t, redact.WithValidator("iban", func(m string) bool {
		got = append(got, m)
		return true
	}))
	r.Apply("a GB82WEST12345698765432 b")
	assert.Equal(t, []string{"GB82WEST12345698765432"}, got)
}

func TestWithValidator_OnlyNamedRule(t *testing.T) {
	r := redact.New(redact.WithValidator("a", func(string) bool { return false }))
	_, _ = r.AddRule("a", `AAA\d+`, "")
	_, _ = r.AddRule("b", `BBB\d+`, "")
	assert.Equal(t, "AAA1 ***REDACTED***", r.Apply("AAA1 BBB2"))
}

func TestWithValidator_AllMustAccept(t *testing.T) {
	r := redact.New(
		redact.WithValidator("a", func(string) bool { return true }),
		redact.WithValidator("a", func(m string) bool { return strings.HasSuffix(m, "9") }),
	)
	_, _ = r.AddRule("a", `AAA\d`, "")
	assert.Equal(t, "AAA1 ***REDACTED***", r.Apply("AAA1 AAA9"))
}

// A caller validator on a kit rule narrows it; kit's own checks stay.
func TestWithValidator_AddsToBuiltinChecks(t *testing.T) {
	p, err := redact.LoadPresidioReader(strings.NewReader(presidioDoc))
	assert.NoError(t, err)
	// No-op validator: the card rule still enforces Luhn.
	r := redact.New(redact.WithValidator("credit-card", func(string) bool { return true })).AddRules(p...)
	assert.Equal(t, "***REDACTED***", r.Apply("4111111111111111"))
	assert.Equal(t, "4111111111111112", r.Apply("4111111111111112"), "Luhn still applies")
	// A rejecting validator removes a Luhn-valid card.
	r = redact.New(redact.WithValidator("credit-card", func(string) bool { return false })).AddRules(p...)
	assert.Equal(t, "4111111111111111", r.Apply("4111111111111111"))
}

func TestWithValidator_PanicFailsClosed(t *testing.T) {
	r := ibanRedactor(t, redact.WithValidator("iban", func(string) bool { panic("boom") }))
	assert.Equal(t, "***REDACTED***", r.Apply("GB82WEST12345698765432"))
}

func TestWithValidator_PanicsOnBadArgs(t *testing.T) {
	assert.Panics(t, func() { redact.WithValidator("", func(string) bool { return true }) })
	assert.Panics(t, func() { redact.WithValidator("x", nil) })
}

func TestWithValidator_ConcurrentApply(t *testing.T) {
	r := ibanRedactor(t, redact.WithValidator("iban", redact.ValidateIBAN))
	done := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for j := 0; j < 200; j++ {
				assert.Equal(t, "***REDACTED***", r.Apply("GB82WEST12345698765432"))
				assert.Equal(t, "GB82WEST12345698765433", r.Apply("GB82WEST12345698765433"))
			}
		}()
	}
	for i := 0; i < 8; i++ {
		<-done
	}
}

// Validators are per Redactor; Default() and the embedded policy never
// see them, so the shared default behavior (and parity) is unchanged.
func TestDefaultUnaffectedByValidators(t *testing.T) {
	const badIBAN = "GB82WEST12345698765433" // fails mod-97
	_ = redact.New(redact.WithValidator("iban", redact.ValidateIBAN))
	d := redact.Default()
	assert.NotEqual(t, badIBAN, d.Apply(badIBAN), "built-in iban rule stays shape-only")
	e, err := redact.NewEmbedded()
	assert.NoError(t, err)
	assert.NotEqual(t, badIBAN, e.Apply(badIBAN))
	assert.Equal(t, e.Rules(), d.Rules())
}

// ---- LoadPresidioReader ----

const presidioDoc = `
[[rule]]
id = "credit-card"
description = "card"
pattern = '\b(?:\d[ -]?){13,19}\b'
replacement = "<CARD>"

[[rule]]
id = "lookahead"
description = "RE2-incompatible, skipped"
pattern = '(?=x)x'
`

func TestLoadPresidioReader(t *testing.T) {
	rs, err := redact.LoadPresidioReader(strings.NewReader(presidioDoc))
	assert.NoError(t, err)
	if assert.Len(t, rs, 1) {
		assert.Equal(t, "credit-card", rs[0].ID())
		assert.Equal(t, "<CARD>", rs[0].Spec().Replacement)
	}
}

func TestLoadPresidioReader_MatchesPathLoader(t *testing.T) {
	p, err := redact.LoadPresidio(redact.DefaultPresidioPath())
	assert.NoError(t, err)
	assert.NotEmpty(t, p)
	// An empty document is valid and yields no rules.
	rs, err := redact.LoadPresidioReader(strings.NewReader(""))
	assert.NoError(t, err)
	assert.Empty(t, rs)
}

func TestLoadPresidioReader_Errors(t *testing.T) {
	_, err := redact.LoadPresidioReader(strings.NewReader("[[rule"))
	assert.ErrorContains(t, err, "parse presidio rules")
	_, err = redact.LoadPresidioReader(errReader{})
	assert.ErrorContains(t, err, "read presidio rules")
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, assert.AnError }

// Every table entry matches the vendored registry list and every
// registry country is in the table.
func TestIBANLengthTableMatchesRegistry(t *testing.T) {
	raw, err := os.ReadFile("testdata/iban-registry-v101.txt")
	assert.NoError(t, err)
	want := map[string]int{}
	for _, l := range strings.Split(string(raw), "\n") {
		if l == "" || l[0] == '#' {
			continue
		}
		var cc string
		var n int
		_, err := fmt.Sscanf(l, "%s %d", &cc, &n)
		assert.NoError(t, err, l)
		want[cc] = n
	}
	assert.Len(t, want, 89)
	assert.Equal(t, want, redact.IBANLengthsForTest())
}

// Each registry country accepts an IBAN of exactly its length and
// rejects one a character shorter or longer, even with a valid check.
func TestValidateIBAN_RegistryLengths(t *testing.T) {
	for cc, n := range redact.IBANLengthsForTest() {
		for _, l := range []int{n - 1, n, n + 1} {
			iban := ibanWithCheck(cc, strings.Repeat("0", l-4))
			assert.Equal(t, l == n, redact.ValidateIBAN(iban), "%s len %d", cc, l)
		}
	}
}

// ibanWithCheck builds cc + check + bban with a valid mod-97 check.
func ibanWithCheck(cc, bban string) string {
	rem := 0
	for _, c := range bban + cc + "00" {
		if c >= 'A' {
			rem = (rem*100 + int(c-'A') + 10) % 97
		} else {
			rem = (rem*10 + int(c-'0')) % 97
		}
	}
	return fmt.Sprintf("%s%02d%s", cc, 98-rem, bban)
}

func TestWithValidator_PanicLoggedOncePerRule(t *testing.T) {
	var buf bytes.Buffer
	l := charmlog.New(&buf)
	r := ibanRedactor(t, redact.WithLogger(l), redact.WithValidator("iban", func(string) bool { panic("secret-in-panic") }))
	for i := 0; i < 3; i++ {
		assert.Equal(t, "***REDACTED***", r.Apply("GB82WEST12345698765432"))
	}
	out := buf.String()
	assert.Equal(t, 1, strings.Count(out, "validator panicked"), out)
	assert.Contains(t, out, "iban")
	assert.NotContains(t, out, "GB82WEST")
}

// A validator attached to the embedded policy's iban rule.
func TestNewEmbedded_WithValidator(t *testing.T) {
	r, err := redact.NewEmbedded(redact.WithValidator("iban", redact.ValidateIBAN))
	assert.NoError(t, err)
	assert.Equal(t, "GB82WEST12345698765433", r.Apply("GB82WEST12345698765433"))
	assert.Equal(t, "***REDACTED***", r.Apply("GB82WEST12345698765432"))
}
