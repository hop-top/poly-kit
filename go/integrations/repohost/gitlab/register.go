package gitlab

import (
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/drone/go-scm/scm"
	scmgitlab "github.com/drone/go-scm/scm/driver/gitlab"
	"github.com/drone/go-scm/scm/transport"

	"hop.top/kit/go/integrations/repohost"
)

// defaultBaseURL is the SaaS GitLab host root used when
// [repohost.Config.BaseURL] is empty.
const defaultBaseURL = "https://gitlab.com"

func init() {
	repohost.RegisterDriver("gitlab", Open)
}

// Open constructs a GitLab MutableHost from cfg. Token resolution
// order: cfg.Token, then GITLAB_TOKEN. When BaseURL is empty, the
// driver targets gitlab.com (SaaS); otherwise it targets the
// configured self-hosted instance.
func Open(cfg repohost.Config) (repohost.MutableHost, error) {
	if cfg.Provider != "gitlab" {
		return nil, fmt.Errorf("gitlab: provider mismatch: got %q", cfg.Provider)
	}
	base := cfg.BaseURL
	if base == "" {
		base = defaultBaseURL
	}

	token := cfg.Token
	if token == "" {
		token = os.Getenv("GITLAB_TOKEN")
	}

	var (
		client *scm.Client
		err    error
	)
	if cfg.BaseURL == "" {
		client = scmgitlab.NewDefault()
	} else {
		client, err = scmgitlab.New(base)
		if err != nil {
			return nil, fmt.Errorf("gitlab: new client: %w", err)
		}
	}

	client.Client = httpClientWithToken(cfg.HTTPClient, token)
	return &Host{cfg: cfg, client: client, baseURL: strings.TrimRight(base, "/")}, nil
}

// httpClientWithToken wraps base's transport with GitLab's
// PrivateToken round-tripper. PATs and project/group access tokens
// are accepted as PRIVATE-TOKEN; OAuth tokens go through Bearer.
// We pick PrivateToken which is the conventional GitLab path; users
// that need OAuth can set Config.HTTPClient with their own
// pre-authorized transport.
func httpClientWithToken(base *http.Client, token string) *http.Client {
	if base == nil {
		base = &http.Client{}
	}
	if token == "" {
		return base
	}
	out := *base
	out.Transport = &transport.PrivateToken{
		Base:  base.Transport,
		Token: token,
	}
	return &out
}
