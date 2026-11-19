package llm_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/llm"
)

type stubDoer struct{ got *http.Request }

func (s *stubDoer) Do(r *http.Request) (*http.Response, error) {
	s.got = r
	return &http.Response{StatusCode: 204, Body: http.NoBody, Request: r}, nil
}

func TestResolveWithTransportReachesTheAdapter(t *testing.T) {
	var got []llm.ResolvedConfig
	r := hermeticRegistry(&got)
	rt := llm.DoerTransport(&stubDoer{})

	_, err := r.Resolve("fake://m")
	require.NoError(t, err)
	_, err = r.Resolve("fake://m", llm.WithTransport(rt), llm.Hermetic())
	require.NoError(t, err)

	require.Len(t, got, 2)
	assert.Nil(t, got[0].Transport, "nil by default")
	assert.NotNil(t, got[1].Transport)
	assert.True(t, got[1].Hermetic, "combines with Hermetic")
}

func TestDoerTransport(t *testing.T) {
	assert.Nil(t, llm.DoerTransport(nil), "no doer, no transport")

	d := &stubDoer{}
	req, _ := http.NewRequest(http.MethodGet, "http://x.test/p", nil)
	resp, err := llm.DoerTransport(d).RoundTrip(req)
	require.NoError(t, err)
	assert.Equal(t, 204, resp.StatusCode)
	assert.Same(t, req, d.got)
}

func TestHTTPClient(t *testing.T) {
	rt := llm.DoerTransport(&stubDoer{})
	c := llm.ResolvedConfig{Transport: rt}.HTTPClient()
	assert.Equal(t, rt, c.Transport)

	// Never follows a redirect.
	require.NotNil(t, c.CheckRedirect)
	assert.ErrorIs(t, c.CheckRedirect(nil, nil), http.ErrUseLastResponse)

	// No transport: still a usable client, redirects still refused.
	d := llm.ResolvedConfig{}.HTTPClient()
	assert.NotNil(t, d.Transport)
	assert.ErrorIs(t, d.CheckRedirect(nil, nil), http.ErrUseLastResponse)
}
