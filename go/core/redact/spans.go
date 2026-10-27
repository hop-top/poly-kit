package redact

import (
	"bytes"
	"slices"
	"strings"
	"unicode/utf8"
)

// Apply works in three steps, all on the original input:
//
//  1. collect: every candidate rule finds its matches. Each match is
//     trimmed of leading and trailing '\n' (as gitleaks does), its
//     secret span picked (the secret group, else the whole match),
//     then checked against the entropy floor and the allowlists. What
//     survives is a hit.
//  2. merge: hits are sorted by start, then longest first, then rule
//     order. A hit that overlaps or touches the current region extends
//     it; the region keeps the rule of its first hit. So the earliest
//     span wins, ties go to the longest, then to the rule added first.
//  3. render: regions are replaced left to right in one pass.
//
// Replacement text never re-enters step 1.

// hit is one surviving finding: the secret span and its rule index.
type hit struct {
	start, end, rule int
}

// region is a merged span and the rule whose replacement it gets.
type region = hit

// collectMode selects allowlist semantics and bookkeeping.
type collectMode int

const (
	// redactRecord: Apply. Fail-closed allowlists; exemptions counted
	// and reported to OnAllowed.
	redactRecord collectMode = iota
	// redactQuiet: Spans. Same decisions, no bookkeeping.
	redactQuiet
	// detect: Scan. gitleaks allowlist semantics, no bookkeeping.
	detect
)

// regionsOf collects and merges s's hits for redaction.
func regionsOf[T string | []byte](r *Redactor, s T, record bool) []region {
	mode := redactQuiet
	if record {
		mode = redactRecord
	}
	return merge(collect(r, s, mode))
}

// merge folds overlapping and adjacent hits into regions; see above.
func merge(hits []hit) []region {
	if len(hits) == 0 {
		return nil
	}
	slices.SortFunc(hits, func(a, b hit) int {
		if a.start != b.start {
			return a.start - b.start
		}
		if a.end != b.end {
			return b.end - a.end
		}
		return a.rule - b.rule
	})
	out := hits[:1]
	for _, h := range hits[1:] {
		last := &out[len(out)-1]
		if h.start <= last.end {
			last.end = max(last.end, h.end)
			continue
		}
		out = append(out, h)
	}
	return out
}

// collect returns every rule's hits on s, in rule order then position.
func collect[T string | []byte](r *Redactor, s T, mode collectMode) []hit {
	var buf [8]uint64
	cand := r.candidates(s, buf[:])
	var hits []hit
	for i := range r.rules {
		if !has(cand, i) {
			continue
		}
		hits = r.ruleHits(s, i, hits, mode)
	}
	return hits
}

// ruleHits appends rule i's hits on s to hits.
func (r *Redactor) ruleHits(s any, i int, hits []hit, mode collectMode) []hit {
	rule := &r.rules[i]
	var locs, subs [][]int
	switch v := s.(type) {
	case string:
		if rule.spec.SecretGroup > 0 {
			subs = rule.re.FindAllStringSubmatchIndex(v, -1)
			locs = subs
		} else {
			locs = rule.re.FindAllStringIndex(v, -1)
		}
		if len(locs) == 0 {
			return hits
		}
		return r.judge(v, rule, i, locs, subs, hits, mode)
	case []byte:
		if rule.spec.SecretGroup > 0 {
			subs = rule.re.FindAllSubmatchIndex(v, -1)
			locs = subs
		} else {
			locs = rule.re.FindAllIndex(v, -1)
		}
		if len(locs) == 0 {
			return hits
		}
		return r.judge(string(v), rule, i, locs, subs, hits, mode)
	}
	return hits
}

