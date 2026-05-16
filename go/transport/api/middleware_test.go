package api_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	charmlog "charm.land/log/v2"
	"github.com/spf13/viper"
	kitlog "hop.top/kit/go/console/log"
	"hop.top/kit/go/transport/api"

	"github.com/stretchr/testify/assert"
)

func TestChain_Order(t *testing.T) {
	var order []int
	mw := func(n int) api.Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, n)
				next.ServeHTTP(w, r)
			})
		}
	}

	h := api.Chain(mw(1), mw(2), mw(3))(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		order = append(order, 0)
	}))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	assert.Equal(t, []int{1, 2, 3, 0}, order)
}

func TestChain_Empty(t *testing.T) {
	called := false
	h := api.Chain()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
	}))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	assert.True(t, called)
}

func TestLogger_AcceptsKitLogInfoMethodValue(t *testing.T) {
	// kitlog.New returns *charmlog.Logger; .Info is a bound method
	// value of shape func(msg string, keyvals ...any), which must
	// satisfy api.Logger's parameter type without a wrapper.
	var buf bytes.Buffer
	l := kitlog.New(viper.New())
	l.SetOutput(&buf)
	l.SetLevel(charmlog.InfoLevel)

	mw := api.Logger(l.Info)
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/widgets", nil))

	assert.Equal(t, http.StatusTeapot, rec.Code)
	out := buf.String()
	assert.Contains(t, out, "http request")
	assert.Contains(t, out, "method=GET")
	assert.Contains(t, out, "path=/widgets")
	assert.Contains(t, out, "status=418")
}

func TestWithMiddleware(t *testing.T) {
	called := false
	mw := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			next.ServeHTTP(w, r)
		})
	}

	r := api.NewRouter(api.WithMiddleware(mw))
	r.Handle("GET", "/test", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	r.ServeHTTP(rec, req)

	assert.True(t, called)
	assert.Equal(t, http.StatusOK, rec.Code)
}
