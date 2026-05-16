package bridge

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v5"
	"github.com/stretchr/testify/require"
)

// schemaPath resolves the hand-written JSON Schema relative to this test
// file. Going up two dirs from go/bridge/ lands in the repo's hops/main/
// root where contracts/ lives.
func schemaPath(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	require.NoError(t, err)
	return filepath.Join(wd, "..", "..", "contracts", "bridge.schema.json")
}

func loadSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	p := schemaPath(t)
	data, err := os.ReadFile(p)
	require.NoError(t, err, "read schema %s", p)

	c := jsonschema.NewCompiler()
	require.NoError(t, c.AddResource("bridge.schema.json", bytes.NewReader(data)))
	s, err := c.Compile("bridge.schema.json")
	require.NoError(t, err, "compile schema")
	return s
}

// validateRaw decodes JSON then runs schema validation. Mirrors how
// non-Go shells (Swift Share Extension) will use the schema.
func validateRaw(t *testing.T, s *jsonschema.Schema, raw []byte) error {
	t.Helper()
	var v interface{}
	require.NoError(t, json.Unmarshal(raw, &v))
	return s.Validate(v)
}

// TestSchema_Accepts_GoMarshalledFixtures runs the same positive fixtures
// from payload_test.go through the JSON Schema validator. Asserts the
// schema validates the exact bytes the Go types produce.
func TestSchema_Accepts_GoMarshalledFixtures(t *testing.T) {
	s := loadSchema(t)
	for _, tc := range positivePayloads() {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(tc.in)
			require.NoError(t, err)
			require.NoError(t, validateRaw(t, s, data), "schema must accept marshaled %s", tc.name)
		})
	}
}

// TestSchema_RejectsZeroOrMultipleKinds mirrors TestPayload_RejectsZeroOrMultipleKinds.
// Schema oneOf must catch payloads with no kind and payloads with >1 kind.
func TestSchema_RejectsZeroOrMultipleKinds(t *testing.T) {
	s := loadSchema(t)
	for _, tc := range negativeKindRawJSON() {
		t.Run(tc.name, func(t *testing.T) {
			require.Error(t, validateRaw(t, s, []byte(tc.raw)), "schema must reject %s", tc.name)
		})
	}
}

// TestSchema_RejectsUnknownProperties asserts additionalProperties:false
// catches typo'd or sneaky fields that aren't part of the wire format.
// Negative shape: a valid url payload + an unknown nested property.
func TestSchema_RejectsUnknownProperties(t *testing.T) {
	s := loadSchema(t)
	raw := `{"id":"x","source":"s","timestamp":1,` +
		`"url":{"href":"https://e.io","_extra":"nope"}}`
	require.Error(t, validateRaw(t, s, []byte(raw)),
		"schema must reject unknown property in url")
}

// TestSchema_RejectsNonBase64BlobData asserts the base64 pattern on
// Blob.data actually rejects garbage. contentEncoding alone is annotation,
// not enforcement.
func TestSchema_RejectsNonBase64BlobData(t *testing.T) {
	s := loadSchema(t)
	raw := `{"id":"x","source":"s","timestamp":1,` +
		`"blob":{"data":"!!!not-base64!!!","mime":"image/png"}}`
	require.Error(t, validateRaw(t, s, []byte(raw)),
		"schema must reject non-base64 blob.data")
}
