// cobratree.go defines the example's cobra command tree. It is kept
// out of main.go so the e2e suite can rebuild a fresh tree per test
// without touching the entry point.
package main

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"
)

// buildCobraTree returns the example's command tree:
//
//	widget
//	├── add     [safe]          --name (required) --tag (stringSlice)
//	├── list    [safe]          --tag (filter)
//	├── get     [safe]          <id>
//	└── delete  [destructive]   <id>
//	report
//	├── generate [safe]    --from --to
//	└── purge    [destructive, auth-required]    --before
//	subscription
//	└── cancel  [destructive]   <id>
//	auth
//	└── oauth-link [safe]       --provider --code --state
//	notify
//	└── message [safe]          --source --title
//	ping        [safe]
//	tick        [safe]          --count int=3 --interval string=100ms
//
// `tick` is the multi-event leaf the streaming surfaces (WS / SSE /
// RPC-stream / Bus) demonstrate against. It prints "tick i=<n>" lines
// with a sleep between, so streaming clients observe each frame.
//
// `subscription cancel`, `auth oauth-link`, and `notify message` are
// the Wave 3 demo leaves the Signed-URL, OAuth-callback, and Webhook
// surfaces target.
func buildCobraTree() *cobra.Command {
	root := &cobra.Command{Use: "cmdsurface-example", Short: "cmdsurface demo"}

	// widget
	widget := &cobra.Command{Use: "widget", Short: "Manage widgets"}

	var addName string
	var addTags []string
	widgetAdd := &cobra.Command{
		Use:   "add",
		Short: "Add a widget",
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintf(cmd.OutOrStdout(), "widget add: name=%s tags=%v\n", addName, addTags)
			return nil
		},
	}
	widgetAdd.Flags().StringVar(&addName, "name", "", "widget name (required)")
	widgetAdd.Flags().StringSliceVar(&addTags, "tag", nil, "widget tag (repeatable)")
	_ = widgetAdd.MarkFlagRequired("name")

	var listTag string
	widgetList := &cobra.Command{
		Use:   "list",
		Short: "List widgets",
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintf(cmd.OutOrStdout(), "widget list: tag=%s\n", listTag)
			return nil
		},
	}
	widgetList.Flags().StringVar(&listTag, "tag", "", "filter by tag")

	widgetGet := &cobra.Command{
		Use:   "get <id>",
		Short: "Get a widget by id",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintf(cmd.OutOrStdout(), "widget get: id=%s\n", args[0])
			return nil
		},
	}

	widgetDelete := &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a widget",
		Args:  cobra.ExactArgs(1),
		Annotations: map[string]string{
			"kit/side-effect": "destructive",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintf(cmd.OutOrStdout(), "widget delete: id=%s\n", args[0])
			return nil
		},
	}

	widget.AddCommand(widgetAdd, widgetList, widgetGet, widgetDelete)

	// report
	report := &cobra.Command{Use: "report", Short: "Reporting commands"}

	var genFrom, genTo string
	reportGenerate := &cobra.Command{
		Use:   "generate",
		Short: "Generate a report",
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintf(cmd.OutOrStdout(), "report generate: from=%s to=%s\n", genFrom, genTo)
			return nil
		},
	}
	reportGenerate.Flags().StringVar(&genFrom, "from", "yesterday", "start date")
	reportGenerate.Flags().StringVar(&genTo, "to", "today", "end date")

	var purgeBefore string
	reportPurge := &cobra.Command{
		Use:   "purge",
		Short: "Purge old reports",
		Annotations: map[string]string{
			"kit/side-effect":   "destructive",
			"kit/auth-required": "true",
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintf(cmd.OutOrStdout(), "report purge: before=%s\n", purgeBefore)
			return nil
		},
	}
	reportPurge.Flags().StringVar(&purgeBefore, "before", "", "purge entries before this date")

	report.AddCommand(reportGenerate, reportPurge)

	// ping
	ping := &cobra.Command{
		Use:   "ping",
		Short: "Sanity ping that prints pong",
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), "pong")
			return nil
		},
	}

	// tick — streaming-friendly demo command. Prints N lines, sleeping
	// `interval` between each, so the WS / SSE / RPC-stream surfaces
	// have a multi-event payload to forward and clients can demonstrate
	// cancellation against a long-lived run.
	var tickCount int
	var tickInterval string
	tick := &cobra.Command{
		Use:   "tick",
		Short: "Print N lines with a delay between (streaming demo)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			d, err := time.ParseDuration(tickInterval)
			if err != nil {
				return fmt.Errorf("parse interval: %w", err)
			}
			out := cmd.OutOrStdout()
			ctx := cmd.Context()
			for i := 1; i <= tickCount; i++ {
				fmt.Fprintf(out, "tick i=%d\n", i)
				if i == tickCount {
					break
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(d):
				}
			}
			return nil
		},
	}
	tick.Flags().IntVar(&tickCount, "count", 3, "number of ticks to emit")
	tick.Flags().StringVar(&tickInterval, "interval", "100ms", "delay between ticks (Go duration)")

	// subscription — destructive `cancel` leaf used by the Signed-URL
	// surface demo ("click this link to cancel"). Locked to CLI+Lib
	// by default; the signed-URL tests opt SurfaceSigned in via the
	// BuildExample WithAllowDestructiveOn option.
	subscription := &cobra.Command{Use: "subscription", Short: "Manage subscriptions"}
	subscriptionCancel := &cobra.Command{
		Use:   "cancel <id>",
		Short: "Cancel a subscription",
		Args:  cobra.ExactArgs(1),
		Annotations: map[string]string{
			"kit/side-effect": "destructive",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintf(cmd.OutOrStdout(), "subscription cancel: id=%s\n", args[0])
			return nil
		},
	}
	subscription.AddCommand(subscriptionCancel)

	// auth — safe `oauth-link` leaf used as the OAuth callback target.
	auth := &cobra.Command{Use: "auth", Short: "Auth-related commands"}
	var oauthProvider, oauthCode, oauthState string
	authOAuthLink := &cobra.Command{
		Use:   "oauth-link",
		Short: "Record an OAuth link (callback target)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintf(cmd.OutOrStdout(),
				"auth oauth-link: provider=%s code=%s state=%s\n",
				oauthProvider, oauthCode, oauthState)
			return nil
		},
	}
	authOAuthLink.Flags().StringVar(&oauthProvider, "provider", "", "OAuth provider name")
	authOAuthLink.Flags().StringVar(&oauthCode, "code", "", "OAuth authorization code")
	authOAuthLink.Flags().StringVar(&oauthState, "state", "", "OAuth state value")
	auth.AddCommand(authOAuthLink)

	// notify — safe `message` leaf used as the Webhook target.
	notify := &cobra.Command{Use: "notify", Short: "Notification commands"}
	var notifySource, notifyTitle string
	notifyMessage := &cobra.Command{
		Use:   "message",
		Short: "Record an inbound notification (webhook target)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintf(cmd.OutOrStdout(),
				"notify message: source=%s title=%s\n",
				notifySource, notifyTitle)
			return nil
		},
	}
	notifyMessage.Flags().StringVar(&notifySource, "source", "", "notification source")
	notifyMessage.Flags().StringVar(&notifyTitle, "title", "", "notification title")
	notify.AddCommand(notifyMessage)

	root.AddCommand(widget, report, subscription, auth, notify, ping, tick)
	return root
}
