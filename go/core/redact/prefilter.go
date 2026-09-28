package redact

import (
	"regexp/syntax"
	"unicode"
	"unicode/utf8"
)

// The prefilter decides, before any regex runs, which rules could
// possibly match an input. Two screens are derived from each rule's
// syntax tree:
//
//   - literal clauses: sets of literals such that every string the
//     pattern matches contains at least one member of each set. A rule
//     with a clause none of whose literals occurs in the input cannot
//     match it.
//   - minimum match length: a rule cannot match an input shorter than
//     the shortest string it matches.
//
// A rule for which no clause can be derived runs whenever the input is
// long enough.
//
// Literals and input are compared after ASCII case folding (A-Z to
// a-z, every other byte unchanged). Folding is a per-byte map, so a
// literal present in a string is present in its folded form, and a
// case-insensitive pattern's non-ASCII fold partners (the Kelvin sign
// for k, the long s for s) are listed as literals of their own. Both
// screens therefore err in one direction only: they can run a rule
// that finds nothing, never skip one that would match.
//
// The scanner indexes literals by their first two folded bytes and
// makes one pass over the input per screening.

const (
	// pfMaxExact caps the size of an exact string set carried up the
	// tree; larger sets are demoted to clauses.
	pfMaxExact = 64
	// pfMaxClass caps the runes a character class may hold and still
	// be enumerated as literals.
	pfMaxClass = 12
	// pfMaxClauses caps the clauses kept per node; the most selective
	// survive. Dropping a clause only weakens the screen.
	pfMaxClauses = 4
)

// pfInfo summarizes what a syntax node can match.
//
// When exact is set, the node matches only strings in set. Otherwise
// every match contains a member of each clause; no clauses means
// nothing is known.
type pfInfo struct {
	exact   bool
	set     []string
	clauses [][]string
}

var pfNone = pfInfo{}

func pfExact(set ...string) pfInfo { return pfInfo{exact: true, set: dedup(set)} }

// asReq returns the clauses info implies: an exact set with no empty
// member is itself a clause.
func (i pfInfo) asReq() pfInfo {
	if !i.exact {
		return i
	}
	if len(i.set) == 0 {
		return pfNone
	}
	for _, s := range i.set {
		if s == "" {
			return pfNone
		}
	}
	return pfInfo{clauses: [][]string{i.set}}
}

// betterClause reports whether clause a filters more than b: a longer
// shortest literal first, then fewer literals.
func betterClause(a, b []string) bool {
	ma, mb := minLen(a), minLen(b)
	if ma != mb {
		return ma > mb
	}
	return len(a) < len(b)
}

func minLen(set []string) int {
	m := -1
	for _, s := range set {
		if m < 0 || len(s) < m {
			m = len(s)
		}
	}
	return m
}

// bestClauses keeps the pfMaxClauses most selective clauses, dropping
// duplicates.
func bestClauses(cs [][]string) [][]string {
	var out [][]string
	for _, c := range cs {
		dup := false
		for _, o := range out {
			if sameSet(c, o) {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, c)
		}
	}
	// Insertion sort: clause lists are short.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && betterClause(out[j], out[j-1]); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	if len(out) > pfMaxClauses {
		out = out[:pfMaxClauses]
	}
	return out
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	m := make(map[string]bool, len(a))
	for _, s := range a {
		m[s] = true
	}
	for _, s := range b {
		if !m[s] {
			return false
		}
	}
	return true
}

// ruleScreen is what the prefilter knows about one pattern.
type ruleScreen struct {
	// clauses: every match contains a member of each; none means the
	// rule is screened by length alone.
	clauses [][]string
	// minLen is the fewest input bytes a match can span.
	minLen int
}

// screenFor derives the screen for pattern. A pattern that does not
// parse (it compiled, so this is not expected) gets an empty screen:
// the rule always runs.
func screenFor(pattern string) ruleScreen {
	re, err := syntax.Parse(pattern, syntax.Perl)
	if err != nil {
		return ruleScreen{}
	}
	re = re.Simplify()
	return ruleScreen{clauses: analyze(re).asReq().clauses, minLen: minMatchLen(re)}
}

