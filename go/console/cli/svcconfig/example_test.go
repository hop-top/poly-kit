package svcconfig_test

import (
	"fmt"
	"strings"

	"github.com/spf13/viper"

	"hop.top/kit/go/console/cli/svcconfig"
)

func Example() {
	v := viper.New()
	v.Set("services.all.body_limit.max_bytes", 2048)
	v.Set("services.api.body_limit.enabled", true)

	r := svcconfig.New(v)
	val, from, _ := r.Lookup("api", "body_limit", "max_bytes")
	fmt.Println(val, from)

	v.Set("services.all.addr", "0.0.0.0:8080")
	msg, _, _ := strings.Cut(r.Validate().Error(), ";")
	fmt.Println(msg)
	// Output:
	// 2048 services.all.body_limit.max_bytes
	// services.all.addr: not a middleware key
}
