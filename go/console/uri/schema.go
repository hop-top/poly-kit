package uri

import (
	"encoding/json"

	"github.com/invopop/jsonschema"
	"github.com/spf13/cobra"

	"hop.top/kit/go/console/cli/cmdmeta"
)

// outputSchemaVersion is the MAJOR.MINOR every uri leaf declares for
// the shape it renders under --format json|yaml|table. Bump it with
// any change to the row types in types.go.
const outputSchemaVersion = "1.0"

// setOutputSchema declares the structured-output schema of cmd as the
// JSON Schema reflected from v.
//
// It writes the same kit/output-schema + kit/output-schema-version
// annotations cli.SetOutputSchema writes, with the same reflector.
// This package cannot call cli.SetOutputSchema: cli mounts this tree
// (cli.WithURI), so importing cli would close a cycle.
func setOutputSchema(cmd *cobra.Command, v any) {
	setOutputSchemaDoc(cmd, jsonschema.Reflect(v))
}

// setOutputSchemaAnyOf declares a schema satisfied by any one of the
// reflected shapes of vs — for a leaf whose flags pick which of
// several row types it renders.
func setOutputSchemaAnyOf(cmd *cobra.Command, vs ...any) {
	r := &jsonschema.Reflector{DoNotReference: true, Anonymous: true}
	doc := &jsonschema.Schema{Version: jsonschema.Version}
	for _, v := range vs {
		s := r.Reflect(v)
		s.Version = ""
		doc.AnyOf = append(doc.AnyOf, s)
	}
	setOutputSchemaDoc(cmd, doc)
}

func setOutputSchemaDoc(cmd *cobra.Command, doc *jsonschema.Schema) {
	raw, err := json.Marshal(doc)
	if err != nil || len(raw) == 0 || string(raw) == "null" {
		return
	}
	setAnnotation(cmd, cmdmeta.KeyOutputSchema, string(raw))
	setAnnotation(cmd, cmdmeta.KeyOutputSchemaVersion, outputSchemaVersion)
}
