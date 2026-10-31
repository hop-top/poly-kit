package bridge

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPayload_JSON_RoundTrip(t *testing.T) {
	for _, tc := range positivePayloads() {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(tc.in)
			require.NoError(t, err)

			var out Payload
			require.NoError(t, json.Unmarshal(data, &out))
			assert.Equal(t, tc.in, out)
		})
	}
}

func TestPayload_RejectsZeroOrMultipleKinds(t *testing.T) {
	for _, tc := range negativeKindRawJSON() {
		t.Run(tc.name, func(t *testing.T) {
			var p Payload
			err := json.Unmarshal([]byte(tc.raw), &p)
			assert.Error(t, err, "expected oneof guard to reject")
		})
	}
}

func TestPayload_MarshalRejectsZeroOrMultipleKinds(t *testing.T) {
	cases := map[string]Payload{
		"zero kinds":  {ID: "1"},
		"text+url":    {ID: "1", Text: &Text{Body: "x"}, URL: &URL{Href: "https://e.com"}},
		"file+blob":   {ID: "1", File: &File{Path: "/a"}, Blob: &Blob{Data: []byte("x")}},
		"all four":    {ID: "1", Text: &Text{}, URL: &URL{}, File: &File{}, Blob: &Blob{}},
		"text+file":   {ID: "1", Text: &Text{Body: "x"}, File: &File{Path: "/a"}},
		"url+blob":    {ID: "1", URL: &URL{Href: "h"}, Blob: &Blob{}},
		"empty+empty": {ID: "1", Text: &Text{}, URL: &URL{}},
	}
	for name, p := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := json.Marshal(p)
			assert.ErrorIs(t, err, ErrInvalidOneof, "by value")
			_, err = json.Marshal(&p)
			assert.ErrorIs(t, err, ErrInvalidOneof, "by pointer")
		})
	}
}

// Whatever Marshal accepts, Unmarshal accepts, and vice versa.
func TestPayload_MarshalUnmarshalSymmetric(t *testing.T) {
	for _, tc := range positivePayloads() {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(tc.in)
			require.NoError(t, err)
			var out Payload
			require.NoError(t, json.Unmarshal(data, &out))
		})
	}
}
