package adapters_test

import (
	"os"

	"hop.top/kit/go/ai/toolspec"
	"hop.top/kit/go/ai/toolspec/adapters"
)

func Example() {
	spec := &toolspec.ToolSpec{
		Name:          "demo",
		SchemaVersion: "1.0",
		Commands:      []toolspec.Command{{Name: "list"}},
	}
	if err := adapters.KitManifest().Render(os.Stdout, spec); err != nil {
		panic(err)
	}
	// Output:
	// {
	//   "tool": "demo",
	//   "version": "",
	//   "schema_version": "1.0",
	//   "commands": [
	//     {
	//       "path": [
	//         "demo",
	//         "list"
	//       ],
	//       "short": "",
	//       "side_effect": "",
	//       "idempotent": "",
	//       "retryable": false,
	//       "dry_run_supported": false
	//     }
	//   ]
	// }
}
