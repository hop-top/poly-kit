package bridge

import (
	"net/url"
	"strings"
)

// Match is the routing half of the bridge: given a payload and the
// manifests [Load] returned, it selects the rule that should handle
// the payload. It decides only WHO handles the payload; running that
// handler belongs to the receiver, and nothing in this package
// executes a process, opens a socket or touches the filesystem.
//
// Selection runs in three steps:
//
//  1. Kind. Every rule whose Kind equals the payload's oneof variant
//     becomes a candidate. Rules of other kinds are never examined.
//  2. Filters. Each candidate's Schemes, MIME and MaxSize must admit
//     the payload. A candidate failing any filter is dropped, and
//     the reason it failed is recorded.
//  3. Priority. The surviving candidate with the highest Priority
//     wins.
//
// # Fall-through
//
// A candidate rejected on scheme, MIME or size does not end
// dispatch: the next-highest surviving candidate is considered, and
// so on down. The package doc promises the "highest-priority
// handler", which is a statement about ordering the handlers that
// CAN take the payload, not about consulting only the first one.
// This is also the only behavior a shell integration can live with.
// A CLI declaring `kind: file, priority: 10, mime: [application/pdf]`
// is saying "I am the best answer for PDFs", not "I am the only
// answer for files"; failing a PNG share because a PDF handler
// outranks the image handler would make installing a specialist CLI
// break payloads that worked before it arrived. Filters are the
// rule's own statement of what it cannot take, so honoring them and
// moving on is what the manifest author asked for.
//
// # Ties
//
// Two surviving candidates with equal Priority are broken by
// manifest Name, ascending; a tie within one manifest is broken by
// the rule's index in that manifest's Accepts slice. [Load] already
// returns manifests sorted by Name, so first-loaded and
// name-ordering coincide there — but Match must not depend on that.
// A caller assembling manifests by hand, from a database, or from a
// map has no such guarantee, and a dispatcher that silently routes
// to a different CLI depending on Go's map iteration order is the
// failure mode worth engineering against. Name ordering is total,
// caller-independent and reproducible from the manifests alone, so
// the same input always names the same winner. Rejecting the tie as
// an error was the alternative; it is worse, because two unrelated
// CLIs both claiming urls at priority 5 is an ordinary install, not
// an operator mistake, and a share sheet that fails rather than
// picking one is not usable.
//
// # Empty and zero cases
//
// A nil or empty manifest slice yields ReasonNoManifests. A payload
// with zero or several content kinds set yields ReasonInvalidPayload
// — [Payload.UnmarshalJSON] already rejects such messages off the
// wire, so Match guards only against payloads built in Go. A
// manifest whose Accepts is empty contributes no candidates and is
// otherwise inert; it cannot win, and it never suppresses another
// manifest that can. [Manifest.Validate] rejects an empty Accepts,
// so Load never returns one, but Match does not assume its input
// came from Load.
//
// # Reporting
//
// The result always carries a verdict. On success Matched is true
// and Reason is [ReasonMatched]; on failure Matched is false and
// Reason names the rule that excluded the payload, drawn from the
// closed [NoMatchReason] set. No error is returned: finding no
// handler is a routing outcome the caller reports to a user, not a
// fault. See [NoMatchReason] for the precedence among reasons when
// different candidates failed differently.
func Match(p *Payload, manifests []*Manifest) Result {
	if len(manifests) == 0 {
		return Result{Reason: ReasonNoManifests}
	}
	kind, ok := payloadKind(p)
	if !ok {
		return Result{Reason: ReasonInvalidPayload}
	}

	fired := map[NoMatchReason]bool{}
	var best *Result

	for _, m := range manifests {
		if m == nil {
			continue
		}
		for i, r := range m.Accepts {
			if r.Kind != kind {
				continue
			}
			if reason := admits(r, p, kind); reason != ReasonMatched {
				fired[reason] = true
				continue
			}
			cand := Result{
				Matched:  true,
				Manifest: m,
				Rule:     m.Accepts[i],
				Index:    i,
			}
			if best == nil || outranks(cand, *best) {
				c := cand
				best = &c
			}
		}
	}

	if best != nil {
		return *best
	}
	if len(fired) == 0 {
		// No rule of this kind existed anywhere, so no filter ran.
		return Result{Reason: ReasonNoKindRule}
	}
	return Result{Reason: pickNoMatchReason(fired)}
}

