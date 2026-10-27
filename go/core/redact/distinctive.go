package redact

import "regexp/syntax"

// minDistinctive is the shortest mandatory literal prefix that makes a
// rule distinctive.
const minDistinctive = 3

// maxClassRunes is the widest class that still counts as one literal
// position.
const maxClassRunes = 4

// isDistinctive reports whether every match of pattern starts with a
// literal of at least minDistinctive runes (after empty-width
// assertions): ghp_, xoxb-, AKIA, sk-ant-. Such a rule names its own
// token shape, so redaction trusts it without the keyword gate or the
// entropy floor. A rule whose match can start with a class or an
// optional piece (generic-api-key and the keyword-context rules) is
// generic.
//
// The walk is on the parsed, unsimplified tree, and ports walk their
// own parse the same way: literals count their runes, a class of at
// most four runes counts as one literal rune, an
// alternation contributes its shortest branch and ends the prefix
// unless every branch is literal entirely (`(?:sk|rk)_`), a
// repeat with a minimum of one contributes its operand's prefix and
// ends it, anything else ends it.
func isDistinctive(pattern string) bool {
	re, err := syntax.Parse(pattern, syntax.Perl)
	if err != nil {
		return false
	}
	n, _ := literalPrefix(re)
	return n >= minDistinctive
}

// literalPrefix returns the mandatory literal prefix length of re in
// runes, and whether re is that literal entirely (so a concatenation
// may continue past it).
func literalPrefix(re *syntax.Regexp) (int, bool) {
	switch re.Op {
	case syntax.OpEmptyMatch, syntax.OpBeginLine, syntax.OpEndLine,
		syntax.OpBeginText, syntax.OpEndText,
		syntax.OpWordBoundary, syntax.OpNoWordBoundary:
		return 0, true
	case syntax.OpLiteral:
		return len(re.Rune), true
	case syntax.OpCharClass:
		if smallClass(re.Rune) {
			return 1, true
		}
		return 0, false
	case syntax.OpCapture:
		return literalPrefix(re.Sub[0])
	case syntax.OpConcat:
		total := 0
		for _, sub := range re.Sub {
			n, whole := literalPrefix(sub)
			total += n
			if !whole {
				return total, false
			}
		}
		return total, true
	case syntax.OpAlternate:
		least, whole := -1, true
		for _, sub := range re.Sub {
			n, w := literalPrefix(sub)
			whole = whole && w
			if least < 0 || n < least {
				least = n
			}
		}
		return max(least, 0), whole
	case syntax.OpPlus:
		n, _ := literalPrefix(re.Sub[0])
		return n, false
	case syntax.OpRepeat:
		if re.Min >= 1 {
			n, _ := literalPrefix(re.Sub[0])
			return n, false
		}
	}
	return 0, false
}

// smallClass reports whether the class holds at most four runes: a
// case-fold orbit ([Pp]) or a factored alternation (gh[su]_ from
// ghu_|ghs_), which pins its position as well as a literal does.
func smallClass(ranges []rune) bool {
	n := 0
	for i := 0; i+1 < len(ranges); i += 2 {
		n += int(ranges[i+1]-ranges[i]) + 1
		if n > maxClassRunes {
			return false
		}
	}
	return n > 0
}
