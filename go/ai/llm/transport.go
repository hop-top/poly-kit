package llm

import "net/http"

// Doer is the one method of an HTTP client an adapter needs: anything
// that sends a request and returns a response, an *http.Client among
// them.
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

// DoerTransport adapts d to an [http.RoundTripper], for [WithTransport]
// when the caller holds a client or recorder rather than a transport.
// A nil d yields a nil transport: the adapter's default applies.
func DoerTransport(d Doer) http.RoundTripper {
	if d == nil {
		return nil
	}
	return doerTransport{d}
}

type doerTransport struct{ d Doer }

func (t doerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return t.d.Do(r)
}

// defaultTransport sends through http.DefaultClient's transport, as
// read at request time, so a transport installed there process-wide
// (a proxy, a recorder) still applies when the caller supplied none.
type defaultTransport struct{}

func (defaultTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if rt := http.DefaultClient.Transport; rt != nil {
		return rt.RoundTrip(r)
	}
	return http.DefaultTransport.RoundTrip(r)
}

// HTTPClient returns the client an adapter sends through: the config's
// [ResolvedConfig.Transport], or without one http.DefaultClient's
// transport as read at request time. It never follows a redirect: the
// 3xx comes back as the response, so a credential never moves to a host
// the server names. An adapter wanting its own redirect error replaces
// CheckRedirect on the returned client.
func (cfg ResolvedConfig) HTTPClient() *http.Client {
	rt := cfg.Transport
	if rt == nil {
		rt = defaultTransport{}
	}
	return &http.Client{
		Transport: rt,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}
