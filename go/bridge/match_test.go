package bridge

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mf builds a manifest with the given name and accept rules. Every
// other field is filled with something Validate accepts, so a test
// can assert the fixtures it routes over are legal manifests.
func mf(name string, rules ...AcceptRule) *Manifest {
	return &Manifest{
		Name:    name,
		Version: "1.0.0",
		Binary:  name,
		Mode:    ModeSubprocess,
		Accepts: rules,
	}
}

func textPayload(body, mime string) *Payload {
	return &Payload{ID: "p", Text: &Text{Body: body, Mime: mime}}
}

func urlPayload(href string) *Payload {
	return &Payload{ID: "p", URL: &URL{Href: href}}
}

func filePayload(path, mime string, size int64) *Payload {
	return &Payload{ID: "p", File: &File{Path: path, Mime: mime, Size: size}}
}

func blobPayload(data []byte, mime string) *Payload {
	return &Payload{ID: "p", Blob: &Blob{Data: data, Mime: mime}}
}

// TestMatch_KindSelection walks all four oneof variants against one
// manifest carrying exactly one rule per kind. Each payload must pick
// the rule for its own kind and no other.
func TestMatch_KindSelection(t *testing.T) {
	m := mf(
		"all",
		AcceptRule{Kind: KindURL, Priority: 1, Schemes: []string{"https"}},
		AcceptRule{Kind: KindText, Priority: 1, MIME: []string{"text/*"}},
		AcceptRule{Kind: KindFile, Priority: 1, MIME: []string{"application/pdf"}},
		AcceptRule{Kind: KindBlob, Priority: 1, MIME: []string{"image/png"}},
	)
	require.NoError(t, m.Validate(), "fixture must be a legal manifest")

	cases := []struct {
		name     string
		payload  *Payload
		wantKind Kind
	}{
		{"text", textPayload("hi", "text/plain"), KindText},
		{"url", urlPayload("https://example.com"), KindURL},
		{"file", filePayload("/tmp/x.pdf", "application/pdf", 10), KindFile},
		{"blob", blobPayload([]byte{1, 2}, "image/png"), KindBlob},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Match(tc.payload, []*Manifest{m})
			require.True(t, got.Matched, "reason: %s", got.Reason)
			assert.Equal(t, ReasonMatched, got.Reason)
			assert.Equal(t, tc.wantKind, got.Rule.Kind)
			assert.Same(t, m, got.Manifest)
		})
	}
}

// TestMatch_PriorityWins asserts the highest Priority takes the
// payload regardless of the order manifests are supplied in.
func TestMatch_PriorityWins(t *testing.T) {
	low := mf("aaa-low", AcceptRule{Kind: KindURL, Priority: 1, Schemes: []string{"https"}})
	high := mf("zzz-high", AcceptRule{Kind: KindURL, Priority: 10, Schemes: []string{"https"}})

	t.Run("high supplied last", func(t *testing.T) {
		got := Match(urlPayload("https://e.io"), []*Manifest{low, high})
		require.True(t, got.Matched)
		assert.Equal(t, "zzz-high", got.Manifest.Name)
	})
	t.Run("high supplied first", func(t *testing.T) {
		got := Match(urlPayload("https://e.io"), []*Manifest{high, low})
		require.True(t, got.Matched)
		assert.Equal(t, "zzz-high", got.Manifest.Name)
	})
}

// TestMatch_TieBrokenByManifestName pins the documented tie rule:
// equal Priority falls to the lower manifest Name, and the answer
// does not move when the caller reorders the slice.
func TestMatch_TieBrokenByManifestName(t *testing.T) {
	alpha := mf("alpha", AcceptRule{Kind: KindURL, Priority: 5, Schemes: []string{"https"}})
	beta := mf("beta", AcceptRule{Kind: KindURL, Priority: 5, Schemes: []string{"https"}})

	orders := [][]*Manifest{
		{alpha, beta},
		{beta, alpha},
	}
	for i, order := range orders {
		got := Match(urlPayload("https://e.io"), order)
		require.True(t, got.Matched)
		assert.Equal(t, "alpha", got.Manifest.Name,
			"order %d: name ordering must decide, not slice order", i)
	}
}