// judge turns rule matches into hits: trim, pick the secret, then
// apply the entropy floor and the allowlists.
func (r *Redactor) judge(s string, rule *Rule, i int, locs, subs [][]int, hits []hit, mode collectMode) []hit {
	for k, loc := range locs {
		ms, me := loc[0], loc[1]
		for ms < me && s[ms] == '\n' {
			ms++
		}
		for me > ms && s[me-1] == '\n' {
			me--
		}
		ss, se := ms, me
		if g := rule.spec.SecretGroup; g > 0 && subs != nil {
			gs, ge := max(subs[k][2*g], ms), min(subs[k][2*g+1], me)
			if subs[k][2*g] >= 0 && ge > gs {
				ss, se = gs, ge
			}
		}
		if se <= ss {
			continue
		}
		secret := s[ss:se]
		if rule.spec.Entropy != 0 && shannonEntropy(secret) <= rule.spec.Entropy {
			continue
		}
		if r.exempt(rule, s, secret, ms, me, mode == detect) {
			if mode == redactRecord {
				r.recordAllowed(rule, secret, ss, se)
			}
			continue
		}
		hits = append(hits, hit{start: ss, end: se, rule: i})
	}
	return hits
}

// exempt reports whether an exact entry or an allowlist (global, then
// the rule's) exempts the finding whose match is s[ms:me].
func (r *Redactor) exempt(rule *Rule, s, secret string, ms, me int, detect bool) bool {
	match := s[ms:me]
	for _, lists := range [][]string{r.exactAllowlist, rule.spec.AllowlistExact} {
		for _, e := range lists {
			if e != "" && (e == secret || e == match) {
				return true
			}
		}
	}
	if len(r.allowlists) == 0 && len(rule.allowlists) == 0 {
		return false
	}
	f := finding{secret: secret, match: match}
	if r.needLine {
		f.line = lineAround(s, ms, me)
	}
	for _, lists := range [][]allowlist{r.allowlists, rule.allowlists} {
		for k := range lists {
			if lists[k].allows(f, detect) {
				return true
			}
		}
	}
	return false
}

// lineAround returns the line(s) holding s[start:end], without their
// newline characters.
func lineAround(s string, start, end int) string {
	ls := strings.LastIndexByte(s[:start], '\n') + 1
	le := strings.IndexByte(s[end:], '\n')
	if le < 0 {
		return s[ls:]
	}
	return s[ls : end+le]
}

// candidates returns the rules to run on s as a bitset: those passing
// their keyword gate and, unless noPrefilter, the literal screen.
// buf is scratch space the result may alias.
func (r *Redactor) candidates(s any, buf []uint64) []uint64 {
	n := (len(r.rules) + 63) / 64
	if n > len(buf) {
		buf = make([]uint64, n)
	}
	buf = buf[:n]
	switch v := s.(type) {
	case string:
		screen(r, v, buf)
	case []byte:
		screen(r, v, buf)
	}
	return buf
}

func screen[T string | []byte](r *Redactor, s T, dst []uint64) {
	lower, ascii := keywordText(s)
	if r.noPrefilter {
		clear(dst)
		for i := range r.rules {
			if keywordGate(r.rules[i].spec.Keywords, lower) {
				dst[i/64] |= 1 << (i % 64)
			}
		}
		return
	}
	if ascii {
		candidates(r.both, s, dst)
		return
	}
	var kwBuf [8]uint64
	kw := kwBuf[:]
	if len(dst) > len(kw) {
		kw = make([]uint64, len(dst))
	}
	kw = kw[:len(dst)]
	candidates(r.pf, s, dst)
	candidates(r.kw, lower.(T), kw)
	for i := range dst {
		dst[i] &= kw[i]
	}
}

// keywordText is what keyword gates search: s lower-cased as gitleaks
// does (strings.ToLower). The literal scanner folds ASCII itself, so
// only input with non-ASCII bytes is rewritten.
// ascii reports that s was left as is.
func keywordText[T string | []byte](s T) (lower any, ascii bool) {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			switch v := any(s).(type) {
			case string:
				return strings.ToLower(v), false
			case []byte:
				return bytes.ToLower(v), false
			}
		}
	}
	return any(s), true
}

// keywordGate is the reference keyword check: no keywords, or one of
// them occurs in the lower-cased text.
func keywordGate(keywords []string, lower any) bool {
	if len(keywords) == 0 {
		return true
	}
	var text string
	switch v := lower.(type) {
	case string:
		text = strings.ToLower(v)
	case []byte:
		text = strings.ToLower(string(v))
	}
	for _, k := range keywords {
		if strings.Contains(text, k) {
			return true
		}
	}
	return false
}
