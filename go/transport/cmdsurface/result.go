package cmdsurface

import (
	"encoding/json"
	"io"
	"strings"
)

// decodeStructured decodes stdout as the one JSON document the output
// package's json formatter writes for a command that declares an
// output schema. Numbers keep their exact text (json.Number), so a
// payload re-encoded by a transport is byte-for-byte the CLI's own.
//
// The second result is false when stdout is empty, is not JSON, or
// carries anything after the document: the runner then leaves
// Result.Data nil rather than guess, and the streams say what the
// command actually wrote.
func decodeStructured(stdout string) (any, bool) {
	if strings.TrimSpace(stdout) == "" {
		return nil, false
	}
	dec := json.NewDecoder(strings.NewReader(stdout))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, false
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, false
	}
	return v, true
}