// TestMatch_TieWithinManifestBrokenByIndex pins the last-resort
// tie-break: two equal-priority rules of the same kind in one
// manifest resolve to the earlier Accepts entry.
func TestMatch_TieWithinManifestBrokenByIndex(t *testing.T) {
	m := mf(
		"solo",
		AcceptRule{Kind: KindText, Priority: 5, MIME: []string{"text/plain"}},
		AcceptRule{Kind: KindText, Priority: 5, MIME: []string{"text/*"}},
	)
	got := Match(textPayload("hi", "text/plain"), []*Manifest{m})
	require.True(t, got.Matched)
	assert.Equal(t, 0, got.Index, "earlier rule must win an intra-manifest tie")
	assert.Equal(t, []string{"text/plain"}, got.Rule.MIME)
}

// TestMatch_FallThroughPastFilters is the core fall-through
// assertion: the top-priority candidate is rejected on a filter and
// the next-highest must still be considered, once per filter kind.
func TestMatch_FallThroughPastFilters(t *testing.T) {
	cases := []struct {
		name      string
		blocked   *Manifest // priority 10, rejects the payload
		fallback  *Manifest // priority 1, accepts it
		payload   *Payload
		wantWiner string
	}{
		{
			name:      "scheme rejects top candidate",
			blocked:   mf("blocked", AcceptRule{Kind: KindURL, Priority: 10, Schemes: []string{"file"}}),
			fallback:  mf("fallback", AcceptRule{Kind: KindURL, Priority: 1, Schemes: []string{"https"}}),
			payload:   urlPayload("https://e.io"),
			wantWiner: "fallback",
		},
		{
			name:      "mime rejects top candidate",
			blocked:   mf("blocked", AcceptRule{Kind: KindFile, Priority: 10, MIME: []string{"application/pdf"}}),
			fallback:  mf("fallback", AcceptRule{Kind: KindFile, Priority: 1, MIME: []string{"image/*"}}),
			payload:   filePayload("/tmp/a.png", "image/png", 10),
			wantWiner: "fallback",
		},
		{
			name:      "size rejects top candidate",
			blocked:   mf("blocked", AcceptRule{Kind: KindText, Priority: 10, MIME: []string{"text/*"}, MaxSize: 4}),
			fallback:  mf("fallback", AcceptRule{Kind: KindText, Priority: 1, MIME: []string{"text/*"}}),
			payload:   textPayload("0123456789", "text/plain"),
			wantWiner: "fallback",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.NoError(t, tc.blocked.Validate())
			require.NoError(t, tc.fallback.Validate())

			got := Match(tc.payload, []*Manifest{tc.blocked, tc.fallback})
			require.True(t, got.Matched,
				"fall-through must reach the lower-priority handler; reason: %s", got.Reason)
			assert.Equal(t, tc.wantWiner, got.Manifest.Name)
		})
	}
}

// TestMatch_FallThroughWithinOneManifest asserts fall-through is not
// a cross-manifest-only behavior: a manifest's own lower-priority
// rule takes the payload its top rule filtered out.
func TestMatch_FallThroughWithinOneManifest(t *testing.T) {
	m := mf(
		"solo",
		AcceptRule{Kind: KindFile, Priority: 10, MIME: []string{"application/pdf"}},
		AcceptRule{Kind: KindFile, Priority: 1, MIME: []string{"image/*"}},
	)
	got := Match(filePayload("/tmp/a.png", "image/png", 1), []*Manifest{m})
	require.True(t, got.Matched, "reason: %s", got.Reason)
	assert.Equal(t, 1, got.Index)
}

// TestMatch_SchemeFilter covers scheme matching across the forms a
// shell can produce, including case folding.
func TestMatch_SchemeFilter(t *testing.T) {
	cases := []struct {
		name    string
		schemes []string
		href    string
		want    bool
	}{
		{"exact https", []string{"http", "https"}, "https://e.io/x", true},
		{"exact http", []string{"http", "https"}, "http://e.io/x", true},
		{"scheme not listed", []string{"https"}, "ftp://e.io/x", false},
		{"uppercase href scheme folds", []string{"https"}, "HTTPS://e.io/x", true},
		{"uppercase rule scheme folds", []string{"HTTPS"}, "https://e.io/x", true},
		{"relative href has no scheme", []string{"https"}, "/just/a/path", false},
		{"empty href", []string{"https"}, "", false},
		{"custom scheme", []string{"obsidian"}, "obsidian://open?x=1", true},
		// validate only requires the scheme list be non-empty, so a
		// hand-built rule can carry an empty-string entry. A
		// scheme-less href must not satisfy it by both being "".
		{"empty rule scheme does not match a scheme-less href", []string{""}, "/just/a/path", false},
		{"empty rule scheme does not match an empty href", []string{""}, "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := mf("c", AcceptRule{Kind: KindURL, Priority: 1, Schemes: tc.schemes})
			got := Match(urlPayload(tc.href), []*Manifest{m})
			assert.Equal(t, tc.want, got.Matched)
			if !tc.want {
				assert.Equal(t, ReasonSchemeMismatch, got.Reason)
			}
		})
	}
}

