package output

import (
	"bytes"
	"encoding/json"

	"gopkg.in/yaml.v3"
)

// orderedEntry is one key/value pair of an orderedMap.
type orderedEntry struct {
	Key   string
	Value any
}

// orderedMap is a JSON/YAML object whose keys serialize in slice order
// rather than sorted order.
//
// Go's encoding/json and yaml.v3 both sort map[string]any keys on output,
// so a map cannot carry the user's --cols order to json/yaml the way
// filterColumns carries it to table/csv/text. This type is that carrier:
// an ordered slice of pairs with explicit marshalers.
//
// The two encoders need different mechanisms. encoding/json honors
// MarshalJSON, which must return the complete object bytes — indentation
// is reapplied afterwards by the enclosing Encoder, so emitting compact
// bytes here is correct. yaml.v3 has no MarshalYAML-to-bytes form; it
// honors MarshalYAML returning a value to encode in this node's place, and
// only a *yaml.Node of Kind MappingNode preserves pair order (returning a
// map would re-sort). Both are verified by the round-trip tests in
// orderedmap_test.go.
type orderedMap []orderedEntry

var (
	_ json.Marshaler = orderedMap(nil)
	_ yaml.Marshaler = orderedMap(nil)
)

// MarshalJSON emits a compact JSON object with keys in slice order.
func (o orderedMap) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, e := range o {
		if i > 0 {
			buf.WriteByte(',')
		}
		key, err := json.Marshal(e.Key)
		if err != nil {
			return nil, err
		}
		buf.Write(key)
		buf.WriteByte(':')
		val, err := json.Marshal(e.Value)
		if err != nil {
			return nil, err
		}
		buf.Write(val)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// MarshalYAML returns a mapping node whose content is in slice order.
func (o orderedMap) MarshalYAML() (any, error) {
	node := &yaml.Node{Kind: yaml.MappingNode}
	for _, e := range o {
		var key, val yaml.Node
		if err := key.Encode(e.Key); err != nil {
			return nil, err
		}
		if err := val.Encode(e.Value); err != nil {
			return nil, err
		}
		node.Content = append(node.Content, &key, &val)
	}
	return node, nil
}
