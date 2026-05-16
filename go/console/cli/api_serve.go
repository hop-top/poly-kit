package cli

import (
	"fmt"
	"net/http"

	"github.com/spf13/cobra"
	kitlog "hop.top/kit/go/console/log"
	"hop.top/kit/go/transport/api"
)

func serveCmd(root *Root) *cobra.Command {
	cfg := root.apiCfg

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the HTTP API server",
		RunE: func(cmd *cobra.Command, _ []string) error {
			addr, _ := cmd.Flags().GetString("addr")
			noAuth, _ := cmd.Flags().GetBool("no-auth")

			logger := kitlog.New(root.Viper)

			// Build middleware stack.
			mws := []api.Middleware{
				api.RequestID(),
				api.Logger(logger.Info),
				api.Recovery(func(v any, r *http.Request) {
					logger.Error("panic recovered",
						"error", v,
						"path", r.URL.Path,
					)
				}),
				api.ContentType("application/json"),
			}

			if cfg.Auth != nil && !noAuth {
				mws = append(mws, api.Auth(cfg.Auth))
			}

			// Build router.
			opts := []api.RouterOption{api.WithMiddleware(mws...)}
			if cfg.OpenAPI != nil {
				opts = append(opts, api.WithOpenAPI(*cfg.OpenAPI))
			}
			router := api.NewRouter(opts...)

			// Custom routes.
			if cfg.Handlers != nil {
				cfg.Handlers(router)
			}

			// Resource routes.
			if cfg.Resources != nil {
				cfg.Resources(router, api.HumaAPI(router))
			}

			// WebSocket hub.
			if cfg.OnHub != nil {
				hub := api.NewHub()
				go hub.Run(cmd.Context())
				router.Handle("GET", "/ws", api.WSHandler(hub))
				cfg.OnHub(hub)
			}

			fmt.Fprintf(cmd.ErrOrStderr(), "Listening on %s\n", addr)
			return api.ListenAndServe(cmd.Context(), addr, router)
		},
	}

	cmd.Flags().String("addr", cfg.Addr, "Listen address")
	if cfg.Auth != nil {
		cmd.Flags().Bool("no-auth", false, "Disable authentication")
	}

	return cmd
}
