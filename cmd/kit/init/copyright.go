// Package kitinit — copyright.go defines the Copyright type and grammar
// parser used by the multi-holder LICENSE template path.
//
// A Copyright captures one holder line of the form:
//
//	<year-or-range> <holder>[ <<URL>>]
//
// where year-or-range matches ^\d{4}(-\d{4})?$ and URL (optional) is
// wrapped in plain ASCII angle brackets. The angle-bracket form is
// preserved at template-render time so license detectors (GitHub /
// licensee / SPDX) still classify the file correctly — markdown link
// syntax breaks detection, hence the deliberate plain-text form.
//
// ParseCopyrights accepts the raw value slice from cobra's
// StringSliceVar binding for --author: each value may itself contain
// ";"-delimited holders. Concatenation order is preserved end-to-end.
//
// Backwards-compat: a value whose first whitespace-separated token is
// NOT a 4-digit year (e.g. legacy "Jane Doe") is treated as a single
// holder with Years = the current scaffold year and no URL.
package kitinit

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Copyright is one rendered LICENSE copyright line.
//
// Years carries either "YYYY" or "YYYY-YYYY"; the parser validates the
// shape and rejects reversed ranges at construction time. Holder is the
// untrimmed text between the year-space and any trailing " <URL>"
// suffix; URL is the bracketed-trimmed inner value (no angle brackets).
type Copyright struct {
	Years  string
	Holder string
	URL    string
}

// yearOrRangeRegex matches a single 4-digit year or a hyphen-joined
// range "YYYY-YYYY". The parser further validates that ranges are not
// reversed.
var yearOrRangeRegex = regexp.MustCompile(`^[0-9]{4}(-[0-9]{4})?$`)

// urlSuffixRegex captures a trailing " <URL>" segment when present.
// The leading space anchor ensures we don't strip URLs from holder
// names that happen to contain angle brackets mid-text.
var urlSuffixRegex = regexp.MustCompile(`\s+<([^<>]+)>\s*$`)

// ParseCopyrights walks the raw --author value slice and returns the
// resolved []Copyright. currentYear seeds the legacy single-name
// fallback path (when the first token is not a 4-digit year).
//
// Rules (locked by track plan):
//   - split each value on ";", trim whitespace per chunk
//   - empty chunks (consecutive ";" or trailing ";") are skipped silently
//   - per-chunk grammar: "<years> <holder>[ <<URL>>]"
//   - years: ^[0-9]{4}(-[0-9]{4})?$, reversed ranges reject
//   - if first whitespace-separated token is not a 4-digit year, treat
//     the whole chunk as legacy single-holder: Years = currentYear,
//     Holder = chunk, URL = ""
//   - errors carry the offending --author value index + chunk index
//     so users can locate the bad input quickly
func ParseCopyrights(values []string, currentYear int) ([]Copyright, error) {
	var out []Copyright
	for vi, v := range values {
		chunks := strings.Split(v, ";")
		for ci, c := range chunks {
			trimmed := strings.TrimSpace(c)
			if trimmed == "" {
				continue
			}
			cp, err := parseHolderChunk(trimmed, currentYear)
			if err != nil {
				return nil, fmt.Errorf("--author[%d] chunk %d (%q): %w", vi, ci, trimmed, err)
			}
			out = append(out, cp)
		}
	}
	return out, nil
}

// parseHolderChunk decodes one trimmed chunk into a Copyright. See
// ParseCopyrights for the grammar.
func parseHolderChunk(chunk string, currentYear int) (Copyright, error) {
	// Split off the optional trailing " <URL>" suffix first so the
	// holder text doesn't accidentally swallow it.
	var url string
	body := chunk
	if loc := urlSuffixRegex.FindStringSubmatchIndex(chunk); loc != nil {
		url = strings.TrimSpace(chunk[loc[2]:loc[3]])
		body = strings.TrimSpace(chunk[:loc[0]])
	} else if strings.Contains(chunk, "<") {
		// Sanity: an unmatched "<" with no terminating ">" past it
		// almost always means a fat-fingered URL. Surface a clear
		// error so the user fixes the input rather than silently
		// folding the unclosed segment into the holder name.
		open := strings.LastIndex(chunk, "<")
		close := strings.LastIndex(chunk, ">")
		if close < open {
			return Copyright{}, fmt.Errorf("missing closing '>' on URL")
		}
	}

	// First whitespace-separated token decides between strict and
	// legacy parse paths.
	parts := strings.SplitN(body, " ", 2)
	first := parts[0]

	if !yearOrRangeRegex.MatchString(first) {
		// Legacy: whole chunk is the holder name; current year, no URL.
		// Discard any captured URL because legacy callers never pass one.
		return Copyright{
			Years:  strconv.Itoa(currentYear),
			Holder: chunk,
			URL:    "",
		}, nil
	}

	// Strict path: validate the year range and require a non-empty holder.
	if err := validateYears(first); err != nil {
		return Copyright{}, err
	}
	if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
		return Copyright{}, fmt.Errorf("missing holder name after years %q", first)
	}
	holder := strings.TrimSpace(parts[1])
	return Copyright{
		Years:  first,
		Holder: holder,
		URL:    url,
	}, nil
}

// validateYears rejects reversed ranges (e.g. "2022-2016") and
// out-of-shape inputs. yearOrRangeRegex guarantees the syntactic
// shape upstream; this catches the semantic reversal.
func validateYears(years string) error {
	if !yearOrRangeRegex.MatchString(years) {
		return fmt.Errorf("invalid years %q (want YYYY or YYYY-YYYY)", years)
	}
	parts := strings.Split(years, "-")
	if len(parts) == 2 {
		start, _ := strconv.Atoi(parts[0])
		end, _ := strconv.Atoi(parts[1])
		if end < start {
			return fmt.Errorf("reversed year range %q (end %d < start %d)", years, end, start)
		}
	}
	return nil
}

// DefaultCopyrights returns the canonical 4-holder block used when no
// --author flag is supplied and no defaults.yaml override is present.
// year is the current scaffold year, frozen at first render.
func DefaultCopyrights(year int) []Copyright {
	y := strconv.Itoa(year)
	return []Copyright{
		{Years: y, Holder: "Idea Crafters LLC", URL: "https://ideacrafters.com"},
		{Years: y, Holder: "AI Experts", URL: "https://lesexperts.ai"},
		{Years: y, Holder: "@jadb", URL: "https://github.com/jadb"},
		{Years: y, Holder: "@monaam", URL: "https://github.com/monaam"},
	}
}