func analyze(re *syntax.Regexp) pfInfo {
	switch re.Op {
	case syntax.OpEmptyMatch, syntax.OpBeginLine, syntax.OpEndLine,
		syntax.OpBeginText, syntax.OpEndText,
		syntax.OpWordBoundary, syntax.OpNoWordBoundary:
		return pfExact("")
	case syntax.OpLiteral:
		return analyzeLiteral(re)
	case syntax.OpCharClass:
		return analyzeClass(re.Rune)
	case syntax.OpCapture:
		return analyze(re.Sub[0])
	case syntax.OpQuest:
		sub := analyze(re.Sub[0])
		if sub.exact && len(sub.set) < pfMaxExact {
			return pfExact(append(append([]string(nil), sub.set...), "")...)
		}
		return pfNone
	case syntax.OpPlus:
		return analyze(re.Sub[0]).asReq()
	case syntax.OpRepeat:
		if re.Min >= 1 {
			return analyze(re.Sub[0]).asReq()
		}
		return pfNone
	case syntax.OpConcat:
		parts := make([]pfInfo, len(re.Sub))
		for i, s := range re.Sub {
			parts[i] = analyze(s)
		}
		return concatInfos(parts)
	case syntax.OpAlternate:
		return analyzeAlternate(re.Sub)
	default:
		// OpStar, OpAnyChar, OpAnyCharNotNL, OpNoMatch and anything
		// unknown: no requirement.
		return pfNone
	}
}

// analyzeLiteral treats each rune as the class of strings it matches:
// itself, or its fold orbit under (?i).
func analyzeLiteral(re *syntax.Regexp) pfInfo {
	fold := re.Flags&syntax.FoldCase != 0
	parts := make([]pfInfo, 0, len(re.Rune))
	for _, r := range re.Rune {
		runes := []rune{r}
		if fold {
			for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
				runes = append(runes, f)
			}
		}
		set, ok := foldedRunes(runes)
		if !ok {
			parts = append(parts, pfNone)
			continue
		}
		parts = append(parts, pfExact(set...))
	}
	return concatInfos(parts)
}

// analyzeClass enumerates a small class; ranges are inclusive pairs.
func analyzeClass(ranges []rune) pfInfo {
	var runes []rune
	for i := 0; i+1 < len(ranges); i += 2 {
		if int(ranges[i+1]-ranges[i])+1+len(runes) > pfMaxClass {
			return pfNone
		}
		for r := ranges[i]; r <= ranges[i+1]; r++ {
			runes = append(runes, r)
		}
	}
	set, ok := foldedRunes(runes)
	if !ok || len(set) == 0 {
		return pfNone
	}
	return pfExact(set...)
}

// foldedRunes renders runes as folded literals. The replacement
// character is refused: regexp matches it against any invalid UTF-8
// byte, which a literal search would not find.
func foldedRunes(runes []rune) ([]string, bool) {
	out := make([]string, 0, len(runes))
	for _, r := range runes {
		if r == utf8.RuneError || r < 0 || r > unicode.MaxRune {
			return nil, false
		}
		if 'A' <= r && r <= 'Z' {
			r += 'a' - 'A'
		}
		out = append(out, string(r))
	}
	return dedup(out), true
}

// concatInfos combines the infos of consecutive pieces. Runs of exact
// pieces are multiplied out while the product stays small. A match of
// the concatenation contains a match of every piece, so every run and
// every piece's clauses are clauses of the whole.
func concatInfos(parts []pfInfo) pfInfo {
	var (
		clauses [][]string
		run     = []string{""}
		allExt  = true // every part so far was exact
	)
	flush := func() {
		clauses = append(clauses, pfInfo{exact: true, set: run}.asReq().clauses...)
		run = []string{""}
	}
	for _, p := range parts {
		if !p.exact {
			allExt = false
			flush()
			clauses = append(clauses, p.clauses...)
			continue
		}
		if len(run)*len(p.set) > pfMaxExact {
			allExt = false
			flush()
			run = append([]string(nil), p.set...)
			continue
		}
		next := make([]string, 0, len(run)*len(p.set))
		for _, a := range run {
			for _, b := range p.set {
				next = append(next, a+b)
			}
		}
		run = dedup(next)
	}
	if allExt {
		return pfExact(run...)
	}
	flush()
	return pfInfo{clauses: bestClauses(clauses)}
}

