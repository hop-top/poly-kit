package httpwrap_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"

	"hop.top/kit/go/runtime/provenance"
	"hop.top/kit/go/runtime/provenance/wrap/httpwrap"
)

func Example() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "beta")
	}))
	defer srv.Close()

	ctx := provenance.WithMode(context.Background(), provenance.ModeWarn) // ModeOff records nothing
	c := httpwrap.New(http.DefaultClient)
	body, prov, err := c.ReadAll(ctx, "/cohort", srv.URL+"/cohort")
	if err != nil {
		panic(err)
	}
	out := struct {
		Cohort provenance.Cached[string] `json:"cohort"`
	}{Cohort: provenance.NewCached(string(body), prov)}
	fmt.Println(out.Cohort.Value(), prov.Source, prov.URL == srv.URL+"/cohort")

	// Output:
	// beta authoritative true
}
