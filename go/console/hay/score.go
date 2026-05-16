package hay

import "strings"

// Subsequence scores query against candidate using case-insensitive
// ordered character matching with position bonuses.
func Subsequence(query, candidate string) int {
	q := strings.ToLower(query)
	c := strings.ToLower(candidate)
	qi := 0
	score := 0
	for ci := 0; ci < len(c) && qi < len(q); ci++ {
		if c[ci] != q[qi] {
			continue
		}
		qi++
		score++ // matched char
		if ci == 0 {
			score += 2 // start bonus
		} else if c[ci-1] == '/' || c[ci-1] == '-' || c[ci-1] == '_' {
			score++ // word boundary bonus
		}
	}
	if qi < len(q) {
		return 0
	}
	return score
}

// Substring scores query against candidate using case-insensitive
// substring containment.
func Substring(query, candidate string) int {
	q := strings.ToLower(query)
	c := strings.ToLower(candidate)
	idx := strings.Index(c, q)
	if idx < 0 {
		return 0
	}
	score := len(q) * 2
	if idx == 0 {
		score += 3 // prefix bonus
	} else if c[idx-1] == '/' || c[idx-1] == '-' || c[idx-1] == '_' || c[idx-1] == '.' {
		score += 2 // word boundary bonus
	}
	return score
}

// Levenshtein scores query against candidate by inverting edit distance.
func Levenshtein(query, candidate string) int {
	q := strings.ToLower(query)
	c := strings.ToLower(candidate)
	dist := editDistance(q, c)
	if dist > len(q) {
		return 0
	}
	maxLen := len(q)
	if len(c) > maxLen {
		maxLen = len(c)
	}
	s := maxLen - dist
	if s < 0 {
		return 0
	}
	return s
}

// Combined returns the maximum of Subsequence and Substring scores.
func Combined(query, candidate string) int {
	a := Subsequence(query, candidate)
	b := Substring(query, candidate)
	if a > b {
		return a
	}
	return b
}

// StringScore wraps a string-based scorer for generic use with ScoreFn[T].
func StringScore[T any](extract func(T) string, score func(string, string) int) ScoreFn[T] {
	return func(query string, item T) int {
		return score(query, extract(item))
	}
}

// editDistance computes Levenshtein distance using a single-row DP approach.
func editDistance(a, b string) int {
	la, lb := len(a), len(b)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	prev := make([]int, lb+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		cur := make([]int, lb+1)
		cur[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			del := prev[j] + 1
			ins := cur[j-1] + 1
			sub := prev[j-1] + cost
			cur[j] = min(del, min(ins, sub))
		}
		prev = cur
	}
	return prev[lb]
}
