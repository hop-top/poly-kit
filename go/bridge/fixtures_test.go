package bridge

// Shared fixtures consumed by both payload_test.go (Go round-trip) and
// schema_test.go (JSON Schema validation). Single source of truth — keeps
// the Go behavior and the schema authority aligned.

// positivePayload is one canonical valid Payload fixture.
type positivePayload struct {
	name string
	in   Payload
}

// positivePayloads returns the canonical valid Payload fixtures used by
// both Go round-trip tests and JSON Schema validation tests.
func positivePayloads() []positivePayload {
	return []positivePayload{
		{
			name: "text",
			in: Payload{
				ID:            "01JABCD",
				Source:        "macos.share-ext",
				SourceVersion: "0.1.0",
				Timestamp:     1714300000000,
				Text:          &Text{Body: "hello world", Mime: "text/plain"},
				Meta:          map[string]string{"lang": "en"},
			},
		},
		{
			name: "url",
			in: Payload{
				ID:            "01JABCE",
				Source:        "macos.shortcuts",
				SourceVersion: "0.1.0",
				Timestamp:     1714300001000,
				URL: &URL{
					Href:      "https://example.com/page",
					Title:     "Example",
					Selection: "selected text",
				},
			},
		},
		{
			name: "file",
			in: Payload{
				ID:            "01JABCF",
				Source:        "macos.share-ext",
				SourceVersion: "0.1.0",
				Timestamp:     1714300002000,
				File:          &File{Path: "/tmp/x.pdf", Mime: "application/pdf", Size: 4096},
			},
		},
		{
			name: "blob",
			in: Payload{
				ID:            "01JABCG",
				Source:        "macos.share-ext",
				SourceVersion: "0.1.0",
				Timestamp:     1714300003000,
				Blob: &Blob{
					Data:     []byte{0x89, 0x50, 0x4e, 0x47},
					Mime:     "image/png",
					Filename: "snap.png",
				},
			},
		},
	}
}

// negativeKindCase is one invalid raw-JSON literal violating the
// "exactly one kind" oneof constraint.
type negativeKindCase struct {
	name string
	raw  string
}

// negativeKindRawJSON returns invalid JSON literals that violate the
// "exactly one kind" oneof constraint, shared by Go UnmarshalJSON
// guard tests and JSON Schema rejection tests.
func negativeKindRawJSON() []negativeKindCase {
	return []negativeKindCase{
		{
			name: "empty object — zero kinds",
			raw:  `{}`,
		},
		{
			name: "header fields only — zero kinds",
			raw:  `{"id":"x","source":"s","timestamp":1}`,
		},
		{
			name: "two kinds — text + url",
			raw:  `{"id":"x","text":{"body":"hi"},"url":{"href":"https://e.io"}}`,
		},
		{
			name: "two kinds — file + blob",
			raw:  `{"id":"x","file":{"path":"/a"},"blob":{"data":"AA=="}}`,
		},
		{
			name: "all four kinds",
			raw: `{"id":"x","text":{"body":"hi"},"url":{"href":"https://e.io"},` +
				`"file":{"path":"/a"},"blob":{"data":"AA=="}}`,
		},
	}
}
