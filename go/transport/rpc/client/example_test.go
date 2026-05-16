package client_test

import (
	"net/http"

	"hop.top/kit/go/runtime/domain"
	"hop.top/kit/go/transport/rpc/client"
)

// myEntity satisfies domain.Entity for demonstration purposes.
type myEntity struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (e myEntity) GetID() string { return e.ID }

// compile-time check
var _ domain.Entity = myEntity{}

func ExampleNew() {
	c := client.New[myEntity](
		"http://localhost:8080",
		client.WithAuth("my-bearer-token"),
		client.WithHTTPClient(&http.Client{}),
	)

	// Client is ready; call c.Create, c.Get, c.List, c.Update,
	// or c.Delete against a running server.
	_ = c
}
