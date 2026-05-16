// Command notebook is a personal note-taking CLI exercising kit's packages.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/runtime/domain"
	dsqlite "hop.top/kit/go/runtime/domain/sqlite"
	"hop.top/kit/go/runtime/domain/version"
	"hop.top/kit/go/runtime/sync"
	"hop.top/kit/go/storage/sqlstore"
	"hop.top/kit/go/transport/api"
)

// Note is the primary domain entity.
type Note struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

func (n Note) GetID() string { return n.ID }

// noteService adapts VersionedRepository[Note] to api.Service[Note].
type noteService struct {
	vr *version.VersionedRepository[Note]
}

func (ns *noteService) Create(ctx context.Context, n Note) (Note, error) {
	if n.ID == "" {
		// 48 bits entropy (~16M items before birthday collision risk).
		n.ID = uuid.New().String()[:12]
	}
	now := time.Now().UTC().Format(time.RFC3339)
	n.CreatedAt = now
	n.UpdatedAt = now
	if err := ns.vr.Create(ctx, &n); err != nil {
		return Note{}, err
	}
	return n, nil
}

func (ns *noteService) Get(ctx context.Context, id string) (Note, error) {
	n, err := ns.vr.Get(ctx, id)
	if err != nil {
		return Note{}, err
	}
	return *n, nil
}

func (ns *noteService) List(ctx context.Context, q domain.Query) ([]Note, error) {
	return ns.vr.List(ctx, q)
}

func (ns *noteService) Update(ctx context.Context, n Note) (Note, error) {
	n.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := ns.vr.Update(ctx, &n); err != nil {
		return Note{}, err
	}
	return n, nil
}

func (ns *noteService) Delete(ctx context.Context, id string) error {
	return ns.vr.Delete(ctx, id)
}

const migrateSQL = `CREATE TABLE IF NOT EXISTS notes (
  id         TEXT PRIMARY KEY,
  title      TEXT NOT NULL,
  body       TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);`

func scanNote(row *sql.Row) (Note, error) {
	var n Note
	err := row.Scan(&n.ID, &n.Title, &n.Body, &n.CreatedAt, &n.UpdatedAt)
	return n, err
}

func scanNoteRows(rows *sql.Rows) (Note, error) {
	var n Note
	err := rows.Scan(&n.ID, &n.Title, &n.Body, &n.CreatedAt, &n.UpdatedAt)
	return n, err
}

func bindNote(n Note) ([]string, []any) {
	return []string{"id", "title", "body", "created_at", "updated_at"},
		[]any{n.ID, n.Title, n.Body, n.CreatedAt, n.UpdatedAt}
}

func dataDir() string {
	xdg := os.Getenv("XDG_DATA_HOME")
	if xdg == "" {
		home, _ := os.UserHomeDir()
		xdg = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(xdg, "notebook")
}

func main() {
	dbPath := filepath.Join(dataDir(), "notebook.db")
	store, err := sqlstore.Open(dbPath, sqlstore.Options{MigrateSQL: migrateSQL})
	if err != nil {
		fmt.Fprintf(os.Stderr, "open db: %v\n", err)
		os.Exit(1)
	}

	repo := dsqlite.NewSQLiteRepository[Note](store, "notes", scanNote, scanNoteRows, bindNote)
	vr := version.NewVersionedRepository[Note](repo)
	ns := &noteService{vr: vr}
	repl := sync.NewReplicator[Note](vr)

	root := cli.New(cli.Config{
		Name:    "notebook",
		Version: "0.1.0",
		Short:   "Personal note-taking CLI powered by kit",
	},
		cli.WithIdentity(cli.IdentityConfig{}),
		cli.WithPeers(cli.PeerConfig{Service: "_notebook._tcp"}),
		cli.WithAPI(cli.APIConfig{
			Addr: ":8080",
			OpenAPI: &api.OpenAPIConfig{
				Title:   "Notebook API",
				Version: "0.1.0",
			},
			Resources: func(r *api.Router, humaAPI interface{}) {
				r.Mount("/notes", api.ResourceRouter[Note](ns,
					api.WithHumaAPI[Note](api.HumaAPI(r), "/notes"),
				))
			},
		}),
	)

	root.Cmd.AddCommand(
		newCmd(ns),
		editCmd(ns),
		listCmd(ns),
		getCmd(ns),
		deleteCmd(ns),
		historyCmd(vr),
		revertCmd(vr),
		syncCmd(repl),
	)

	if err := root.Execute(context.Background()); err != nil {
		os.Exit(1)
	}
}

func newCmd(ns api.Service[Note]) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "new <title>",
		Short: "Create a note",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			body, _ := cmd.Flags().GetString("body")
			n, err := ns.Create(cmd.Context(), Note{Title: args[0], Body: body})
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Created note %s\n", n.ID)
			return nil
		},
	}
	cmd.Flags().String("body", "", "Note body")
	return cmd
}

