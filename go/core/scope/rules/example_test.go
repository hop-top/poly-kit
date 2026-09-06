package rules_test

import (
	"fmt"

	"github.com/BurntSushi/toml"

	"hop.top/kit/go/core/scope/rules"
)

// The embedded bytes are plain gitleaks TOML.
func Example() {
	var doc struct {
		Rules []struct {
			ID string `toml:"id"`
		} `toml:"rules"`
	}
	if err := toml.Unmarshal(rules.GitleaksContent, &doc); err != nil {
		fmt.Println("err:", err)
		return
	}
	fmt.Println(len(doc.Rules) > 0, len(rules.GitleaksPaths) > 0)
	// Output: true true
}