// TestMatch_SchemeFilter_EmptySchemesMatchesNothing pins the reading
// AcceptRule.validate gives an empty scheme list: "rule would match
// nothing". Such a rule cannot reach Match via Load, so this guards
// a hand-built manifest.
func TestMatch_SchemeFilter_EmptySchemesMatchesNothing(t *testing.T) {
	m := mf("c", AcceptRule{Kind: KindURL, Priority: 1})
	require.Error(t, m.Validate(), "fixture is deliberately invalid")

	got := Match(urlPayload("https://e.io"), []*Manifest{m})
	assert.False(t, got.Matched)
	assert.Equal(t, ReasonSchemeMismatch, got.Reason)
}

// TestMatch_MIMEFilter covers exact and prefix patterns against all
// three MIME-carrying kinds, so a filter that only worked for text
// cannot pass.
func TestMatch_MIMEFilter(t *testing.T) {
	cases := []struct {
		name     string
		patterns []string
		mime     string
		want     bool
	}{
		{"exact match", []string{"application/pdf"}, "application/pdf", true},
		{"exact miss", []string{"application/pdf"}, "application/json", false},
		{"prefix wildcard matches subtype", []string{"text/*"}, "text/markdown", true},
		{"prefix wildcard rejects other type", []string{"text/*"}, "image/png", false},
		{"prefix wildcard is not a substring test", []string{"text/*"}, "application/text", false},
		{"prefix wildcard matches the whole type, not its prefix", []string{"text/*"}, "textual/plain", false},
		{"prefix wildcard rejects a shorter type", []string{"textual/*"}, "text/plain", false},
		{"exact pattern does not match by prefix", []string{"text/plain"}, "text/plaintext", false},
		{"second pattern in list matches", []string{"application/pdf", "image/png"}, "image/png", true},
		{"case folds on payload", []string{"application/pdf"}, "APPLICATION/PDF", true},
		{"case folds on pattern", []string{"Application/PDF"}, "application/pdf", true},
		{"parameters stripped", []string{"text/plain"}, "text/plain; charset=utf-8", true},
		{"parameters stripped under wildcard", []string{"text/*"}, "text/html;charset=utf-8", true},
		{"full wildcard", []string{"*/*"}, "anything/goes", true},
		{"bare star", []string{"*"}, "anything/goes", true},
		{"absent mime matches nothing", []string{"text/*"}, "", false},
		{"whitespace-only mime matches nothing", []string{"text/*"}, "   ", false},
	}

	kinds := []struct {
		name    string
		kind    Kind
		payload func(mime string) *Payload
	}{
		{"text", KindText, func(mime string) *Payload { return textPayload("body", mime) }},
		{"file", KindFile, func(mime string) *Payload { return filePayload("/tmp/x", mime, 1) }},
		{"blob", KindBlob, func(mime string) *Payload { return blobPayload([]byte{1}, mime) }},
	}

	for _, k := range kinds {
		t.Run(k.name, func(t *testing.T) {
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					m := mf("c", AcceptRule{Kind: k.kind, Priority: 1, MIME: tc.patterns})
					got := Match(k.payload(tc.mime), []*Manifest{m})
					assert.Equal(t, tc.want, got.Matched)
					if !tc.want {
						assert.Equal(t, ReasonMIMEMismatch, got.Reason)
					}
				})
			}
		})
	}
}

// TestMatch_MIMEFilter_EmptyPatternsMatchNothing pins the hand-built
// counterpart to the empty-schemes case: validate requires patterns
// on non-url kinds, and Match reads their absence the same way.
func TestMatch_MIMEFilter_EmptyPatternsMatchNothing(t *testing.T) {
	m := mf("c", AcceptRule{Kind: KindText, Priority: 1})
	require.Error(t, m.Validate(), "fixture is deliberately invalid")

	got := Match(textPayload("hi", "text/plain"), []*Manifest{m})
	assert.False(t, got.Matched)
	assert.Equal(t, ReasonMIMEMismatch, got.Reason)
}

