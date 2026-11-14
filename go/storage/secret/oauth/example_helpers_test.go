package oauth_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"

	"hop.top/kit/go/storage/secret/file"
)

// exampleDirs returns a file store rooted in a fresh temp dir, a lock dir
// beside it, and a cleanup func.
func exampleDirs() (store *file.Store, lockDir string, cleanup func()) {
	root, err := os.MkdirTemp("", "oauth-example-")
	if err != nil {
		panic(err)
	}
	return file.New(root+"/secrets", nil), root + "/locks", func() { _ = os.RemoveAll(root) }
}

// tokenServer answers every token request with next(form) and the status 200.
func tokenServer(next func(form map[string]string) string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		form := map[string]string{}
		for k := range r.PostForm {
			form[k] = r.PostForm.Get(k)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, next(form))
	}))
}