// analyzeAlternate: the union of branches. Exact while every branch
// is exact and the union small. Otherwise one clause, the union of
// each branch's best clause, and none when any branch has none.
func analyzeAlternate(subs []*syntax.Regexp) pfInfo {
	infos := make([]pfInfo, len(subs))
	allExact, n := true, 0
	for i, s := range subs {
		infos[i] = analyze(s)
		allExact = allExact && infos[i].exact
		n += len(infos[i].set)
	}
	if allExact && n <= pfMaxExact {
		var set []string
		for _, info := range infos {
			set = append(set, info.set...)
		}
		return pfExact(set...)
	}
	var union []string
	for _, info := range infos {
		cs := info.asReq().clauses
		if len(cs) == 0 {
			return pfNone
		}
		best := cs[0]
		for _, c := range cs[1:] {
			if betterClause(c, best) {
				best = c
			}
		}
		union = append(union, best...)
	}
	return pfInfo{clauses: [][]string{dedup(union)}}
}

// minMatchLen returns a lower bound on the input bytes a match of re
// spans. Every rune counts at its shortest UTF-8 width; a rune set
// that admits U+FFFD counts one byte, as regexp matches it against a
// single invalid byte.
func minMatchLen(re *syntax.Regexp) int {
	const ceiling = 1 << 20
	clamp := func(n int) int { return min(n, ceiling) }
	switch re.Op {
	case syntax.OpLiteral:
		n := 0
		for _, r := range re.Rune {
			w := runeWidth(r)
			if re.Flags&syntax.FoldCase != 0 {
				for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
					w = min(w, runeWidth(f))
				}
			}
			n += w
		}
		return clamp(n)
	case syntax.OpCharClass:
		if len(re.Rune) == 0 {
			return 0
		}
		w := 4
		for i := 0; i+1 < len(re.Rune); i += 2 {
			lo, hi := re.Rune[i], re.Rune[i+1]
			if lo <= utf8.RuneError && utf8.RuneError <= hi {
				return 1
			}
			w = min(w, runeWidth(lo))
		}
		return w
	case syntax.OpAnyChar, syntax.OpAnyCharNotNL:
		return 1
	case syntax.OpCapture, syntax.OpPlus:
		return minMatchLen(re.Sub[0])
	case syntax.OpRepeat:
		return clamp(re.Min * minMatchLen(re.Sub[0]))
	case syntax.OpConcat:
		n := 0
		for _, s := range re.Sub {
			n = clamp(n + minMatchLen(s))
		}
		return n
	case syntax.OpAlternate:
		n := ceiling
		for _, s := range re.Sub {
			n = min(n, minMatchLen(s))
		}
		return n
	default:
		// Empty-width assertions, OpEmptyMatch, OpStar, OpQuest,
		// OpNoMatch: nothing guaranteed.
		return 0
	}
}

func runeWidth(r rune) int {
	if r == utf8.RuneError {
		return 1
	}
	if w := utf8.RuneLen(r); w > 0 {
		return w
	}
	return 1
}