// Result is the verdict Match returns. Matched reports whether a
// handler was found; on success Manifest and Rule name it, and on
// failure Reason names the rule that excluded the payload.
//
// The receiver consumes Manifest for the fields it needs to run the
// handler — Binary, Mode, Invoke and FallbackInproc — and Rule for
// the accept entry that won, which is what a --why or a debug log
// prints. Neither is copied: Manifest points into the slice the
// caller passed, so the receiver must not mutate it.
type Result struct {
	// Matched reports whether a handler was found. When false,
	// Manifest is nil and Reason is set.
	Matched bool

	// Manifest is the winning CLI's manifest, nil when Matched is
	// false. It aliases the element the caller passed in.
	Manifest *Manifest

	// Rule is the accept rule that won, a copy of the entry at
	// Index in Manifest.Accepts. Zero when Matched is false.
	Rule AcceptRule

	// Index is the winning rule's position in Manifest.Accepts. It
	// is the tie-break of last resort and lets a caller point at
	// the exact manifest entry in a diagnostic. Zero and
	// meaningless when Matched is false.
	Index int

	// Reason names the rule that left the payload unhandled.
	// [ReasonMatched] when Matched is true.
	Reason NoMatchReason
}

// outranks reports whether a should beat b. Higher Priority wins;
// ties fall to the lower manifest Name, then to the lower rule index
// within one manifest. Both keys are properties of the manifests
// themselves, so the winner does not depend on the order the caller
// supplied them.
func outranks(a, b Result) bool {
	if a.Rule.Priority != b.Rule.Priority {
		return a.Rule.Priority > b.Rule.Priority
	}
	if a.Manifest.Name != b.Manifest.Name {
		return a.Manifest.Name < b.Manifest.Name
	}
	return a.Index < b.Index
}

// payloadKind returns the payload's oneof variant. ok is false when
// zero or more than one content kind is set, mirroring the invariant
// [Payload.UnmarshalJSON] enforces on the wire.
func payloadKind(p *Payload) (Kind, bool) {
	if p == nil {
		return "", false
	}
	var kind Kind
	set := 0
	if p.Text != nil {
		kind, set = KindText, set+1
	}
	if p.URL != nil {
		kind, set = KindURL, set+1
	}
	if p.File != nil {
		kind, set = KindFile, set+1
	}
	if p.Blob != nil {
		kind, set = KindBlob, set+1
	}
	if set != 1 {
		return "", false
	}
	return kind, true
}

// admits reports whether rule r accepts payload p, whose variant is
// already known to be kind. It returns ReasonMatched when the rule
// admits the payload, and otherwise the reason it does not. Filters
// run cheapest-first, and the first failure short-circuits: a rule
// rejecting on two grounds reports one of them, and
// [pickNoMatchReason] resolves what the dispatch as a whole reports.
func admits(r AcceptRule, p *Payload, kind Kind) NoMatchReason {
	switch kind {
	case KindURL:
		if !schemeMatches(r.Schemes, p.URL.Href) {
			return ReasonSchemeMismatch
		}
		// MaxSize has no meaning for a url: the href is a
		// reference, not the content, and its length is not the
		// size of anything the handler would receive.
		return ReasonMatched

	case KindText:
		if !mimeMatches(r.MIME, p.Text.Mime) {
			return ReasonMIMEMismatch
		}
		return sizeVerdict(r.MaxSize, int64(len(p.Text.Body)))

	case KindFile:
		if !mimeMatches(r.MIME, p.File.Mime) {
			return ReasonMIMEMismatch
		}
		// File.Size is the shell's declared size and is optional
		// on the wire; a zero size is "not stated", and Match
		// does not stat the path to find out.
		return sizeVerdict(r.MaxSize, p.File.Size)

	case KindBlob:
		if !mimeMatches(r.MIME, p.Blob.Mime) {
			return ReasonMIMEMismatch
		}
		return sizeVerdict(r.MaxSize, int64(len(p.Blob.Data)))
	}
	return ReasonNoKindRule
}

