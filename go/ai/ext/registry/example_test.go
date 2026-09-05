package registry_test

import (
	"context"
	"fmt"

	"hop.top/kit/go/ai/ext"
	"hop.top/kit/go/ai/ext/registry"
)

type audit struct{}

func (audit) Meta() ext.Metadata           { return ext.Metadata{Name: "audit", Version: "0.1.0"} }
func (audit) Capabilities() ext.Capability { return ext.CapRegistry }
func (audit) Init(context.Context) error   { return nil }
func (audit) Close() error                 { return nil }

func Example() {
	r := registry.New()
	r.Register(audit{})
	for _, e := range r.List() {
		fmt.Println(e.Meta().Name, e.Meta().Version, e.Capabilities())
	}
	// Output:
	// audit 0.1.0 registry
}