// TestMatch_MaxSizeBoundary walks the limit for every sized kind:
// one under, exactly at, and one over. Exactly at the limit must be
// admitted.
func TestMatch_MaxSizeBoundary(t *testing.T) {
	const limit int64 = 8

	kinds := []struct {
		name    string
		kind    Kind
		payload func(size int64) *Payload
	}{
		{
			name: "text",
			kind: KindText,
			payload: func(size int64) *Payload {
				return textPayload(string(make([]byte, size)), "text/plain")
			},
		},
		{
			name: "file",
			kind: KindFile,
			payload: func(size int64) *Payload {
				return filePayload("/tmp/x", "text/plain", size)
			},
		},
		{
			name: "blob",
			kind: KindBlob,
			payload: func(size int64) *Payload {
				return blobPayload(make([]byte, size), "text/plain")
			},
		},
	}

	sizes := []struct {
		name string
		size int64
		want bool
	}{
		{"under limit", limit - 1, true},
		{"exactly at limit", limit, true},
		{"one over limit", limit + 1, false},
	}

	for _, k := range kinds {
		t.Run(k.name, func(t *testing.T) {
			for _, s := range sizes {
				t.Run(s.name, func(t *testing.T) {
					m := mf("c", AcceptRule{
						Kind: k.kind, Priority: 1,
						MIME: []string{"text/*"}, MaxSize: limit,
					})
					require.NoError(t, m.Validate())

					got := Match(k.payload(s.size), []*Manifest{m})
					assert.Equal(t, s.want, got.Matched,
						"size %d against max_size %d", s.size, limit)
					if !s.want {
						assert.Equal(t, ReasonTooLarge, got.Reason)
					}
				})
			}
		})
	}
}

// TestMatch_MaxSizeZeroMeansNoLimit pins the optional-field reading:
// an omitted max_size cannot mean "accept nothing", because validate
// admits zero while rejecting negatives.
func TestMatch_MaxSizeZeroMeansNoLimit(t *testing.T) {
	m := mf("c", AcceptRule{Kind: KindBlob, Priority: 1, MIME: []string{"image/png"}})
	require.NoError(t, m.Validate())

	got := Match(blobPayload(make([]byte, 1<<20), "image/png"), []*Manifest{m})
	assert.True(t, got.Matched, "reason: %s", got.Reason)
}

// TestMatch_URLIgnoresMaxSize asserts a max_size on a url rule does
// not gate the href's length: the href is a reference, not content.
func TestMatch_URLIgnoresMaxSize(t *testing.T) {
	m := mf("c", AcceptRule{
		Kind: KindURL, Priority: 1,
		Schemes: []string{"https"}, MaxSize: 4,
	})
	require.NoError(t, m.Validate())

	got := Match(urlPayload("https://example.com/a/very/long/path"), []*Manifest{m})
	assert.True(t, got.Matched, "reason: %s", got.Reason)
}

