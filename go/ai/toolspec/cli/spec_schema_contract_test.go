package cli

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/toolspec"
	kitcli "hop.top/kit/go/console/cli"
)

// updateSpecSchemaContract regenerates the reflected output schemas
// the spec and spec coverage commands carry:
//
//	go test ./go/ai/toolspec/cli/ -run TestSpecOutputSchemaContract \
//	    -update-spec-schema-contract
var updateSpecSchemaContract = flag.Bool("update-spec-schema-contract", false,
	"rewrite contracts/toolspec-spec-v1/*.schema.json")

var specSchemaContractDir = filepath.Join("..", "..", "..", "..", "contracts", "toolspec-spec-v1")

// TestSpecOutputSchemaContract pins the kit/output-schema bytes Go
// reflects for toolspec.Manifest and CoverageReport. SDKs without
// reflection embed these files verbatim so their spec entries match.
func TestSpecOutputSchemaContract(t *testing.T) {
	for _, tc := range []struct {
		file string
		typ  any
	}{
		{"manifest.schema.json", &toolspec.Manifest{}},
		{"coverage-report.schema.json", &CoverageReport{}},
	} {
		t.Run(tc.file, func(t *testing.T) {
			cmd := &cobra.Command{Use: "x"}
			require.NoError(t, kitcli.SetOutputSchema(cmd, kitcli.OutputSchema{Type: tc.typ, Version: "1.0"}))
			got := cmd.Annotations["kit/output-schema"]
			path := filepath.Join(specSchemaContractDir, tc.file)
			if *updateSpecSchemaContract {
				require.NoError(t, os.MkdirAll(specSchemaContractDir, 0o755))
				require.NoError(t, os.WriteFile(path, []byte(got), 0o644))
				return
			}
			want, err := os.ReadFile(path)
			require.NoError(t, err, "regenerate with -update-spec-schema-contract")
			require.Equal(t, string(want), got)
		})
	}
}
