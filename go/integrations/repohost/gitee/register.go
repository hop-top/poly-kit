package gitee

import (
	"fmt"
	"net/http"
	"os"

	"github.com/drone/go-scm/scm"
	scmgitee "github.com/drone/go-scm/scm/driver/gitee"
	"github.com/drone/go-scm/scm/transport"

	"hop.top/kit/go/integrations/repohost"
)

func init() {
	repohost.RegisterDriver("gitee", Open)
}

// Open constructs a Gitee MutableHost from cfg. Token resolution
// order: cfg.Token, then GITEE_TOKEN. When BaseURL is empty, the
// driver targets gitee.com (SaaS); otherwise it targets the
// configured self-hosted Gitee Enterprise instance.
func Open(cfg repohost.Config) (repohost.MutableHost, error) {
	if cfg.Provider != "gitee" {
		return nil, fmt.Errorf("gitee: provider mismatch: got %q", cfg.Provider)
	}

	token := cfg.Token
	if token == "" {
		token = os.Getenv("GITEE_TOKEN")
	}

	var (
		client *scm.Client
		err    error
	)
	if cfg.BaseURL == "" {
		client = scmgitee.NewDefault()
	} else {
		client, err = scmgitee.New(cfg.BaseURL)
		if err != nil {
			return nil, fmt.Errorf("gitee: new client: %w", err)
		}
	}

	client.Client = httpClientWithToken(cfg.HTTPClient, token)
	return &Host{cfg: cfg, client: client}, nil
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