// TestMatch_NoMatchReasons walks every failure mode end to end and
// pins the reason each produces.
func TestMatch_NoMatchReasons(t *testing.T) {
	cases := []struct {
		name      string
		payload   *Payload
		manifests []*Manifest
		want      NoMatchReason
	}{
		{
			name:      "nil manifests",
			payload:   textPayload("hi", "text/plain"),
			manifests: nil,
			want:      ReasonNoManifests,
		},
		{
			name:      "empty manifest slice",
			payload:   textPayload("hi", "text/plain"),
			manifests: []*Manifest{},
			want:      ReasonNoManifests,
		},
		{
			name:      "nil payload",
			payload:   nil,
			manifests: []*Manifest{mf("c", AcceptRule{Kind: KindText, Priority: 1, MIME: []string{"text/*"}})},
			want:      ReasonInvalidPayload,
		},
		{
			name:      "payload with no variant set",
			payload:   &Payload{ID: "p"},
			manifests: []*Manifest{mf("c", AcceptRule{Kind: KindText, Priority: 1, MIME: []string{"text/*"}})},
			want:      ReasonInvalidPayload,
		},
		{
			name:      "payload with two variants set",
			payload:   &Payload{ID: "p", Text: &Text{Body: "hi"}, URL: &URL{Href: "https://e.io"}},
			manifests: []*Manifest{mf("c", AcceptRule{Kind: KindText, Priority: 1, MIME: []string{"text/*"}})},
			want:      ReasonInvalidPayload,
		},
		{
			name:      "no rule of this kind",
			payload:   blobPayload([]byte{1}, "image/png"),
			manifests: []*Manifest{mf("c", AcceptRule{Kind: KindURL, Priority: 1, Schemes: []string{"https"}})},
			want:      ReasonNoKindRule,
		},
		{
			name:      "manifest with empty accepts contributes nothing",
			payload:   textPayload("hi", "text/plain"),
			manifests: []*Manifest{mf("empty")},
			want:      ReasonNoKindRule,
		},
		{
			name:      "all candidates filtered on scheme",
			payload:   urlPayload("ftp://e.io"),
			manifests: []*Manifest{mf("c", AcceptRule{Kind: KindURL, Priority: 1, Schemes: []string{"https"}})},
			want:      ReasonSchemeMismatch,
		},
		{
			name:      "all candidates filtered on mime",
			payload:   filePayload("/tmp/a.png", "image/png", 1),
			manifests: []*Manifest{mf("c", AcceptRule{Kind: KindFile, Priority: 1, MIME: []string{"application/pdf"}})},
			want:      ReasonMIMEMismatch,
		},
		{
			name:    "all candidates filtered on size",
			payload: textPayload("0123456789", "text/plain"),
			manifests: []*Manifest{
				mf("c", AcceptRule{Kind: KindText, Priority: 1, MIME: []string{"text/*"}, MaxSize: 4}),
			},
			want: ReasonTooLarge,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Match(tc.payload, tc.manifests)
			assert.False(t, got.Matched)
			assert.Nil(t, got.Manifest)
			assert.Equal(t, tc.want, got.Reason)
			assert.True(t, got.Reason.IsValid())
		})
	}
}

// TestMatch_ReasonPrecedence asserts a dispatch failing on several
// grounds at once reports the most actionable one — size over MIME
// over scheme — rather than whichever manifest happened to come
// first in the slice.
func TestMatch_ReasonPrecedence(t *testing.T) {
	// A url payload where one rule rejects on scheme; and a
	// separate run where text rules reject on both mime and size.
	tooBig := mf("aaa", AcceptRule{Kind: KindText, Priority: 1, MIME: []string{"text/*"}, MaxSize: 2})
	wrongMIME := mf("zzz", AcceptRule{Kind: KindText, Priority: 9, MIME: []string{"application/json"}})

	for i, order := range [][]*Manifest{{tooBig, wrongMIME}, {wrongMIME, tooBig}} {
		got := Match(textPayload("0123456789", "text/plain"), order)
		assert.False(t, got.Matched)
		assert.Equal(t, ReasonTooLarge, got.Reason,
			"order %d: size rejection is the more actionable reason", i)
	}
}

// TestMatch_EmptyAcceptsDoesNotSuppressAnother asserts an inert
// manifest never shadows a manifest that can take the payload.
func TestMatch_EmptyAcceptsDoesNotSuppressAnother(t *testing.T) {
	empty := mf("aaa-empty")
	real := mf("bbb-real", AcceptRule{Kind: KindText, Priority: 1, MIME: []string{"text/*"}})

	got := Match(textPayload("hi", "text/plain"), []*Manifest{empty, real})
	require.True(t, got.Matched, "reason: %s", got.Reason)
	assert.Equal(t, "bbb-real", got.Manifest.Name)
}

// TestMatch_NilManifestEntrySkipped asserts a nil element in the
// slice is stepped over rather than panicking the receiver.
func TestMatch_NilManifestEntrySkipped(t *testing.T) {
	real := mf("real", AcceptRule{Kind: KindText, Priority: 1, MIME: []string{"text/*"}})

	got := Match(textPayload("hi", "text/plain"), []*Manifest{nil, real})
	require.True(t, got.Matched, "reason: %s", got.Reason)
	assert.Equal(t, "real", got.Manifest.Name)
}

// TestMatch_DoesNotMutateInput asserts Match is a pure read over the
// manifests it is handed: the winner aliases the caller's element and
// the rule values are untouched.
func TestMatch_DoesNotMutateInput(t *testing.T) {
	rule := AcceptRule{Kind: KindURL, Priority: 3, Schemes: []string{"https"}, MaxSize: 99}
	m := mf("c", rule)

	got := Match(urlPayload("https://e.io"), []*Manifest{m})
	require.True(t, got.Matched)
	assert.Same(t, m, got.Manifest)
	assert.Equal(t, rule, m.Accepts[0], "Match must not rewrite the rule")
	assert.Equal(t, rule, got.Rule, "Result.Rule is a copy of the winning entry")
}

