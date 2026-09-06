package config_test

import (
	"fmt"
	"strings"

	"hop.top/kit/go/ai/ext/config"
)

func Example() {
	yaml := `
extensions:
  audit:
    enabled: false
  stats:
    enabled: true
    settings:
      interval: 30
`
	store := config.NewStore()
	if err := store.Load(strings.NewReader(yaml)); err != nil {
		panic(err)
	}
	fmt.Println("audit:", store.IsEnabled("audit"))
	fmt.Println("stats:", store.IsEnabled("stats"), store.Settings("stats")["interval"])
	fmt.Println("unknown:", store.IsEnabled("unknown"))
	// Output:
	// audit: false
	// stats: true 30
	// unknown: true
}
