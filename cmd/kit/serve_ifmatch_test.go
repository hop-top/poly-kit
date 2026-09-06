package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

// put issues a PUT with an optional If-Match header and returns the
// response status plus the ETag the server assigned.
func put(t *testing.T, url, body, ifMatch string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, url, bytes.NewReader([]byte(body)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if ifMatch != "" {
		req.Header.Set("If-Match", ifMatch)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	return resp.StatusCode, resp.Header.Get("ETag")
}

// seed creates a document and returns its URL and current ETag.
func seed(t *testing.T, base string) (string, string) {
	t.Helper()
	resp, err := http.Post(base+"/notes/", "application/json",
		bytes.NewReader([]byte(`{"id":"n1","title":"first"}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("seed status = %d, want 201", resp.StatusCode)
	}

	url := base + "/notes/n1"
	get, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer get.Body.Close()
	etag := get.Header.Get("ETag")
	if etag == "" {
		t.Fatal("GET returned no ETag; a client has no token to send back")
	}
	return url, etag
}

func TestGet_CarriesTheCurrentVersionAsAnETag(t *testing.T) {
	router, _, _, _ := newTestEngine(t)
	srv := httptest.NewServer(router)
	defer srv.Close()

	_, etag := seed(t, srv.URL)

	if _, err := strconv.Unquote(etag); err != nil {
		t.Errorf("ETag %q is not a quoted entity tag: %v", etag, err)
	}
}

func TestPut_WithoutIfMatchStaysUnconditional(t *testing.T) {
	router, _, _, _ := newTestEngine(t)
	srv := httptest.NewServer(router)
	defer srv.Close()

	url, _ := seed(t, srv.URL)

	// Two blind writes in a row: last writer wins, as before.
	if code, _ := put(t, url, `{"title":"a"}`, ""); code != http.StatusOK {
		t.Fatalf("first blind put = %d, want 200", code)
	}
	if code, _ := put(t, url, `{"title":"b"}`, ""); code != http.StatusOK {
		t.Fatalf("second blind put = %d, want 200", code)
	}
}

func TestPut_MatchingIfMatchSucceedsAndAdvancesTheETag(t *testing.T) {
	router, _, _, _ := newTestEngine(t)
	srv := httptest.NewServer(router)
	defer srv.Close()

	url, etag := seed(t, srv.URL)

	code, next := put(t, url, `{"title":"updated"}`, etag)
	if code != http.StatusOK {
		t.Fatalf("conditional put = %d, want 200", code)
	}
	if next == "" {
		t.Fatal("successful conditional put returned no ETag")
	}
	if next == etag {
		t.Errorf("ETag did not advance after a write: still %s", next)
	}
}

func TestPut_StaleIfMatchIsRefusedAndChangesNothing(t *testing.T) {
	router, _, _, _ := newTestEngine(t)
	srv := httptest.NewServer(router)
	defer srv.Close()

	url, stale := seed(t, srv.URL)

	// Someone else writes first, so the held tag goes stale.
	if code, _ := put(t, url, `{"title":"theirs"}`, stale); code != http.StatusOK {
		t.Fatalf("first writer = %d, want 200", code)
	}

	// The second writer still holds the original tag.
	code, _ := put(t, url, `{"title":"mine"}`, stale)
	if code != http.StatusPreconditionFailed {
		t.Fatalf("stale conditional put = %d, want 412", code)
	}

	// The refused write must not have landed.
	get, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer get.Body.Close()
	body := make([]byte, 512)
	n, _ := get.Body.Read(body)
	if got := string(body[:n]); !bytes.Contains([]byte(got), []byte("theirs")) {
		t.Errorf("refused write overwrote the document: %s", got)
	}
}

func TestPut_MalformedIfMatchIsRefusedBeforeAnyWrite(t *testing.T) {
	router, _, _, _ := newTestEngine(t)
	srv := httptest.NewServer(router)
	defer srv.Close()

	url, etag := seed(t, srv.URL)

	// Each of these is a client that believes it has a guard. Silently
	// treating any of them as unconditional would be worse than a 400.
	for _, header := range []string{
		"*",
		`W/"abc"`,
		`"a", "b"`,
		"unquoted",
		`""`,
	} {
		code, _ := put(t, url, `{"title":"nope"}`, header)
		if code != http.StatusBadRequest {
			t.Errorf("If-Match %q = %d, want 400", header, code)
		}
	}

	// The document still holds its seeded value and its original tag.
	get, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer get.Body.Close()
	if got := get.Header.Get("ETag"); got != etag {
		t.Errorf("ETag changed after refused writes: %s want %s", got, etag)
	}
}
