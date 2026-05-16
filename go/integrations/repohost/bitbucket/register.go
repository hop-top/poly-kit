package bitbucket

import (
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/drone/go-scm/scm"
	scmbitbucket "github.com/drone/go-scm/scm/driver/bitbucket"
	"github.com/drone/go-scm/scm/transport"

	"hop.top/kit/go/integrations/repohost"
)

// defaultBaseURL is the Bitbucket Cloud REST API v2 root used when
// [repohost.Config.BaseURL] is empty.
const defaultBaseURL = "https://api.bitbucket.org"

func init() {
	repohost.RegisterDriver("bitbucket", Open)
}

// Open constructs a Bitbucket Cloud MutableHost from cfg. Token
// resolution order: cfg.Token, then BITBUCKET_TOKEN. Tokens are
// applied as Bearer auth (modern Atlassian API tokens). Bitbucket
// Server is out of scope — it would need a separate provider name
// delegating to go-scm's stash driver.
func Open(cfg repohost.Config) (repohost.MutableHost, error) {
	if cfg.Provider != "bitbucket" {
		return nil, fmt.Errorf("bitbucket: provider mismatch: got %q", cfg.Provider)
	}
	base := cfg.BaseURL
	if base == "" {
		base = defaultBaseURL
	}

	token := cfg.Token
	if token == "" {
		token = os.Getenv("BITBUCKET_TOKEN")
	}

	var (
		client *scm.Client
		err    error
	)
	if cfg.BaseURL == "" {
		client = scmbitbucket.NewDefault()
	} else {
		client, err = scmbitbucket.New(base)
		if err != nil {
			return nil, fmt.Errorf("bitbucket: new client: %w", err)
		}
	}

	client.Client = httpClientWithToken(cfg.HTTPClient, token)
	return &Host{
		cfg:     cfg,
		client:  client,
		baseURL: strings.TrimRight(base, "/"),
	}, nil
}

func httpClientWithToken(base *http.Client, token string) *http.Client {
	if base == nil {
		base = &http.Client{}
	}
	if token == "" {
		return base
	}
	out := *base
	out.Transport = &transport.BearerToken{
		Base:  base.Transport,
		Token: token,
	}
	return &out
}