func editCmd(ns api.Service[Note]) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "edit <id>",
		Short: "Edit a note",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			n, err := ns.Get(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if t, _ := cmd.Flags().GetString("title"); t != "" {
				n.Title = t
			}
			if b, _ := cmd.Flags().GetString("body"); b != "" {
				n.Body = b
			}
			if _, err := ns.Update(cmd.Context(), n); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Updated note %s\n", n.ID)
			return nil
		},
	}
	cmd.Flags().String("title", "", "New title")
	cmd.Flags().String("body", "", "New body")
	return cmd
}

func listCmd(ns api.Service[Note]) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List notes",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			search, _ := cmd.Flags().GetString("search")
			limit, _ := cmd.Flags().GetInt("limit")
			notes, err := ns.List(cmd.Context(), domain.Query{
				Search: search,
				Limit:  limit,
			})
			if err != nil {
				return err
			}
			if len(notes) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No notes found.")
				return nil
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tTITLE\tUPDATED")
			for _, n := range notes {
				fmt.Fprintf(w, "%s\t%s\t%s\n", n.ID, n.Title, n.UpdatedAt)
			}
			return w.Flush()
		},
	}
	cmd.Flags().String("search", "", "Search query")
	cmd.Flags().Int("limit", 20, "Max results")
	return cmd
}

func getCmd(ns api.Service[Note]) *cobra.Command {
	return &cobra.Command{
		Use:   "get <id>",
		Short: "Show a note",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			n, err := ns.Get(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "ID:      %s\nTitle:   %s\nBody:    %s\nCreated: %s\nUpdated: %s\n",
				n.ID, n.Title, n.Body, n.CreatedAt, n.UpdatedAt)
			return nil
		},
	}
}

func deleteCmd(ns api.Service[Note]) *cobra.Command {
	return &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a note",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := ns.Delete(cmd.Context(), args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Deleted note %s\n", args[0])
			return nil
		},
	}
}

func historyCmd(vr *version.VersionedRepository[Note]) *cobra.Command {
	return &cobra.Command{
		Use:   "history <id>",
		Short: "List versions of a note",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			versions := vr.ListVersions(args[0])
			if len(versions) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No versions found.")
				return nil
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "VERSION\tTIMESTAMP\tHASH")
			for _, v := range versions {
				ts := time.Unix(0, v.Timestamp).UTC().Format(time.RFC3339)
				fmt.Fprintf(w, "%s\t%s\t%s\n", v.ID, ts, v.Hash[:12])
			}
			return w.Flush()
		},
	}
}

func revertCmd(vr *version.VersionedRepository[Note]) *cobra.Command {
	return &cobra.Command{
		Use:   "revert <id> <version>",
		Short: "Revert a note to a previous version",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := vr.Revert(cmd.Context(), args[0], args[1]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Reverted note %s to version %s\n", args[0], args[1])
			return nil
		},
	}
}

func syncCmd(repl *sync.Replicator[Note]) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Manage sync remotes",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(
		syncAddCmd(repl),
		syncRemoveCmd(repl),
		syncStatusCmd(repl),
	)
	return cmd
}

func syncAddCmd(repl *sync.Replicator[Note]) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add <name> <url>",
		Short: "Add a sync remote",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			modeStr, _ := cmd.Flags().GetString("mode")
			mode := sync.Bidirectional
			switch strings.ToLower(modeStr) {
			case "push":
				mode = sync.PushOnly
			case "pull":
				mode = sync.PullOnly
			}
			transport := sync.NewHTTPTransport(args[1])
			rem := sync.Remote{
				Name:      args[0],
				Transport: transport,
				Mode:      mode,
			}
			if err := repl.AddRemote(rem); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Added remote %s (%s)\n", args[0], args[1])
			return nil
		},
	}
	cmd.Flags().String("mode", "both", "Sync mode: push, pull, or both")
	return cmd
}

func syncRemoveCmd(repl *sync.Replicator[Note]) *cobra.Command {
	return &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove a sync remote",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := repl.RemoveRemote(args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Removed remote %s\n", args[0])
			return nil
		},
	}
}

func syncStatusCmd(repl *sync.Replicator[Note]) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show sync remote health",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			statuses := repl.Status()
			if len(statuses) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No remotes configured.")
				return nil
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tCONNECTED\tPENDING\tLAST SYNC\tERROR")
			for _, s := range statuses {
				errStr := "-"
				if s.LastError != nil {
					errStr = s.LastError.Error()
				}
				lastSync := "-"
				if !s.LastSync.IsZero() {
					lastSync = s.LastSync.Format(time.RFC3339)
				}
				fmt.Fprintf(w, "%s\t%v\t%d\t%s\t%s\n",
					s.Name, s.Connected, s.PendingDiffs, lastSync, errStr)
			}
			return w.Flush()
		},
	}
}