func dedup(in []string) []string {
	if len(in) < 2 {
		return in
	}
	seen := make(map[string]bool, len(in))
	out := in[:0:0]
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// prefilter indexes every rule's screen for one pass over an input.
type prefilter struct {
	// Per rule: its clauses are ids [clauseLo[i], clauseHi[i]); none
	// means screened by length alone.
	clauseLo, clauseHi []int32
	minLen             []int
	nclauses           int

	lits   []string
	litIdx map[string]int32
	// litClauses lists the clauses each literal satisfies.
	litClauses [][]int32
	// one holds single-byte literals by byte; two holds longer
	// literals by their first two bytes, with twoBits as a fast
	// membership test.
	one     [256][]int32
	two     map[uint16][]int32
	twoBits [1 << 10]uint64
}

// add indexes the next rule, compiled from pattern.
func (p *prefilter) add(pattern string) {
	sc := screenFor(pattern)
	lo := int32(p.nclauses)
	for _, clause := range sc.clauses {
		cid := int32(p.nclauses)
		p.nclauses++
		for _, l := range clause {
			id := p.literal(l)
			p.litClauses[id] = append(p.litClauses[id], cid)
		}
	}
	p.clauseLo = append(p.clauseLo, lo)
	p.clauseHi = append(p.clauseHi, int32(p.nclauses))
	p.minLen = append(p.minLen, sc.minLen)
}

// literal returns l's id, indexing it on first sight.
func (p *prefilter) literal(l string) int32 {
	if id, ok := p.litIdx[l]; ok {
		return id
	}
	if p.litIdx == nil {
		p.litIdx = map[string]int32{}
		p.two = map[uint16][]int32{}
	}
	id := int32(len(p.lits))
	p.litIdx[l] = id
	p.lits = append(p.lits, l)
	p.litClauses = append(p.litClauses, nil)
	if len(l) == 1 {
		p.one[l[0]] = append(p.one[l[0]], id)
	} else {
		k := uint16(l[0])<<8 | uint16(l[1])
		p.two[k] = append(p.two[k], id)
		p.twoBits[k>>6] |= 1 << (k & 63)
	}
	return id
}

func (p *prefilter) words() int { return (len(p.minLen) + 63) / 64 }

func foldByte(b byte) byte {
	if 'A' <= b && b <= 'Z' {
		return b + 'a' - 'A'
	}
	return b
}

// candidates writes into dst (len p.words()) the rules that may match
// s: those s is long enough for whose every clause has a literal
// occurring in s.
func candidates[T string | []byte](p *prefilter, s T, dst []uint64) {
	clear(dst)
	var satBuf [32]uint64
	sat := satBuf[:]
	if n := (p.nclauses + 63) / 64; n > len(satBuf) {
		sat = make([]uint64, n)
	}
	var litBuf [16]uint64
	hit := litBuf[:]
	if n := (len(p.lits) + 63) / 64; n > len(litBuf) {
		hit = make([]uint64, n)
	}
	mark := func(id int32) {
		hit[id/64] |= 1 << (id % 64)
		for _, c := range p.litClauses[id] {
			sat[c/64] |= 1 << (c % 64)
		}
	}
	n := len(s)
	for i := 0; i < n; i++ {
		b0 := foldByte(s[i])
		for _, id := range p.one[b0] {
			if hit[id/64]&(1<<(id%64)) == 0 {
				mark(id)
			}
		}
		if i+1 >= n {
			break
		}
		k := uint16(b0)<<8 | uint16(foldByte(s[i+1]))
		if p.twoBits[k>>6]&(1<<(k&63)) == 0 {
			continue
		}
		for _, id := range p.two[k] {
			if hit[id/64]&(1<<(id%64)) != 0 {
				continue
			}
			l := p.lits[id]
			if len(l) > n-i {
				continue
			}
			j := 2
			for ; j < len(l); j++ {
				if foldByte(s[i+j]) != l[j] {
					break
				}
			}
			if j == len(l) {
				mark(id)
			}
		}
	}
rules:
	for ri := range p.minLen {
		if n < p.minLen[ri] {
			continue
		}
		for c := p.clauseLo[ri]; c < p.clauseHi[ri]; c++ {
			if sat[c/64]&(1<<(c%64)) == 0 {
				continue rules
			}
		}
		dst[ri/64] |= 1 << (ri % 64)
	}
}

func has(set []uint64, i int) bool { return set[i/64]&(1<<(i%64)) != 0 }
