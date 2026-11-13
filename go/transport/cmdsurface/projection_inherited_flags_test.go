package cmdsurface_test

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"hop.top/kit/go/transport/api"
	"hop.top/kit/go/transport/cmdsurface"
)

// inheritedFlagsTree is a tree whose root carries persistent flags:
//
//	root (--scope, --limit, --legacy [deprecated], --internal [hidden])
//	└── item
//	    ├── show (read)
//	    └── put  (write, --scope redeclared locally with another usage)
func inheritedFlagsTree() *cobra.Command {
	root := &cobra.Command{Use: "tool"}
	pf := root.PersistentFlags()
	pf.String("scope", "", "scope to act in")
	pf.Int("limit", 0, "row limit")
	pf.String("legacy", "", "retired flag")
	_ = pf.MarkDeprecated("legacy", "use scope")
	pf.String("internal", "", "operator-only flag")
	_ = pf.MarkHidden("internal")

	say := func(cmd *cobra.Command, _ []string) error {
		scope, _ := cmd.Flags().GetString("scope")
		limit, _ := cmd.Flags().GetInt("limit")
		internal, _ := cmd.Flags().GetString("internal")
		cmd.Printf("scope=%s limit=%d internal=%s\n", scope, limit, internal)
		return nil
	}
	item := &cobra.Command{Use: "item", Short: "Items"}
	show := &cobra.Command{
		Use: "show", Short: "Show an item",
		Annotations: map[string]string{"kit/side-effect": "read"},
		RunE:        say,
	}
	put := &cobra.Command{
		Use: "put", Short: "Put an item",
		Annotations: map[string]string{"kit/side-effect": "write"},
		RunE:        say,
	}
	put.Flags().String("scope", "", "local scope wins")
	item.AddCommand(show, put)
	root.AddCommand(item)
	return root
}

func inheritedFlagsServer(t *testing.T) (string, *cmdsurface.Bridge) {
	t.Helper()
	b := cmdsurface.New(inheritedFlagsTree())
	b.Expose("*", cmdsurface.SurfaceREST)
	r := api.NewRouter()
	if err := cmdsurface.MountProjection(b, r,
		cmdsurface.WithProjectionTool("tool", "1.0.0")); err != nil {
		t.Fatalf("MountProjection: %v", err)
	}
	return serve(t, r), b
}

func TestProjection_PostCarriesInheritedPersistentFlag(t *testing.T) {
	url, _ := inheritedFlagsServer(t)
	status, body := do(t, http.MethodPost, url+"/v1/commands/item/put",
		`{"flags":{"scope":"east","limit":7}}`, nil)
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, body)
	}
	var res api.CommandResult
	if err := json.Unmarshal([]byte(body), &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.Contains(res.Stdout, "scope=east limit=7") {
		t.Errorf("stdout=%q, want the inherited flag values", res.Stdout)
	}
}

func TestProjection_GetCarriesInheritedPersistentFlag(t *testing.T) {
	url, _ := inheritedFlagsServer(t)
	status, body := do(t, http.MethodGet,
		url+"/v1/commands/item/show?scope=west&limit=3", "", nil)
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, body)
	}
	var res api.CommandResult
	if err := json.Unmarshal([]byte(body), &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.Contains(res.Stdout, "scope=west limit=3") {
		t.Errorf("stdout=%q, want the inherited flag values", res.Stdout)
	}
}

func TestProjection_HiddenAndDeprecatedInheritedFlagsStayRefused(t *testing.T) {
	url, _ := inheritedFlagsServer(t)
	for _, name := range []string{"internal", "legacy"} {
		status, body := do(t, http.MethodPost, url+"/v1/commands/item/put",
			`{"flags":{"`+name+`":"x"}}`, nil)
		if status != http.StatusBadRequest || !strings.Contains(body, "unknown flag: "+name) {
			t.Errorf("%s: status=%d body=%s, want 400 unknown flag", name, status, body)
		}
		// A query parameter for a withheld flag is dropped, never forwarded.
		status, body = do(t, http.MethodGet,
			url+"/v1/commands/item/show?"+name+"=x", "", nil)
		if status != http.StatusOK || strings.Contains(body, "internal=x") {
			t.Errorf("%s via query: status=%d body=%s, want the value dropped", name, status, body)
		}
	}
}

func TestProjection_FrameworkFlagsAreNotInvented(t *testing.T) {
	url, _ := inheritedFlagsServer(t)
	for _, name := range []string{"help", "version"} {
		status, body := do(t, http.MethodPost, url+"/v1/commands/item/put",
			`{"flags":{"`+name+`":true}}`, nil)
		if status != http.StatusBadRequest {
			t.Errorf("%s: status=%d body=%s, want 400", name, status, body)
		}
	}
}

// The REST projection publishes the flags the MCP tool schema
// publishes for the same command: visible, local then inherited.
func TestProjection_DiscoveryFlagsMatchMCPSchema(t *testing.T) {
	url, b := inheritedFlagsServer(t)
	status, body := do(t, http.MethodGet, url+"/v1/commands", "", nil)
	if status != http.StatusOK {
		t.Fatalf("discovery: status=%d body=%s", status, body)
	}
	var doc api.DiscoveryDocument
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	rest := map[string][]string{}
	for _, e := range doc.Commands {
		var names []string
		for _, f := range e.Flags {
			names = append(names, f.Name)
		}
		sort.Strings(names)
		rest[strings.Join(e.Path, " ")] = names
	}
	for _, leaf := range b.Leaves() {
		props, _ := cmdsurface.MCPInputSchema(leaf)["properties"].(map[string]any)
		var want []string
		for n := range props {
			want = append(want, n)
		}
		sort.Strings(want)
		got := rest[leaf.PathKey()]
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%s: REST flags %v, MCP flags %v", leaf.PathKey(), got, want)
		}
	}
	if got := strings.Join(rest["item show"], ","); got != "limit,scope" {
		t.Errorf("item show flags = %q, want limit,scope", got)
	}
}

func TestProjection_OpenAPIListsInheritedFlags(t *testing.T) {
	b := cmdsurface.New(inheritedFlagsTree())
	b.Expose("*", cmdsurface.SurfaceREST)
	r := api.NewRouter(api.WithOpenAPI(api.OpenAPIConfig{Title: "t", Version: "0"}))
	if err := cmdsurface.MountProjection(b, r); err != nil {
		t.Fatalf("MountProjection: %v", err)
	}
	status, body := do(t, http.MethodGet, serve(t, r)+api.OpenAPISpecPath, "", nil)
	if status != http.StatusOK {
		t.Fatalf("spec: status=%d", status)
	}
	if !strings.Contains(body, `"scope"`) || !strings.Contains(body, `"limit"`) {
		t.Errorf("spec lacks the inherited flags:\n%s", body)
	}
	for _, hidden := range []string{`"internal"`, `"legacy"`} {
		if strings.Contains(body, hidden) {
			t.Errorf("spec lists withheld flag %s", hidden)
		}
	}
}
