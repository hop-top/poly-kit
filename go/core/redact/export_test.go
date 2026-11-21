package redact

// IBANLengthsForTest exposes the IBAN length table to external tests,
// registry countries only (aliases excluded).
func IBANLengthsForTest() map[string]int { return ibanLen }
