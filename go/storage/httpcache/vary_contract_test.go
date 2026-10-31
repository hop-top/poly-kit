package httpcache

import (
	"net/http"
	"reflect"
	"testing"
)

// TestContract_VaryKeying replays the vary section of keying.json.
func TestContract_VaryKeying(t *testing.T) {
	var v struct {
		Vary struct {
			IndexCases []struct {
				Name, Method, URL string
				WantKey           string `json:"want_key"`
			} `json:"index_cases"`
			VariantCases []struct {
				Name, Method, URL string
				Fields            []string
				RequestHeaders    map[string][]string `json:"request_headers"`
				WantDigest        string              `json:"want_digest"`
			} `json:"variant_cases"`
			FieldListCases []struct {
				Name string
				Vary []string
				Want []string
			} `json:"field_list_cases"`
		} `json:"vary"`
	}
	readContract(t, "keying.json", &v)
	if len(v.Vary.IndexCases) == 0 || len(v.Vary.VariantCases) == 0 || len(v.Vary.FieldListCases) == 0 {
		t.Fatal("keying.json vary section is empty")
	}
	tr := New(nil, http.DefaultTransport)
	for _, c := range v.Vary.IndexCases {
		req, _ := http.NewRequest(c.Method, c.URL, nil)
		if got := tr.varyIndexKey(req); got != c.WantKey {
			t.Errorf("%s: index key = %q, want %q", c.Name, got, c.WantKey)
		}
	}
	for _, c := range v.Vary.VariantCases {
		req, _ := http.NewRequest(c.Method, c.URL, nil)
		for k, vals := range c.RequestHeaders {
			for _, val := range vals {
				req.Header.Add(k, val)
			}
		}
		if got, want := tr.varyKey(req, c.Fields), defaultPrefix+"vary:"+c.WantDigest; got != want {
			t.Errorf("%s: variant key = %q, want %q", c.Name, got, want)
		}
	}
	for _, c := range v.Vary.FieldListCases {
		h := http.Header{}
		for _, line := range c.Vary {
			h.Add("Vary", line)
		}
		got := varyFields(h)
		if len(got) == 0 && len(c.Want) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, c.Want) {
			t.Errorf("%s: varyFields = %v, want %v", c.Name, got, c.Want)
		}
	}
}

// TestContract_VaryAuthCacheability replays the extension sections of
// cacheability.json through the storage gate.
func TestContract_VaryAuthCacheability(t *testing.T) {
	var v struct {
		Vary []struct {
			Name    string
			Status  int
			Headers map[string]string
			Want    bool
		} `json:"vary_response_cacheable"`
		Auth []struct {
			Name            string
			ResponseHeaders map[string]string `json:"response_headers"`
			Want            bool
		} `json:"authorization_response_cacheable"`
		Private []struct {
			Name    string
			Status  int
			Headers map[string]string
			Want    bool
		} `json:"private_response_cacheable"`
	}
	readContract(t, "cacheability.json", &v)
	if len(v.Vary) == 0 || len(v.Auth) == 0 || len(v.Private) == 0 {
		t.Fatal("cacheability.json extension sections are empty")
	}
	plain, _ := http.NewRequest(http.MethodGet, "https://example.com/a", nil)
	authed := plain.Clone(plain.Context())
	authed.Header.Set("Authorization", "Bearer x")
	mk := func(status int, hdr map[string]string) *http.Response {
		r := &http.Response{StatusCode: status, Header: http.Header{}}
		for k, val := range hdr {
			r.Header.Set(k, val)
		}
		return r
	}
	for _, c := range v.Vary {
		if got := storableFor(plain, mk(c.Status, c.Headers)); got != c.Want {
			t.Errorf("vary/%s: storable = %v, want %v", c.Name, got, c.Want)
		}
	}
	for _, c := range v.Auth {
		if got := storableFor(authed, mk(200, c.ResponseHeaders)); got != c.Want {
			t.Errorf("auth/%s: storable = %v, want %v", c.Name, got, c.Want)
		}
	}
	for _, c := range v.Private {
		if got := storableFor(plain, mk(c.Status, c.Headers)); got != c.Want {
			t.Errorf("private/%s: storable = %v, want %v", c.Name, got, c.Want)
		}
	}
}
