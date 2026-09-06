package pkl_test

import (
	"fmt"

	"hop.top/kit/go/core/config/pkl"
)

// LoadSchema parses PKL source text; no pkl binary is needed.
func ExampleLoadSchema() {
	s, err := pkl.LoadSchema("testdata/basic.pkl")
	if err != nil {
		fmt.Println("err:", err)
		return
	}
	for _, item := range pkl.CompletionKeys(s) {
		fmt.Println(item.Value)
	}
	fmt.Println(pkl.ValidateValue(s, "port", "abc") != nil)
	// Output:
	// name
	// port
	// debug
	// true
}