// TestMatch_OverLoadedManifests routes over the manifests Load
// actually returns from testdata, so the matcher is exercised
// against the real YAML fixtures rather than only hand-built structs.
func TestMatch_OverLoadedManifests(t *testing.T) {
	ms, err := Load("testdata/manifests")
	require.NoError(t, err)
	require.Len(t, ms, 2)

	t.Run("url goes to the higher-priority ctxt rule", func(t *testing.T) {
		// ctxt declares url priority 10, tlc priority 1.
		got := Match(urlPayload("https://example.com/page"), ms)
		require.True(t, got.Matched, "reason: %s", got.Reason)
		assert.Equal(t, "ctxt", got.Manifest.Name)
		assert.Equal(t, 10, got.Rule.Priority)
	})

	t.Run("text ties at priority 5 and ctxt wins on name", func(t *testing.T) {
		got := Match(textPayload("hello", "text/plain"), ms)
		require.True(t, got.Matched, "reason: %s", got.Reason)
		assert.Equal(t, "ctxt", got.Manifest.Name)
	})

	t.Run("json file falls through tlc to ctxt", func(t *testing.T) {
		// tlc's file rule lists only text/* and application/pdf;
		// ctxt's also lists application/json. Both sit at
		// priority 5, so name ordering would pick ctxt anyway —
		// assert the rule that won actually admits json.
		got := Match(filePayload("/tmp/a.json", "application/json", 128), ms)
		require.True(t, got.Matched, "reason: %s", got.Reason)
		assert.Equal(t, "ctxt", got.Manifest.Name)
		assert.Contains(t, got.Rule.MIME, "application/json")
	})

	t.Run("oversized text falls through ctxt to tlc", func(t *testing.T) {
		// ctxt caps text at 1 MiB; tlc's text rule sets no cap.
		big := textPayload(string(make([]byte, 1048577)), "text/plain")
		got := Match(big, ms)
		require.True(t, got.Matched, "reason: %s", got.Reason)
		assert.Equal(t, "tlc", got.Manifest.Name)
	})

	t.Run("text exactly at ctxt's cap stays with ctxt", func(t *testing.T) {
		exact := textPayload(string(make([]byte, 1048576)), "text/plain")
		got := Match(exact, ms)
		require.True(t, got.Matched, "reason: %s", got.Reason)
		assert.Equal(t, "ctxt", got.Manifest.Name)
	})

	t.Run("blob has no handler", func(t *testing.T) {
		got := Match(blobPayload([]byte{1}, "image/png"), ms)
		assert.False(t, got.Matched)
		assert.Equal(t, ReasonNoKindRule, got.Reason)
	})
}

// TestNoMatchReason_Vocabulary pins the closed-set contract: every
// reason validates, explains itself, and an invented one does not.
func TestNoMatchReason_Vocabulary(t *testing.T) {
	all := AllNoMatchReasons()
	require.NotEmpty(t, all)

	seen := map[NoMatchReason]bool{}
	for _, r := range all {
		assert.True(t, r.IsValid(), "%s must validate", r)
		assert.NotEmpty(t, r.String(), "reason must have a wire value")
		assert.NotEmpty(t, r.Explain(), "%s must explain itself", r)
		assert.False(t, seen[r], "%s appears twice in precedence", r)
		seen[r] = true
	}

	assert.True(t, ReasonMatched.IsValid(), "the zero value is valid")
	assert.Empty(t, ReasonMatched.String())
	assert.Empty(t, ReasonMatched.Explain(), "the absence of a reason explains nothing")
	assert.False(t, NoMatchReason("invented").IsValid())
	assert.Empty(t, NoMatchReason("invented").Explain())
}

// TestAllNoMatchReasons_ReturnsCopy asserts a caller mutating the
// returned slice cannot corrupt the package's precedence order.
func TestAllNoMatchReasons_ReturnsCopy(t *testing.T) {
	first := AllNoMatchReasons()
	require.NotEmpty(t, first)
	original := first[0]
	first[0] = "clobbered"

	second := AllNoMatchReasons()
	assert.Equal(t, original, second[0])
}
