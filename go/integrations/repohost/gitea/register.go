package gitea

import (
	"fmt"
	"net/http"
	"os"
	"strings"

	scmgitea "github.com/drone/go-scm/scm/driver/gitea"
	"github.com/drone/go-scm/scm/transport"

	"hop.top/kit/go/integrations/repohost"
)

func init() {
	repohost.RegisterDriver("gitea", Open)
}

// Open constructs a Gitea [repohost.MutableHost] from cfg. Provider
// must be "gitea". BaseURL is required (Gitea has no SaaS default).
// Token resolution order: Config.Token, then GITEA_TOKEN.
func Open(cfg repohost.Config) (repohost.MutableHost, error) {
	if cfg.Provider != "gitea" {
		return nil, fmt.Errorf("gitea: provider mismatch: got %q", cfg.Provider)
	}
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("gitea: BaseURL is required (Gitea has no SaaS default)")
	}

	token := cfg.Token
	if token == "" {
		token = os.Getenv("GITEA_TOKEN")
	}

	client, err := scmgitea.New(cfg.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("gitea: new client: %w", err)
	}

	client.Client = httpClientWithToken(cfg.HTTPClient, token)
	return &Host{
		cfg:     cfg,
		client:  client,
		baseURL: strings.TrimRight(cfg.BaseURL, "/"),
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
