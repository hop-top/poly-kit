package netpolicy_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"hop.top/kit/go/core/netpolicy"
)

// A client with an explicit Transport must wrap it in Guard; Install
// only reaches http.DefaultTransport.
func ExampleGuard() {
	client := &http.Client{Transport: netpolicy.Guard(http.DefaultTransport)}

	ctx := netpolicy.WithOffline(context.Background(), true)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://example.com/", nil)
	_, err := client.Do(req)
	fmt.Println("refused:", errors.Is(err, netpolicy.ErrOffline))
	// Output: refused: true
}
