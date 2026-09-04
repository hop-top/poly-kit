package output

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// Keys chosen so slice order disagrees with both alphabetical order and any
// plausible insertion-independent ordering.
func sampleOrdered() orderedMap {
	return orderedMap{
		{Key: "zeta", Value: "z"},
		{Key: "alpha", Value: 1},
		{Key: "mid", Value: true},
	}
}

func TestOrderedMap_MarshalJSON_PreservesSliceOrder(t *testing.T) {
	got, err := json.Marshal(sampleOrdered())
	require.NoError(t, err)
	assert.JSONEq(t, `{"zeta":"z","alpha":1,"mid":true}`, string(got))
	assert.Equal(t, `{"zeta":"z","alpha":1,"mid":true}`, string(got),
		"byte order, not just JSON equivalence")
}

func TestOrderedMap_MarshalJSON_ThroughEncoderIndent(t *testing.T) {
	// The enclosing Encoder reapplies indentation to MarshalJSON's compact
	// bytes; assert that the two compose as expected.
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	require.NoError(t, enc.Encode(sampleOrdered()))
	assert.Equal(t,
		"{\n  \"zeta\": \"z\",\n  \"alpha\": 1,\n  \"mid\": true\n}\n", buf.String())
}

func TestOrderedMap_MarshalYAML_PreservesSliceOrder(t *testing.T) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	require.NoError(t, enc.Encode(sampleOrdered()))
	require.NoError(t, enc.Close())
	assert.Equal(t, "zeta: z\nalpha: 1\nmid: true\n", buf.String())
}

func TestOrderedMap_Empty(t *testing.T) {
	got, err := json.Marshal(orderedMap{})
	require.NoError(t, err)
	assert.Equal(t, "{}", string(got))

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	require.NoError(t, enc.Encode(orderedMap{}))
	require.NoError(t, enc.Close())
	assert.Equal(t, "{}\n", buf.String())
}

func TestOrderedMap_NestedValues(t *testing.T) {
	o := orderedMap{
		{Key: "outer", Value: orderedMap{{Key: "zz", Value: 1}, {Key: "aa", Value: 2}}},
		{Key: "list", Value: []string{"one", "two"}},
	}
	got, err := json.Marshal(o)
	require.NoError(t, err)
	assert.Equal(t, `{"outer":{"zz":1,"aa":2},"list":["one","two"]}`, string(got))

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	require.NoError(t, enc.Encode(o))
	require.NoError(t, enc.Close())
	assert.Equal(t, "outer:\n    zz: 1\n    aa: 2\nlist:\n    - one\n    - two\n", buf.String())
}

func TestOrderedMap_MarshalErrorPropagates(t *testing.T) {
	o := orderedMap{{Key: "bad", Value: make(chan int)}}
	_, err := json.Marshal(o)
	require.Error(t, err)
}
