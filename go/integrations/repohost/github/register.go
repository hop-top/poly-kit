package github

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/drone/go-scm/scm"
	scmgithub "github.com/drone/go-scm/scm/driver/github"
	"github.com/drone/go-scm/scm/transport"

	"hop.top/kit/go/integrations/repohost"
)

func init() {
	repohost.RegisterDriver("github", Open)
}

// Open constructs a GitHub MutableHost from cfg. Token resolution
// order: cfg.Token, then GITHUB_TOKEN, then GH_TOKEN. When BaseURL
// is empty, the driver targets api.github.com (SaaS); otherwise it
// targets the GitHub Enterprise Server endpoint, normalizing the URL
// to include /api/v3.
func Open(cfg repohost.Config) (repohost.MutableHost, error) {
	if cfg.Provider != "github" {
		return nil, fmt.Errorf("github: provider mismatch: got %q", cfg.Provider)
	}
	token := cfg.Token
	if token == "" {
		token = os.Getenv("GITHUB_TOKEN")
	}
	if token == "" {
		token = os.Getenv("GH_TOKEN")
	}

	var (
		client *scm.Client
		err    error
	)
	if cfg.BaseURL == "" {
		client = scmgithub.NewDefault()
	} else {
		base, normErr := normalizeEnterpriseURL(cfg.BaseURL)
		if normErr != nil {
			return nil, fmt.Errorf("github: bad BaseURL: %w", normErr)
		}
		client, err = scmgithub.New(base)
		if err != nil {
			return nil, fmt.Errorf("github: new client: %w", err)
		}
	}

	client.Client = httpClientWithToken(cfg.HTTPClient, token)
	return &Host{cfg: cfg, client: client}, nil
}

// httpClientWithToken wraps base's transport with a bearer-token
// round-tripper. When base is nil, http.DefaultClient is used as the
// starting point. When token is empty, the unwrapped client is
// returned (anonymous, rate-limited access).
func httpClientWithToken(base *http.Client, token string) *http.Client {
	if base == nil {
		base = &http.Client{}
	}
	if token == "" {
		return base
	}
	out := *base // shallow copy so we don't mutate caller's client
	out.Transport = &transport.BearerToken{
		Base:  base.Transport,
		Token: token,
	}
	return &out
}

// normalizeEnterpriseURL appends /api/v3/ to a GHE base URL when the
// caller passed only the host root. Already-suffixed URLs and
// httptest endpoints (which expose the API at root) are returned as
// given.
func normalizeEnterpriseURL(base string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	host := strings.ToLower(u.Host)
	if host == "api.github.com" {
		return base, nil
	}
	// Tests wire httptest.NewServer endpoints (127.0.0.1:NNNNN) that
	// serve the API directly at root — don't tack on /api/v3 there.
	if isLoopback(host) {
		if !strings.HasSuffix(base, "/") {
			base += "/"
		}
		return base, nil
	}
	if strings.Contains(u.Path, "/api/v3") {
		if !strings.HasSuffix(base, "/") {
			base += "/"
		}
		return base, nil
	}
	trimmed := strings.TrimRight(base, "/")
	return trimmed + "/api/v3/", nil
}

// isLoopback reports whether the host is a loopback address (used by
// httptest.NewServer); we keep the check simple — testing through a
// reverse proxy is not the target use case.
func isLoopback(host string) bool {
	h := host
	if i := strings.LastIndex(h, ":"); i >= 0 {
		h = h[:i]
	}
	switch h {
	case "127.0.0.1", "localhost", "::1", "[::1]":
		return true
	}
	return false
}
