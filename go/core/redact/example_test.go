package redact_test

import (
	"fmt"

	"hop.top/kit/go/core/redact"
)

// DefaultPresidioPath resolves to the embedded corpus unless
// KIT_REDACT_PII_RULES_PATH overrides it.
func ExampleLoadPresidio() {
	rs, err := redact.LoadPresidio(redact.DefaultPresidioPath())
	if err != nil {
		fmt.Println("err:", err)
		return
	}
	fmt.Println(len(rs) > 0)
	// Output: true
}
