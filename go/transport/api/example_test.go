package api_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"

	"hop.top/kit/go/transport/api"
)

func ExampleNewRouter() {
	r := api.NewRouter()
	r.Handle("GET", "/hello", func(w http.ResponseWriter, _ *http.Request) {
		api.JSON(w, http.StatusOK, map[string]string{"msg": "hello"})
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/hello", nil)
	r.ServeHTTP(rec, req)

	fmt.Println(rec.Code)
	// Output: 200
}

func ExampleChain() {
	noop := func(next http.Handler) http.Handler { return next }
	mw := api.Chain(noop, noop)

	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	fmt.Println(rec.Code)
	// Output: 200
}

func ExampleResourceRouter() {
	svc := newMockService()
	h := api.ResourceRouter[widget](svc)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	h.ServeHTTP(rec, req)

	fmt.Println(rec.Code)
	// Output: 200
}

func ExamplePathParam() {
	r := api.NewRouter()
	r.Handle("GET", "/items/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := api.PathParam(r, "id")
		api.JSON(w, http.StatusOK, map[string]string{"id": id})
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/items/42", nil)
	r.ServeHTTP(rec, req)

	fmt.Println(rec.Code)
	// Output: 200
}