// sizeVerdict applies a rule's MaxSize to a payload of size bytes. A
// MaxSize of zero is "no limit" — the field is optional on the wire
// and [AcceptRule.validate] admits zero while rejecting negatives,
// so an omitted max_size cannot mean "accept nothing". The limit is
// inclusive: a payload of exactly MaxSize bytes is admitted, which
// is what "max" reads as to a manifest author.
func sizeVerdict(maxSize, size int64) NoMatchReason {
	if maxSize > 0 && size > maxSize {
		return ReasonTooLarge
	}
	return ReasonMatched
}

// schemeMatches reports whether href's scheme is in schemes. Matching
// is case-insensitive per RFC 3986, which declares schemes
// case-insensitive and lowercase canonical; manifests in the wild
// write `https`, and a shell that hands over `HTTPS://…` must route
// the same way. Only the manifest side is folded here — url.Parse
// already lowercases the scheme it returns, so the payload side
// arrives canonical.
//
// [AcceptRule.validate] rejects a url rule with no schemes, so an
// empty list here comes from a hand-built manifest and matches
// nothing — the same reading validate gives it ("rule would match
// nothing").
func schemeMatches(schemes []string, href string) bool {
	if len(schemes) == 0 {
		return false
	}
	u, err := url.Parse(href)
	if err != nil || u.Scheme == "" {
		return false
	}
	for _, s := range schemes {
		if strings.EqualFold(s, u.Scheme) {
			return true
		}
	}
	return false
}

// mimeMatches reports whether mime satisfies any of the rule's
// patterns. Two pattern forms are supported, both already in use in
// the manifests this package ships as testdata:
//
//   - exact, e.g. "application/pdf" — matches that type only;
//   - type wildcard, e.g. "text/*" — matches every subtype of that
//     top-level type.
//
// A bare "*" or "*/*" matches any stated type. Comparison is
// case-insensitive (RFC 2045 declares type and subtype
// case-insensitive) and ignores parameters, so
// "text/plain; charset=utf-8" matches "text/plain".
//
// A payload with no MIME hint matches nothing: MIME is optional on
// the wire, and a rule that bothered to declare patterns is asking
// for a type it can handle, not for whatever arrives. Guessing from
// a file extension would be a second, weaker source of truth for the
// same field, and the contract carries only what the shell stated.
//
// [AcceptRule.validate] requires patterns on every non-url kind, so
// an empty list here comes from a hand-built manifest and matches
// nothing, consistent with validate rejecting it.
func mimeMatches(patterns []string, mime string) bool {
	if len(patterns) == 0 {
		return false
	}
	got := normalizeMIME(mime)
	if got == "" {
		return false
	}
	for _, p := range patterns {
		if mimePatternMatches(strings.ToLower(strings.TrimSpace(p)), got) {
			return true
		}
	}
	return false
}

// mimePatternMatches applies one already-lowercased pattern to an
// already-normalized MIME type.
func mimePatternMatches(pattern, mime string) bool {
	if pattern == "" {
		return false
	}
	if pattern == "*" || pattern == "*/*" {
		return true
	}
	if suffix, ok := strings.CutSuffix(pattern, "/*"); ok {
		typ, _, found := strings.Cut(mime, "/")
		return found && typ == suffix
	}
	return pattern == mime
}

// normalizeMIME lowercases a MIME type and strips any parameters
// after the first ";", so a charset or boundary does not defeat an
// exact pattern. Returns "" for an empty or parameter-only value.
func normalizeMIME(mime string) string {
	base, _, _ := strings.Cut(mime, ";")
	return strings.ToLower(strings.TrimSpace(base))
}
